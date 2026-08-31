package config

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/logger"
	"github.com/actions/actions-runner-controller/vault"
	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
	"github.com/actions/scaleset"
	"golang.org/x/net/http/httpproxy"
)

const appName = "ghalistener"

type Config struct {
	ConfigureURL   string          `json:"configure_url"`
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
	RunnerScaleSetID            int                     `json:"runner_scale_set_id"`
	RunnerScaleSetName          string                  `json:"runner_scale_set_name"`
	ServerRootCA                string                  `json:"server_root_ca"`
	LogLevel                    string                  `json:"log_level"`
	LogFormat                   string                  `json:"log_format"`
	MetricsAddr                 string                  `json:"metrics_addr"`
	MetricsEndpoint             string                  `json:"metrics_endpoint"`
	Metrics                     *v1alpha1.MetricsConfig `json:"metrics"`
	// ScaleSets, when non-empty, makes this listener multiplex one message
	// session per entry (one runner variant each) instead of the single set
	// described by the scalar RunnerScaleSet* / EphemeralRunnerSet* fields.
	// An empty list keeps the legacy single-set behaviour byte-for-byte.
	ScaleSets []ScaleSetConfig `json:"scale_sets,omitempty"`
}

// ScaleSetConfig describes one runner variant the listener must service: its
// registered scale set id and the EphemeralRunnerSet the scaler patches for it.
type ScaleSetConfig struct {
	RunnerScaleSetID       int    `json:"runner_scale_set_id"`
	RunnerScaleSetName     string `json:"runner_scale_set_name"`
	EphemeralRunnerSetName string `json:"ephemeral_runner_set_name"`
	MaxRunners             int    `json:"max_runners"`
	MinRunners             int    `json:"min_runners"`
}

// EffectiveScaleSets returns the scale sets this listener must service. When
// ScaleSets is empty it returns a single synthetic entry built from the scalar
// fields, so the multi-session driver can treat both cases the same way.
func (c *Config) EffectiveScaleSets() []ScaleSetConfig {
	if len(c.ScaleSets) > 0 {
		return c.ScaleSets
	}
	return []ScaleSetConfig{
		{
			RunnerScaleSetID:       c.RunnerScaleSetID,
			RunnerScaleSetName:     c.RunnerScaleSetName,
			EphemeralRunnerSetName: c.EphemeralRunnerSetName,
			MaxRunners:             c.MaxRunners,
			MinRunners:             c.MinRunners,
		},
	}
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
	if len(c.ConfigureURL) == 0 {
		return fmt.Errorf("GitHubConfigUrl is not provided")
	}

	if len(c.EphemeralRunnerSetNamespace) == 0 {
		return fmt.Errorf("EphemeralRunnerSetNamespace %q is missing", c.EphemeralRunnerSetNamespace)
	}

	if len(c.ScaleSets) > 0 {
		if err := c.validateScaleSets(); err != nil {
			return err
		}
	} else {
		if len(c.EphemeralRunnerSetName) == 0 {
			return fmt.Errorf("EphemeralRunnerSetName %q is missing", c.EphemeralRunnerSetName)
		}
		if c.RunnerScaleSetID == 0 {
			return fmt.Errorf(`RunnerScaleSetId "%d" is missing`, c.RunnerScaleSetID)
		}
		if c.MaxRunners < c.MinRunners {
			return fmt.Errorf(`MinRunners "%d" cannot be greater than MaxRunners "%d"`, c.MinRunners, c.MaxRunners)
		}
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

// validateScaleSets checks the multi-session ScaleSets list. Each entry needs a
// scale set id and its own EphemeralRunnerSet name; names must be unique so the
// scaler patches distinct objects.
func (c *Config) validateScaleSets() error {
	seen := make(map[string]struct{}, len(c.ScaleSets))
	for i, s := range c.ScaleSets {
		if s.RunnerScaleSetID == 0 {
			return fmt.Errorf(`ScaleSets[%d].RunnerScaleSetId "%d" is missing`, i, s.RunnerScaleSetID)
		}
		if len(s.EphemeralRunnerSetName) == 0 {
			return fmt.Errorf("ScaleSets[%d].EphemeralRunnerSetName is missing", i)
		}
		if s.MaxRunners < s.MinRunners {
			return fmt.Errorf(`ScaleSets[%d].MinRunners "%d" cannot be greater than MaxRunners "%d"`, i, s.MinRunners, s.MaxRunners)
		}
		if _, dup := seen[s.EphemeralRunnerSetName]; dup {
			return fmt.Errorf("ScaleSets[%d].EphemeralRunnerSetName %q is duplicated", i, s.EphemeralRunnerSetName)
		}
		seen[s.EphemeralRunnerSetName] = struct{}{}
	}
	return nil
}

func (c *Config) Logger() (*slog.Logger, error) {
	return logger.New(c.LogLevel, c.LogFormat)
}

func (c *Config) ActionsClient(logger *slog.Logger, clientOptions ...scaleset.HTTPOption) (*scaleset.Client, error) {
	systemInfo := scaleset.SystemInfo{
		System:     "actions-runner-controller",
		Version:    build.Version,
		CommitSHA:  build.CommitSHA,
		ScaleSetID: c.RunnerScaleSetID,
		Subsystem:  appName,
	}

	options := append([]scaleset.HTTPOption{
		scaleset.WithLogger(logger),
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

		options = append(options, scaleset.WithRootCAs(pool))
	}

	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	options = append(options, scaleset.WithProxy(func(req *http.Request) (*url.URL, error) {
		return proxyFunc(req.URL)
	}))

	var client *scaleset.Client
	switch c.Token {
	case "":
		c, err := scaleset.NewClientWithGitHubApp(
			scaleset.ClientWithGitHubAppConfig{
				GitHubConfigURL: c.ConfigureURL,
				GitHubAppAuth: scaleset.GitHubAppAuth{
					ClientID:       c.AppConfig.AppID,
					InstallationID: c.AppConfig.AppInstallationID,
					PrivateKey:     c.AppConfig.AppPrivateKey,
				},
				SystemInfo: systemInfo,
			},
			options...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate client with GitHub App auth: %w", err)
		}
		client = c
	default:
		c, err := scaleset.NewClientWithPersonalAccessToken(
			scaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL:     c.ConfigureURL,
				PersonalAccessToken: c.Token,
				SystemInfo:          systemInfo,
			},
			options...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate client with PAT auth: %w", err)
		}
		client = c
	}

	return client, nil
}
