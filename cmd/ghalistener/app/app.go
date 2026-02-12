package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/listener"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/worker"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

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
}

//go:generate mockery
type Listener interface {
	Listen(ctx context.Context, handler listener.Handler) error
}

//go:generate mockery
type Worker interface {
	HandleJobStarted(ctx context.Context, jobInfo *actions.JobStarted) error
	HandleDesiredRunnerCount(ctx context.Context, count int, jobsCompleted int) (int, error)
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
	sessionClient, err := app.scalesetClient.MessageSessionClient(
		ctx,
		app.config.RunnerScaleSetID,
		app.hostname,
	)
	if err != nil {
		return fmt.Errorf("failed to create actions message session client: %w", err)
	}
	defer sessionClient.Close(context.Background())

	listener, err := listener.New(listener.Config{
		Client:     sessionClient,
		ScaleSetID: app.config.RunnerScaleSetID,
		MinRunners: app.config.MinRunners,
		MaxRunners: app.config.MaxRunners,
		Logger:     *app.logger.With("component", "listener"),
		Metrics:    app.metrics,
	})
	if err != nil {
		return fmt.Errorf("failed to create new listener: %w", err)
	}

	worker, err := worker.New(
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

	g, ctx := errgroup.WithContext(ctx)
	metricsCtx, cancelMetrics := context.WithCancelCause(ctx)

	g.Go(func() error {
		app.logger.Info("Starting listener")
		listnerErr := listener.Listen(ctx, worker)
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
