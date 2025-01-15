package azurekeyvault

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/actions/actions-runner-controller/proxyconfig"
)

// AzureKeyVault is a struct that holds the Azure Key Vault client.
type AzureKeyVault struct {
	client *azsecrets.Client
}

func New(cfg Config) (*AzureKeyVault, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %v", err)
	}

	client, err := cfg.Client()
	if err != nil {
		return nil, fmt.Errorf("failed to create azsecrets client from config: %v", err)
	}

	return &AzureKeyVault{client: client}, nil
}

// FromEnv creates a new AzureKeyVault instance from environment variables.
// The environment variables should be prefixed with the provided prefix.
// For example, if the prefix is "AZURE_KEY_VAULT_", the environment variables should be:
// AZURE_KEY_VAULT_TENANT_ID, AZURE_KEY_VAULT_CLIENT_ID, AZURE_KEY_VAULT_URL,
// AZURE_KEY_VAULT_CERT_PATH, AZURE_KEY_VAULT_CERT_PASSWORD.
// The proxy configuration can be set using the environment variables prefixed with "PROXY_".
// For example, AZURE_KEY_VAULT_PROXY_HTTP_URL, AZURE_KEY_VAULT_PROXY_HTTP_USERNAME, etc.
func FromEnv(prefix string) (*AzureKeyVault, error) {
	cfg := Config{
		TenantID:     os.Getenv(prefix + "TENANT_ID"),
		ClientID:     os.Getenv(prefix + "CLIENT_ID"),
		URL:          os.Getenv(prefix + "URL"),
		CertPath:     os.Getenv(prefix + "CERT_PATH"),
		CertPassword: os.Getenv(prefix + "CERT_PASSWORD"),
	}

	proxyConfig, err := proxyconfig.ReadFromEnv(prefix + "PROXY_")
	if err != nil {
		return nil, fmt.Errorf("failed to read proxy config: %v", err)
	}
	cfg.Proxy = proxyConfig

	return New(cfg)
}

// GetSecret retrieves a secret from Azure Key Vault.
func (v *AzureKeyVault) GetSecret(ctx context.Context, name string) (string, error) {
	secret, err := v.client.GetSecret(ctx, name, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}
	if secret.Value == nil {
		return "", fmt.Errorf("secret value is nil")
	}

	return *secret.Value, nil
}
