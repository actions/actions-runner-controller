package app

import (
	"crypto/x509"
	"fmt"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/go-logr/logr"
)

type App struct {
	actionsClient *actions.Client

	config config.Config
	logger logr.Logger
}

func New(config config.Config) (*App, error) {
	app := &App{
		config: config,
	}

	if err := app.initLogger(); err != nil {
		return nil, err
	}

	if err := app.initActionsClient(); err != nil {
		return nil, err
	}

	return app, nil
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
