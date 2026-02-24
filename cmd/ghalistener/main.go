package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/scaler"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset/listener"
	"github.com/google/uuid"
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

func run(ctx context.Context, config *config.Config) error {
	ghConfig, err := actions.ParseGitHubConfigFromURL(config.ConfigureUrl)
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
	if metricsExporter != nil {
		listenerOptions = append(
			listenerOptions,
			listener.WithMetricsRecorder(
				metricsExporter,
			),
		)
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
