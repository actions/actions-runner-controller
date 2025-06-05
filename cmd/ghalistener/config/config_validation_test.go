package config

import (
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/vault"
	"github.com/stretchr/testify/assert"
)

func TestConfigValidationMinMax(t *testing.T) {
	config := &Config{
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
		MinRunners:                  5,
		MaxRunners:                  2,
		AppConfig: &appconfig.AppConfig{
			Token: "token",
		},
	}
	err := config.Validate()
	assert.ErrorContains(t, err, `MinRunners "5" cannot be greater than MaxRunners "2"`, "Expected error about MinRunners > MaxRunners")
}

func TestConfigValidationMissingToken(t *testing.T) {
	config := &Config{
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := config.Validate()
	expectedError := "AppConfig validation failed: missing app config"
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidationAppKey(t *testing.T) {
	t.Parallel()

	t.Run("app id integer", func(t *testing.T) {
		t.Parallel()
		config := &Config{
			AppConfig: &appconfig.AppConfig{
				AppID:             "1",
				AppInstallationID: 10,
			},
			ConfigureUrl:                "github.com/some_org/some_repo",
			EphemeralRunnerSetNamespace: "namespace",
			EphemeralRunnerSetName:      "deployment",
			RunnerScaleSetId:            1,
		}
		err := config.Validate()
		expectedError := fmt.Sprintf(`GitHub auth credential is missing, token length: "%d", appId: %q, installationId: "%d", private key length: "%d"`, len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
		assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
	})

	t.Run("app id as client id", func(t *testing.T) {
		t.Parallel()
		config := &Config{
			AppConfig: &appconfig.AppConfig{
				AppID:             "Iv23f8doAlphaNumer1c",
				AppInstallationID: 10,
			},
			ConfigureUrl:                "github.com/some_org/some_repo",
			EphemeralRunnerSetNamespace: "namespace",
			EphemeralRunnerSetName:      "deployment",
			RunnerScaleSetId:            1,
		}
		err := config.Validate()
		expectedError := fmt.Sprintf(`GitHub auth credential is missing, token length: "%d", appId: %q, installationId: "%d", private key length: "%d"`, len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
		assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
	})
}

func TestConfigValidationOnlyOneTypeOfCredentials(t *testing.T) {
	config := &Config{
		AppConfig: &appconfig.AppConfig{
			AppID:             "1",
			AppInstallationID: 10,
			AppPrivateKey:     "asdf",
			Token:             "asdf",
		},
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := config.Validate()
	expectedError := fmt.Sprintf(`only one GitHub auth method supported at a time. Have both PAT and App auth: token length: "%d", appId: %q, installationId: "%d", private key length: "%d"`, len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidation(t *testing.T) {
	config := &Config{
		ConfigureUrl:                "https://github.com/actions",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
		MinRunners:                  1,
		MaxRunners:                  5,
		AppConfig: &appconfig.AppConfig{
			Token: "asdf",
		},
	}

	err := config.Validate()

	assert.NoError(t, err, "Expected no error")
}

func TestConfigValidationConfigUrl(t *testing.T) {
	config := &Config{
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}

	err := config.Validate()

	assert.ErrorContains(t, err, "GitHubConfigUrl is not provided", "Expected error about missing ConfigureUrl")
}

func TestConfigValidationWithVaultConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		config := &Config{
			ConfigureUrl:                "https://github.com/actions",
			EphemeralRunnerSetNamespace: "namespace",
			EphemeralRunnerSetName:      "deployment",
			RunnerScaleSetId:            1,
			MinRunners:                  1,
			MaxRunners:                  5,
			VaultType:                   vault.VaultTypeAzureKeyVault,
			VaultLookupKey:              "testkey",
		}
		err := config.Validate()
		assert.NoError(t, err, "Expected no error for valid vault type")
	})

	t.Run("invalid vault type", func(t *testing.T) {
		config := &Config{
			ConfigureUrl:                "https://github.com/actions",
			EphemeralRunnerSetNamespace: "namespace",
			EphemeralRunnerSetName:      "deployment",
			RunnerScaleSetId:            1,
			MinRunners:                  1,
			MaxRunners:                  5,
			VaultType:                   vault.VaultType("invalid_vault_type"),
			VaultLookupKey:              "testkey",
		}
		err := config.Validate()
		assert.ErrorContains(t, err, `unknown vault type: "invalid_vault_type"`, "Expected error for invalid vault type")
	})

	t.Run("vault type set without lookup key", func(t *testing.T) {
		config := &Config{
			ConfigureUrl:                "https://github.com/actions",
			EphemeralRunnerSetNamespace: "namespace",
			EphemeralRunnerSetName:      "deployment",
			RunnerScaleSetId:            1,
			MinRunners:                  1,
			MaxRunners:                  5,
			VaultType:                   vault.VaultTypeAzureKeyVault,
			VaultLookupKey:              "",
		}
		err := config.Validate()
		assert.ErrorContains(t, err, `vault type set to "invalid_vault_type", but lookup key is empty`, "Expected error for vault type without lookup key")
	})
}
