package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	ConfigureUrl                string `split_words:"true"`
	AppID                       int64  `split_words:"true"`
	AppInstallationID           int64  `split_words:"true"`
	AppPrivateKey               string `split_words:"true"`
	Token                       string `split_words:"true"`
	EphemeralRunnerSetNamespace string `split_words:"true"`
	EphemeralRunnerSetName      string `split_words:"true"`
	MaxRunners                  int    `split_words:"true"`
	MinRunners                  int    `split_words:"true"`
	RunnerScaleSetId            int    `split_words:"true"`
	ServerRootCA                string `split_words:"true"`
	LogLevel                    string `split_words:"true"`
	LogFormat                   string `split_words:"true"`
	MetricsAddr                 string `split_words:"true"`
	MetricsEndpoint             string `split_words:"true"`
}

func (c *Config) validate() error {
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

func Read() (Config, error) {
	var c Config
	if err := envconfig.Process("github", &c); err != nil {
		return Config{}, fmt.Errorf("failed to read config: %w", err)
	}

	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("failed to validate config: %w", err)
	}

	return c, nil
}
