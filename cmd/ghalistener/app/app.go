package app

import (
	"context"
	"crypto/x509"
	"fmt"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/listener"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/worker"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
)

type App struct {
	actionsClient *actions.Client

	config config.Config
	logger logr.Logger

	listener *listener.Listener
	worker   *worker.Worker
	metrics  metrics.ServerPublisher
}

func New(config config.Config) (*App, error) {
	app := &App{
		config: config,
	}

	ghConfig, err := actions.ParseGitHubConfigFromURL(config.ConfigureUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub config from URL: %w", err)
	}

	if err := app.initLogger(); err != nil {
		return nil, err
	}

	if err := app.initActionsClient(); err != nil {
		return nil, err
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

	worker, err := worker.NewKubernetesWorker(
		worker.Config{
			EphemeralRunnerSetNamespace: config.EphemeralRunnerSetNamespace,
			EphemeralRunnerSetName:      config.EphemeralRunnerSetName,
			MaxRunners:                  config.MaxRunners,
			MinRunners:                  config.MinRunners,
		},
		worker.WithLogger(app.logger),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create new kubernetes worker: %w", err)
	}
	app.worker = worker

	listener, err := listener.New(listener.Config{
		Client:     app.actionsClient,
		ScaleSetID: app.config.RunnerScaleSetId,
		Logger:     app.logger,
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
	if app.worker == nil || app.listener == nil {
		panic("app not initialized")
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

func (app *App) initLogger() error {
	logLevel := string(logging.LogLevelDebug)
	if app.config.LogLevel != "" {
		logLevel = app.config.LogLevel
	}

	logFormat := string(logging.LogFormatText)
	if app.config.LogFormat != "" {
		logFormat = app.config.LogFormat
	}

	logger, err := logging.NewLogger(logLevel, logFormat)
	if err != nil {
		return fmt.Errorf("NewLogger failed: %w", err)
	}

	app.logger = logger
	return nil
}

func (app *App) initActionsClient() error {
	var creds actions.ActionsAuth
	switch app.config.Token {
	case "":
		creds.AppCreds = &actions.GitHubAppAuth{
			AppID:             app.config.AppID,
			AppInstallationID: app.config.AppInstallationID,
			AppPrivateKey:     app.config.AppPrivateKey,
		}
	default:
		creds.Token = app.config.Token
	}

	options := []actions.ClientOption{
		actions.WithLogger(app.logger),
		actions.WithUserAgent(fmt.Sprintf("actions-runner-controller/%s", build.Version)),
	}

	if app.config.ServerRootCA != "" {
		systemPool, err := x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("failed to load system cert pool: %w", err)
		}
		pool := systemPool.Clone()
		ok := pool.AppendCertsFromPEM([]byte(app.config.ServerRootCA))
		if !ok {
			return fmt.Errorf("failed to parse root certificate")
		}

		options = append(options, actions.WithRootCAs(pool))
	}

	client, err := actions.NewClient(app.config.ConfigureUrl, &creds, options...)
	if err != nil {
		return fmt.Errorf("failed to create actions client: %w", err)
	}

	app.actionsClient = client
	return nil
}
