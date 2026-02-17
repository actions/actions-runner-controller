package app

//go:generate mockery

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/worker"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

// Listener interface wraps (*scaleset/listener.Listener).Run as Listen
// for testability (test uses listener.On("Listen", ...))
type Listener interface {
	Listen(ctx context.Context, scaler listener.Scaler) error
}

// Worker is the interface for handling scale set messages (= listener.Scaler)
type Worker interface {
	listener.Scaler
}

// listenerAdapter bridges *listener.Listener (has Run) to the Listener interface (needs Listen)
type listenerAdapter struct {
	*listener.Listener
}

func (a *listenerAdapter) Listen(ctx context.Context, scaler listener.Scaler) error {
	return a.Listener.Run(ctx, scaler)
}

// App is responsible for initializing required components and running the app.
type App struct {
	// configured fields
	config   *config.Config
	ghConfig *actions.GitHubConfig
	logger   *slog.Logger

	// initialized fields
	scalesetClient *scaleset.Client
	hostname       string
	metrics        metrics.ServerExporter

	// injectable dependencies for testing
	listener Listener
	worker   Worker
}

func New(config config.Config) (*App, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	app := &App{
		config: &config,
	}

	ghConfig, err := actions.ParseGitHubConfigFromURL(config.ConfigureUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub config from URL: %w", err)
	}
	app.ghConfig = ghConfig

	logger, err := config.Logger()
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}
	app.logger = logger

	scalesetClient, err := config.ActionsClient(*logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create actions client: %w", err)
	}
	app.scalesetClient = scalesetClient

	if config.MetricsAddr != "" {
		app.metrics = metrics.NewExporter(metrics.ExporterConfig{
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
	app.hostname = hostname

	return app, nil
}

func (app *App) Run(ctx context.Context) error {
	var l Listener
	var w Worker

	if app.listener == nil || app.worker == nil {
		sessionClient, err := app.scalesetClient.MessageSessionClient(
			ctx,
			app.config.RunnerScaleSetID,
			app.hostname,
		)
		if err != nil {
			return fmt.Errorf("failed to create actions message session client: %w", err)
		}
		defer sessionClient.Close(context.Background())

		hasMetrics := app.metrics != nil

		var listenerOptions []listener.Option
		if hasMetrics {
			listenerOptions = append(
				listenerOptions,
				listener.WithMetricsRecorder(
					&metricsRecorder{metrics: app.metrics},
				),
			)
			app.metrics.PublishStatic(app.config.MinRunners, app.config.MaxRunners)
		}

		concreteListener, err := listener.New(
			sessionClient,
			listener.Config{
				ScaleSetID: app.config.RunnerScaleSetID,
				MaxRunners: app.config.MaxRunners,
				Logger:     app.logger.With("component", "listener"),
			},
			listenerOptions...,
		)
		if err != nil {
			return fmt.Errorf("failed to create new listener: %w", err)
		}

		concreteWorker, err := worker.New(
			worker.Config{
				EphemeralRunnerSetNamespace: app.config.EphemeralRunnerSetNamespace,
				EphemeralRunnerSetName:      app.config.EphemeralRunnerSetName,
				MaxRunners:                  app.config.MaxRunners,
				MinRunners:                  app.config.MinRunners,
			},
			worker.WithLogger(app.logger.With("component", "worker")),
		)
		if err != nil {
			return fmt.Errorf("failed to create new kubernetes worker: %w", err)
		}

		l = &listenerAdapter{Listener: concreteListener}
		w = concreteWorker
	} else {
		l = app.listener
		w = app.worker
	}

	g, ctx := errgroup.WithContext(ctx)
	metricsCtx, cancelMetrics := context.WithCancelCause(ctx)

	g.Go(func() error {
		app.logger.Info("Starting listener")
		listnerErr := l.Listen(ctx, w)
		cancelMetrics(fmt.Errorf("Listener exited: %w", listnerErr))
		return listnerErr
	})

	if app.metrics != nil {
		g.Go(func() error {
			app.logger.Info("Starting metrics server")
			return app.metrics.ListenAndServe(metricsCtx)
		})
	}

	return g.Wait()
}

var _ listener.MetricsRecorder = (*metricsRecorder)(nil)

type metricsRecorder struct {
	metrics metrics.Publisher // The publisher used to publish metrics.
}

// RecordDesiredRunners implements [listener.MetricsRecorder].
func (m *metricsRecorder) RecordDesiredRunners(count int) {
	m.metrics.PublishDesiredRunners(count)
}

// RecordJobCompleted implements [listener.MetricsRecorder].
func (m *metricsRecorder) RecordJobCompleted(msg *scaleset.JobCompleted) {
	m.metrics.PublishJobCompleted(msg)
}

// RecordJobStarted implements [listener.MetricsRecorder].
func (m *metricsRecorder) RecordJobStarted(msg *scaleset.JobStarted) {
	m.metrics.PublishJobStarted(msg)
}

// RecordStatistics implements [listener.MetricsRecorder].
func (m *metricsRecorder) RecordStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	m.metrics.PublishStatistics(statistics)
}
