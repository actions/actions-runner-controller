package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/listener"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/worker"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
)

// App is responsible for initializing required components and running the app.
type App struct {
	// configured fields
	config config.Config
	logger logr.Logger

	// initialized fields
	listener Listener
	worker   Worker
	metrics  metrics.ServerPublisher
}

//go:generate mockery --name Listener --output ./mocks --outpkg mocks --case underscore
type Listener interface {
	Listen(ctx context.Context, handler listener.Handler) error
}

//go:generate mockery --name Worker --output ./mocks --outpkg mocks --case underscore
type Worker interface {
	HandleJobStarted(ctx context.Context, jobInfo *actions.JobStarted) error
	HandleDesiredRunnerCount(ctx context.Context, count int, jobsCompleted int) (int, error)
}

func New(config config.Config) (*App, error) {
	app := &App{
		config: config,
	}

	ghConfig, err := actions.ParseGitHubConfigFromURL(config.ConfigureUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub config from URL: %w", err)
	}

	{
		logger, err := config.Logger()
		if err != nil {
			return nil, fmt.Errorf("failed to create logger: %w", err)
		}
		app.logger = logger.WithName("listener-app")
	}

	actionsClient, err := config.ActionsClient(app.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create actions client: %w", err)
	}

	if config.MetricsAddr != "" {
		app.metrics = metrics.NewExporter(metrics.ExporterConfig{
			ScaleSetName:      config.EphemeralRunnerSetName,
			ScaleSetNamespace: config.EphemeralRunnerSetNamespace,
			Enterprise:        ghConfig.Enterprise,
			Organization:      ghConfig.Organization,
			Repository:        ghConfig.Repository,
			ServerAddr:        config.MetricsAddr,
			ServerEndpoint:    config.MetricsEndpoint,
		})
	}

	worker, err := worker.New(
		worker.Config{
			EphemeralRunnerSetNamespace: config.EphemeralRunnerSetNamespace,
			EphemeralRunnerSetName:      config.EphemeralRunnerSetName,
			MaxRunners:                  config.MaxRunners,
			MinRunners:                  config.MinRunners,
		},
		worker.WithLogger(app.logger.WithName("worker")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create new kubernetes worker: %w", err)
	}
	app.worker = worker

	listener, err := listener.New(listener.Config{
		Client:     actionsClient,
		ScaleSetID: app.config.RunnerScaleSetId,
		MinRunners: app.config.MinRunners,
		MaxRunners: app.config.MaxRunners,
		Logger:     app.logger.WithName("listener"),
		Metrics:    app.metrics,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create new listener: %w", err)
	}
	app.listener = listener

	app.logger.Info("app initialized")

	return app, nil
}

func (app *App) Run(ctx context.Context) error {
	var errs []error
	if app.worker == nil {
		errs = append(errs, fmt.Errorf("worker not initialized"))
	}
	if app.listener == nil {
		errs = append(errs, fmt.Errorf("listener not initialized"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("app not initialized: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		app.logger.Info("Starting listener")
		return app.listener.Listen(ctx, app.worker)
	})

	if app.metrics != nil {
		g.Go(func() error {
			app.logger.Info("Starting metrics server")
			return app.metrics.ListenAndServe(ctx)
		})
	}

	return g.Wait()
}
