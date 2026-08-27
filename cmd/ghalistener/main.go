package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/scaler"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
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

func run(ctx context.Context, cfg *config.Config) error {
	ghConfig, err := actions.ParseGitHubConfigFromURL(cfg.ConfigureURL)
	if err != nil {
		return fmt.Errorf("failed to parse GitHub config from URL: %w", err)
	}

	logger, err := cfg.Logger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	scalesetClient, err := cfg.ActionsClient(logger)
	if err != nil {
		return fmt.Errorf("failed to create actions client: %w", err)
	}

	scaleSets := cfg.EffectiveScaleSets()

	// The metrics server (registry + HTTP endpoint) is shared across all scale
	// sets; each scale set records under its own metrics recorder so the series
	// carry a distinct "name" label.
	var metricsServer *metrics.Server
	if cfg.MetricsAddr != "" {
		metricsServer = metrics.NewServer(metrics.ServerConfig{
			Enterprise:     ghConfig.Enterprise,
			Organization:   ghConfig.Organization,
			Repository:     ghConfig.Repository,
			ServerAddr:     cfg.MetricsAddr,
			ServerEndpoint: cfg.MetricsEndpoint,
			Metrics:        cfg.Metrics,
			Logger:         logger.With("component", "metrics exporter"),
		})
	}

	// A failure in one scale set's session must not tear down the others, so we
	// do NOT use errgroup's shared context to cancel siblings. Each scale set is
	// supervised independently; only ctx cancellation (shutdown) ends the run.
	g, ctx := errgroup.WithContext(ctx)
	metricsCtx, cancelMetrics := context.WithCancelCause(ctx)

	// Single-set (legacy) mode keeps the original semantics exactly: the listener
	// error propagates, the errgroup returns it, and the pod exits so the
	// AutoscalingListener controller recreates it. Multi-set mode supervises each
	// session in-process so one bad session cannot take down its siblings.
	multiSet := len(cfg.ScaleSets) > 0

	for _, ss := range scaleSets {
		ss := ss
		var recorder metrics.Recorder = metrics.Discard
		if metricsServer != nil {
			recorder = metricsServer.RecorderFor(ss.EphemeralRunnerSetName, cfg.EphemeralRunnerSetNamespace)
		}
		g.Go(func() error {
			if multiSet {
				return superviseScaleSet(ctx, cfg, scalesetClient, ss, recorder, logger)
			}
			err := runOneScaleSet(ctx, cfg, scalesetClient, ss, recorder, logger)
			cancelMetrics(fmt.Errorf("listener exited: %w", err))
			return err
		})
	}

	if metricsServer != nil {
		g.Go(func() error {
			logger.Info("Starting metrics server")
			return metricsServer.ListenAndServe(metricsCtx)
		})
	}

	err = g.Wait()
	cancelMetrics(errors.New("all scale set listeners exited"))
	return err
}

// superviseScaleSet runs one scale set's listener and restarts it with capped
// backoff on transient errors, so a bad session cannot take down its siblings.
// It returns nil when ctx is cancelled (graceful shutdown).
func superviseScaleSet(
	ctx context.Context,
	cfg *config.Config,
	scalesetClient *scaleset.Client,
	ss config.ScaleSetConfig,
	recorder metrics.Recorder,
	logger *slog.Logger,
) error {
	log := logger.With("scaleSet", ss.EphemeralRunnerSetName, "scaleSetID", ss.RunnerScaleSetID)
	return supervise(ctx, log, func() error {
		return runOneScaleSet(ctx, cfg, scalesetClient, ss, recorder, log)
	})
}

// Backoff bounds for the multi-session supervisor. Declared as vars so tests can
// shrink them; not meant to be tuned at runtime.
var (
	superviseMinBackoff = 1 * time.Second
	superviseMaxBackoff = 2 * time.Minute
)

// supervise runs runOnce repeatedly, restarting it with capped exponential
// backoff whenever it returns, until ctx is cancelled. It never returns a
// non-nil error while ctx is live, so one scale set's failures stay contained to
// its own goroutine and cannot cancel siblings sharing an errgroup. It returns
// nil once ctx is cancelled (graceful shutdown).
func supervise(ctx context.Context, log *slog.Logger, runOnce func() error) error {
	backoff := superviseMinBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}

		err := runOnce()
		switch {
		case ctx.Err() != nil:
			// Shutdown requested; a wrapped context error here is expected.
			return nil
		case err == nil:
			// The listener returned without error and without shutdown; treat as
			// a session that ended and should be re-established.
			log.Info("Scale set listener exited, restarting")
			backoff = superviseMinBackoff
		default:
			log.Error("Scale set listener failed, restarting after backoff", "error", err, "backoff", backoff.String())
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, superviseMaxBackoff)
	}
}

// runOneScaleSet establishes one message session and runs its listener + scaler
// once. It returns when the session ends (error or clean exit).
func runOneScaleSet(
	ctx context.Context,
	cfg *config.Config,
	scalesetClient *scaleset.Client,
	ss config.ScaleSetConfig,
	recorder metrics.Recorder,
	logger *slog.Logger,
) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = uuid.NewString()
		logger.Info("Failed to get hostname, fallback to uuid", "uuid", hostname, "error", err)
	}
	// In the legacy single-set case the owner stays the bare hostname (unchanged
	// behaviour). When one listener multiplexes several scale sets, each session
	// needs a distinct owner so GitHub does not treat siblings on the same host
	// as the same session.
	owner := hostname
	if len(cfg.ScaleSets) > 0 {
		owner = fmt.Sprintf("%s-%d", hostname, ss.RunnerScaleSetID)
	}

	sessionClient, err := scalesetClient.MessageSessionClient(ctx, ss.RunnerScaleSetID, owner)
	if err != nil {
		return fmt.Errorf("failed to create actions message session client: %w", err)
	}
	defer func() {
		if err := sessionClient.Close(context.Background()); err != nil {
			logger.Error("Failed to close session client", "error", err)
		}
	}()

	var listenerOptions []listener.Option
	if recorder != nil && recorder != metrics.Discard {
		listenerOptions = append(listenerOptions, listener.WithMetricsRecorder(recorder))
		recorder.RecordStatic(ss.MinRunners, ss.MaxRunners)
	}

	l, err := listener.New(
		sessionClient,
		listener.Config{
			ScaleSetID: ss.RunnerScaleSetID,
			MaxRunners: ss.MaxRunners,
			Logger:     logger.With("component", "listener"),
		},
		listenerOptions...,
	)
	if err != nil {
		return fmt.Errorf("failed to create new listener: %w", err)
	}

	s, err := scaler.New(
		scaler.Config{
			EphemeralRunnerSetNamespace: cfg.EphemeralRunnerSetNamespace,
			EphemeralRunnerSetName:      ss.EphemeralRunnerSetName,
			MaxRunners:                  ss.MaxRunners,
			MinRunners:                  ss.MinRunners,
		},
		scaler.WithLogger(logger.With("component", "worker")),
	)
	if err != nil {
		return fmt.Errorf("failed to create new kubernetes worker: %w", err)
	}

	logger.Info("Starting listener")
	return l.Run(ctx, s)
}
