package actionsgithubcom

import (
	"encoding/json"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	ghalistenerconfig "github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newVaultTestListener(vaultConfig *v1alpha1.VaultConfig) *v1alpha1.AutoscalingListener {
	return &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "arc-system"},
		Spec: v1alpha1.AutoscalingListenerSpec{
			GitHubConfigURL:               "https://github.com/org",
			GitHubConfigSecret:            "gha-runners-app",
			VaultConfig:                   vaultConfig,
			RunnerScaleSetID:              1,
			AutoscalingRunnerSetNamespace: "arc-runners",
			AutoscalingRunnerSetName:      "arc-runner-set",
			EphemeralRunnerSetName:        "arc-runner-set-abcde",
			MaxRunners:                    5,
		},
	}
}

func TestNewScaleSetListenerConfig_GCPSecretManager(t *testing.T) {
	listener := newVaultTestListener(&v1alpha1.VaultConfig{
		Type: vault.VaultTypeGCPSecretManager,
		GCPSecretManager: &v1alpha1.GCPSecretManagerConfig{
			ProjectID: "my-project",
			Version:   "3",
		},
	})

	b := ResourceBuilder{ResourceCache: newTestResourceCache()}
	secret, err := b.newScaleSetListenerConfig(listener, &appconfig.AppConfig{Token: "must-not-leak"}, nil, "")
	require.NoError(t, err)

	raw := secret.Data["config.json"]
	assert.NotContains(t, string(raw), "must-not-leak", "credentials must not be copied into the listener config secret")

	var config ghalistenerconfig.Config
	require.NoError(t, json.Unmarshal(raw, &config))
	assert.Equal(t, vault.VaultTypeGCPSecretManager, config.VaultType)
	assert.Equal(t, "gha-runners-app", config.VaultLookupKey)
	assert.Nil(t, config.AzureKeyVaultConfig)
	assert.Nil(t, config.AppConfig)
	require.NotNil(t, config.GCPSecretManagerConfig)
	assert.Equal(t, "my-project", config.GCPSecretManagerConfig.ProjectID)
	assert.Equal(t, "3", config.GCPSecretManagerConfig.Version)
}

func TestNewScaleSetListenerConfig_VaultTypeWithoutProviderConfig(t *testing.T) {
	b := ResourceBuilder{ResourceCache: newTestResourceCache()}

	for _, vaultType := range []vault.VaultType{vault.VaultTypeAzureKeyVault, vault.VaultTypeGCPSecretManager} {
		t.Run(vaultType.String(), func(t *testing.T) {
			listener := newVaultTestListener(&v1alpha1.VaultConfig{Type: vaultType})
			_, err := b.newScaleSetListenerConfig(listener, nil, nil, "")
			assert.ErrorContains(t, err, "requires")
		})
	}
}
