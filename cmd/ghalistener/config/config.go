package config

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/go-logr/logr"
	"golang.org/x/net/http/httpproxy"
)

type Config struct {
	ConfigureUrl                string `json:"configure_url"`
	AppID                       int64  `json:"app_id"`
	AppInstallationID           int64  `json:"app_installation_id"`
	AppPrivateKey               string `json:"app_private_key"`
	Token                       string `json:"token"`
	EphemeralRunnerSetNamespace string `json:"ephemeral_runner_set_namespace"`
	EphemeralRunnerSetName      string `json:"ephemeral_runner_set_name"`
	MaxRunners                  int    `json:"max_runners"`
	MinRunners                  int    `json:"min_runners"`
	RunnerScaleSetId            int    `json:"runner_scale_set_id"`
	RunnerScaleSetName          string `json:"runner_scale_set_name"`
	ServerRootCA                string `json:"server_root_ca"`
	LogLevel                    string `json:"log_level"`
	LogFormat                   string `json:"log_format"`
	MetricsAddr                 string `json:"metrics_addr"`
	MetricsEndpoint             string `json:"metrics_endpoint"`
}

func Read(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()

	var config Config
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return Config{}, fmt.Errorf("failed to decode config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("failed to validate config: %w", err)
	}

	return config, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if len(c.ConfigureUrl) == 0 {
		return fmt.Errorf("GitHubConfigUrl is not provided")
	}

	if len(c.EphemeralRunnerSetNamespace) == 0 || len(c.EphemeralRunnerSetName) == 0 {
		return fmt.Errorf("EphemeralRunnerSetNamespace '%s' or EphemeralRunnerSetName '%s' is missing", c.EphemeralRunnerSetNamespace, c.EphemeralRunnerSetName)
	}

	if c.RunnerScaleSetId == 0 {
		return fmt.Errorf("RunnerScaleSetId '%d' is missing", c.RunnerScaleSetId)
	}

	if c.MaxRunners < c.MinRunners {
		return fmt.Errorf("MinRunners '%d' cannot be greater than MaxRunners '%d'", c.MinRunners, c.MaxRunners)
	}

	hasToken := len(c.Token) > 0
	hasPrivateKeyConfig := c.AppID > 0 && c.AppPrivateKey != ""

	if !hasToken && !hasPrivateKeyConfig {
		return fmt.Errorf("GitHub auth credential is missing, token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(c.Token), c.AppID, c.AppInstallationID, len(c.AppPrivateKey))
	}

	if hasToken && hasPrivateKeyConfig {
		return fmt.Errorf("only one GitHub auth method supported at a time. Have both PAT and App auth: token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(c.Token), c.AppID, c.AppInstallationID, len(c.AppPrivateKey))
	}

	return nil
}

func (c *Config) Logger() (logr.Logger, error) {
	logLevel := string(logging.LogLevelDebug)
	if c.LogLevel != "" {
		logLevel = c.LogLevel
	}

	logFormat := string(logging.LogFormatText)
	if c.LogFormat != "" {
		logFormat = c.LogFormat
	}

	logger, err := logging.NewLogger(logLevel, logFormat)
	if err != nil {
		return logr.Logger{}, fmt.Errorf("NewLogger failed: %w", err)
	}

	return logger, nil
}

func (c *Config) ActionsClient(logger logr.Logger, clientOptions ...actions.ClientOption) (*actions.Client, error) {
	var creds actions.ActionsAuth
	switch c.Token {
	case "":
		creds.AppCreds = &actions.GitHubAppAuth{
			AppID:             c.AppID,
			AppInstallationID: c.AppInstallationID,
			AppPrivateKey:     c.AppPrivateKey,
		}
	default:
		creds.Token = c.Token
	}

	options := append([]actions.ClientOption{
		actions.WithLogger(logger),
	}, clientOptions...)

	if c.ServerRootCA != "" {
		systemPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("failed to load system cert pool: %w", err)
		}
		pool := systemPool.Clone()
		ok := pool.AppendCertsFromPEM([]byte(c.ServerRootCA))
		if !ok {
			return nil, fmt.Errorf("failed to parse root certificate")
		}

		options = append(options, actions.WithRootCAs(pool))
	}

	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	options = append(options, actions.WithProxy(func(req *http.Request) (*url.URL, error) {
		return proxyFunc(req.URL)
	}))

	client, err := actions.NewClient(c.ConfigureUrl, &creds, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create actions client: %w", err)
	}

	client.SetUserAgent(actions.UserAgentInfo{
		Version:    build.Version,
		CommitSHA:  build.CommitSHA,
		ScaleSetID: c.RunnerScaleSetId,
		HasProxy:   hasProxy(),
		Subsystem:  "ghalistener",
	})

	return client, nil
}

func hasProxy() bool {
	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	return proxyFunc != nil
}
