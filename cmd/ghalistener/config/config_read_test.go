package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestReadGCPSecretManager(t *testing.T) {
	t.Run("requires provider config", func(t *testing.T) {
		path := writeConfig(t, `{"vault_type":"gcp_secret_manager","vault_lookup_key":"gha-runners-app"}`)
		_, err := Read(t.Context(), path)
		assert.ErrorContains(t, err, "gcp_secret_manager configuration is required")
	})

	t.Run("validates provider config", func(t *testing.T) {
		path := writeConfig(t, `{"vault_type":"gcp_secret_manager","vault_lookup_key":"gha-runners-app","gcp_secret_manager":{}}`)
		_, err := Read(t.Context(), path)
		assert.ErrorContains(t, err, "project_id is not set")
	})

	t.Run("dispatches to secret manager", func(t *testing.T) {
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
		path := writeConfig(t, `{"vault_type":"gcp_secret_manager","vault_lookup_key":"gha-runners-app","gcp_secret_manager":{"project_id":"my-project"}}`)
		_, err := Read(t.Context(), path)
		assert.ErrorContains(t, err, "failed to create GCP Secret Manager client")
		assert.ErrorContains(t, err, "application default credentials")
	})
}

func TestReadAzureKeyVaultRequiresProviderConfig(t *testing.T) {
	path := writeConfig(t, `{"vault_type":"azure_key_vault","vault_lookup_key":"gha-runners-app"}`)
	_, err := Read(t.Context(), path)
	assert.ErrorContains(t, err, "azure_key_vault configuration is required")
}
