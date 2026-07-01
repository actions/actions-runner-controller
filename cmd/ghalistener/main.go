package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/scaler"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset/listener"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	configPath, ok := os.LookupEnv("LISTENER_CONFIG_PATH")
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: LISTENER_CONFIG_PATH environment variable is not set\n")
		os.Exit(1)
	}

	config, err := config.Read(ctx, configPath)
	if err != nil {
		log.Printf("Failed to read config: %v", err)
		os.Exit(1)
	}

	if err := run(ctx, config); err != nil {
		log.Printf("Application returned an error: %v", err)
		os.Exit(1)
	}
}

// newOTLPTraceExporter builds the OTLP/HTTP span exporter. A non-empty
// endpoint is a full base URL (the OTLP spec form of
// OTEL_EXPORTER_OTLP_ENDPOINT, e.g. http://collector:4318); the
// /v1/traces signal path is appended when the URL has no path. With an
// empty endpoint the SDK's spec-compliant OTEL_EXPORTER_OTLP_* env
// handling applies (endpoint, headers, TLS, ...).
func newOTLPTraceExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	if endpoint == "" {
		return otlptracehttp.New(ctx)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid OTel endpoint %q: %w", endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid OTel endpoint %q: scheme must be http or https", endpoint)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(u.String()))
}

func run(ctx context.Context, config *config.Config) error {
	ghConfig, err := actions.ParseGitHubConfigFromURL(config.ConfigureURL)
	if err != nil {
		return fmt.Errorf("failed to parse GitHub config from URL: %w", err)
	}

	logger, err := config.Logger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	var metricsExporter metrics.ServerExporter
	if config.MetricsAddr != "" {
		metricsExporter = metrics.NewExporter(metrics.ExporterConfig{
			ScaleSetName:      config.EphemeralRunnerSetName,
			ScaleSetNamespace: config.EphemeralRunnerSetNamespace,
			Enterprise:        ghConfig.Enterprise,
			Organization:      ghConfig.Organization,
			Repository:        ghConfig.Repository,
			ServerAddr:        config.MetricsAddr,
			ServerEndpoint:    config.MetricsEndpoint,
			Metrics:           config.Metrics,
			Logger:            logger.With("component", "metrics exporter"),
		})
	}

	// OTel trace recorder (optional). Enabled by the listener config
	// (otel_endpoint, wired from the controller) or by the standard
	// OTEL_EXPORTER_OTLP_* environment variables.
	var otelRecorder *metrics.OTelRecorder
	otelEnabled := config.OTelEndpoint != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
	if otelEnabled {
		otlpExporter, err := newOTLPTraceExporter(ctx, config.OTelEndpoint)
		if err != nil {
			return fmt.Errorf("failed to create OTel exporter: %w", err)
		}
		otelRecorder = metrics.NewOTelRecorder(metrics.OTelRecorderConfig{
			Exporter:          otlpExporter,
			Logger:            logger.With("component", "otel recorder"),
			ServerURL:         ghConfig.ConfigURL.Scheme + "://" + ghConfig.ConfigURL.Host,
			ScaleSetName:      config.EphemeralRunnerSetName,
			ScaleSetNamespace: config.EphemeralRunnerSetNamespace,
		})
		// Shut down with a fresh bounded context: the signal ctx is
		// already canceled on SIGINT/SIGTERM, which would abort the
		// final flush of queued spans.
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := otelRecorder.Shutdown(sctx); err != nil {
				logger.Error("Failed to shut down OTel recorder", "error", err)
			}
		}()
		endpointSource := config.OTelEndpoint
		if endpointSource == "" {
			endpointSource = "OTEL_EXPORTER_OTLP_* environment"
		}
		logger.Info("OTel trace recorder enabled", "endpoint", endpointSource)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = uuid.NewString()
		logger.Info("Failed to get hostname, fallback to uuid", "uuid", hostname, "error", err)
	}

	scalesetClient, err := config.ActionsClient(logger)
	if err != nil {
		return fmt.Errorf("failed to create actions client: %w", err)
	}

	sessionClient, err := scalesetClient.MessageSessionClient(
		ctx,
		config.RunnerScaleSetID,
		hostname,
	)
	if err != nil {
		return fmt.Errorf("failed to create actions message session client: %w", err)
	}
	defer func() {
		if err := sessionClient.Close(context.Background()); err != nil {
			logger.Error("Failed to close session client", "error", err)
		}
	}()

	var listenerOptions []listener.Option

	// Build the metrics recorder: Prometheus, OTel, or both
	var recorder listener.MetricsRecorder
	switch {
	case metricsExporter != nil && otelRecorder != nil:
		recorder = metrics.NewComposite(metricsExporter, otelRecorder)
	case metricsExporter != nil:
		recorder = metricsExporter
	case otelRecorder != nil:
		recorder = otelRecorder
	}

	if recorder != nil {
		listenerOptions = append(
			listenerOptions,
			listener.WithMetricsRecorder(recorder),
		)
	}
	if metricsExporter != nil {
		metricsExporter.RecordStatic(config.MinRunners, config.MaxRunners)
	}

	listener, err := listener.New(
		sessionClient,
		listener.Config{
			ScaleSetID: config.RunnerScaleSetID,
			MaxRunners: config.MaxRunners,
			Logger:     logger.With("component", "listener"),
		},
		listenerOptions...,
	)
	if err != nil {
		return fmt.Errorf("failed to create new listener: %w", err)
	}

	scaler, err := scaler.New(
		scaler.Config{
			EphemeralRunnerSetNamespace: config.EphemeralRunnerSetNamespace,
			EphemeralRunnerSetName:      config.EphemeralRunnerSetName,
			MaxRunners:                  config.MaxRunners,
			MinRunners:                  config.MinRunners,
		},
		scaler.WithLogger(logger.With("component", "worker")),
	)
	if err != nil {
		return fmt.Errorf("failed to create new kubernetes worker: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	metricsCtx, cancelMetrics := context.WithCancelCause(ctx)

	g.Go(func() error {
		logger.Info("Starting listener")
		listnerErr := listener.Run(ctx, scaler)
		cancelMetrics(fmt.Errorf("listener exited: %w", listnerErr))
		return listnerErr
	})

	if metricsExporter != nil {
		g.Go(func() error {
			logger.Info("Starting metrics server")
			return metricsExporter.ListenAndServe(metricsCtx)
		})
	}

	return g.Wait()
}
