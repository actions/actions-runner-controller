package config

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/actions/actions-runner-controller/vault"
	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
	"github.com/go-logr/logr"
	"golang.org/x/net/http/httpproxy"
)

type Config struct {
	ConfigureUrl   string          `json:"configure_url"`
	VaultType      vault.VaultType `json:"vault_type"`
	VaultLookupKey string          `json:"vault_lookup_key"`
	// If the VaultType is set to "azure_key_vault", this field must be populated.
	AzureKeyVaultConfig *azurekeyvault.Config `json:"azure_key_vault,omitempty"`
	// AppConfig contains the GitHub App configuration.
	// It is initially set to nil if VaultType is set.
	// Otherwise, it is populated with the GitHub App credentials from the GitHub secret.
	*appconfig.AppConfig
	EphemeralRunnerSetNamespace string                  `json:"ephemeral_runner_set_namespace"`
	EphemeralRunnerSetName      string                  `json:"ephemeral_runner_set_name"`
	MaxRunners                  int                     `json:"max_runners"`
	MinRunners                  int                     `json:"min_runners"`
	RunnerScaleSetId            int                     `json:"runner_scale_set_id"`
	RunnerScaleSetName          string                  `json:"runner_scale_set_name"`
	ServerRootCA                string                  `json:"server_root_ca"`
	LogLevel                    string                  `json:"log_level"`
	LogFormat                   string                  `json:"log_format"`
	MetricsAddr                 string                  `json:"metrics_addr"`
	MetricsEndpoint             string                  `json:"metrics_endpoint"`
	Metrics                     *v1alpha1.MetricsConfig `json:"metrics"`
}

func Read(ctx context.Context, configPath string) (*Config, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var config Config
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	var vault vault.Vault
	switch config.VaultType {
	case "":
		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("failed to validate configuration: %v", err)
		}

		return &config, nil
	case "azure_key_vault":
		akv, err := azurekeyvault.New(*config.AzureKeyVaultConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure Key Vault client: %w", err)
		}

		vault = akv
	default:
		return nil, fmt.Errorf("unsupported vault type: %s", config.VaultType)
	}

	appConfigRaw, err := vault.GetSecret(ctx, config.VaultLookupKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get app config from vault: %w", err)
	}

	appConfig, err := appconfig.FromJSONString(appConfigRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to read app config from string: %v", err)
	}

	config.AppConfig = appConfig

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return &config, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if len(c.ConfigureUrl) == 0 {
		return fmt.Errorf("GitHubConfigUrl is not provided")
	}

	if len(c.EphemeralRunnerSetNamespace) == 0 || len(c.EphemeralRunnerSetName) == 0 {
		return fmt.Errorf("EphemeralRunnerSetNamespace %q or EphemeralRunnerSetName %q is missing", c.EphemeralRunnerSetNamespace, c.EphemeralRunnerSetName)
	}

	if c.RunnerScaleSetId == 0 {
		return fmt.Errorf(`RunnerScaleSetId "%d" is missing`, c.RunnerScaleSetId)
	}

	if c.MaxRunners < c.MinRunners {
		return fmt.Errorf(`MinRunners "%d" cannot be greater than MaxRunners "%d"`, c.MinRunners, c.MaxRunners)
	}

	if c.VaultType != "" {
		if err := c.VaultType.Validate(); err != nil {
			return fmt.Errorf("VaultType validation failed: %w", err)
		}
		if c.VaultLookupKey == "" {
			return fmt.Errorf("VaultLookupKey is required when VaultType is set to %q", c.VaultType)
		}
	}

	if c.VaultType == "" && c.VaultLookupKey == "" {
		if err := c.AppConfig.Validate(); err != nil {
			return fmt.Errorf("AppConfig validation failed: %w", err)
		}
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
