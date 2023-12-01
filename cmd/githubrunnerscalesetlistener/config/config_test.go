package config

import (
	"fmt"
	"testing"

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
		Token:                       "token",
	}
	err := config.validate()
	assert.ErrorContains(t, err, "MinRunners '5' cannot be greater than MaxRunners '2", "Expected error about MinRunners > MaxRunners")
}

func TestConfigValidationMissingToken(t *testing.T) {
	config := &Config{
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := config.validate()
	expectedError := fmt.Sprintf("GitHub auth credential is missing, token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidationAppKey(t *testing.T) {
	config := &Config{
		AppID:                       1,
		AppInstallationID:           10,
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := config.validate()
	expectedError := fmt.Sprintf("GitHub auth credential is missing, token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidationOnlyOneTypeOfCredentials(t *testing.T) {
	config := &Config{
		AppID:                       1,
		AppInstallationID:           10,
		AppPrivateKey:               "asdf",
		Token:                       "asdf",
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := config.validate()
	expectedError := fmt.Sprintf("only one GitHub auth method supported at a time. Have both PAT and App auth: token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
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
		Token:                       "asdf",
	}

	err := config.validate()

	assert.NoError(t, err, "Expected no error")
}

func TestConfigValidationConfigUrl(t *testing.T) {
	config := &Config{
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}

	err := config.validate()

	assert.ErrorContains(t, err, "GitHubConfigUrl is not provided", "Expected error about missing ConfigureUrl")
}
