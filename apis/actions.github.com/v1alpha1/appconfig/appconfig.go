package appconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

type AppConfig struct {
	AppID             string `json:"github_app_id"`
	AppInstallationID int64  `json:"github_app_installation_id"`
	AppPrivateKey     string `json:"github_app_private_key"`

	Token string `json:"github_token"`
}

func (c *AppConfig) tidy() *AppConfig {
	if len(c.Token) > 0 {
		return &AppConfig{
			Token: c.Token,
		}
	}

	return &AppConfig{
		AppID:             c.AppID,
		AppInstallationID: c.AppInstallationID,
		AppPrivateKey:     c.AppPrivateKey,
	}
}

func (c *AppConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("missing app config")
	}
	hasToken := len(c.Token) > 0
	hasGitHubAppAuth := c.hasGitHubAppAuth()
	if hasToken && hasGitHubAppAuth {
		return fmt.Errorf("both PAT and GitHub App credentials provided. should only provide one")
	}
	if !hasToken && !hasGitHubAppAuth {
		return fmt.Errorf("no credentials provided: either a PAT or GitHub App credentials should be provided")
	}

	return nil
}

func (c *AppConfig) hasGitHubAppAuth() bool {
	return len(c.AppID) > 0 && c.AppInstallationID > 0 && len(c.AppPrivateKey) > 0
}

func FromSecret(secret *corev1.Secret) (*AppConfig, error) {
	var appInstallationID int64
	if v := string(secret.Data["github_app_installation_id"]); v != "" {
		val, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, err
		}
		appInstallationID = val
	}

	cfg := &AppConfig{
		Token:             string(secret.Data["github_token"]),
		AppID:             string(secret.Data["github_app_id"]),
		AppInstallationID: appInstallationID,
		AppPrivateKey:     string(secret.Data["github_app_private_key"]),
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %v", err)
	}

	return cfg.tidy(), nil
}

func FromJSONString(v string) (*AppConfig, error) {
	var appConfig AppConfig
	if err := json.NewDecoder(bytes.NewBufferString(v)).Decode(&appConfig); err != nil {
		return nil, err
	}

	if err := appConfig.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate app config decoded from string: %w", err)
	}

	return appConfig.tidy(), nil
}
