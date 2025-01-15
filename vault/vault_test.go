package vault_test

import (
	"os"
	"testing"

	"github.com/actions/actions-runner-controller/vault"
	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
	"github.com/stretchr/testify/require"
)

func TestInitAll_AzureKeyVault(t *testing.T) {
	os.Clearenv()
	os.Setenv("LISTENER_AZURE_KEY_VAULT_TENANT_ID", "tenantID")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_CLIENT_ID", "clientID")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_URL", "https://example.com")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_CERT_PATH", "/path/to/cert")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_CERT_PASSWORD", "password")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_HTTP_URL", "http://proxy.example.com")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_HTTP_USERNAME", "username")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_HTTP_PASSWORD", "password")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_HTTPS_URL", "https://proxy.example.com")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_HTTPS_USERNAME", "username")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_HTTPS_PASSWORD", "password")
	os.Setenv("LISTENER_AZURE_KEY_VAULT_PROXY_NO_PROXY", "temp.com")

	vaults, err := vault.InitAll("LISTENER_")
	require.NoError(t, err)
	require.Len(t, vaults, 1)
	require.Contains(t, vaults, vault.VaultTypeAzureKeyVault)
	akv, ok := vaults[vault.VaultTypeAzureKeyVault].(*azurekeyvault.AzureKeyVault)
	require.True(t, ok)
	require.NotNil(t, akv)
}
