package vault

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
)

// Vault is the interface every vault implementation needs to adhere to
type Vault interface {
	GetSecret(ctx context.Context, name string) (string, error)
}

// VaultType is the type of vault supported
const (
	VaultTypeAzureKeyVault = "azure_key_vault"
)

// Compile-time checks
var _ Vault = (*azurekeyvault.AzureKeyVault)(nil)

// InitAll initializes all vaults based on the environment variables
// that start with the given prefix. It returns a map of vault types to their
// corresponding vault instances.
//
// Prefix is the namespace prefix used to filter environment variables.
// For example, the listener environment variable are prefixed with "LISTENER_", followed by the vault type, followed by the value.
//
// For example, listener has prefix "LISTENER_", has "AZURE_KEY_VAULT_" configured,
// and should read the vault URL. The environment variable will be "LISTENER_AZURE_KEY_VAULT_URL".
func InitAll(prefix string) (map[string]Vault, error) {
	envs := os.Environ()

	result := make(map[string]Vault)
	for _, env := range envs {
		if strings.HasPrefix(env, prefix+"AZURE_KEY_VAULT_") {
			akv, err := azurekeyvault.FromEnv(prefix + "AZURE_KEY_VAULT_")
			if err != nil {
				return nil, fmt.Errorf("failed to instantiate azure key vault from env: %v", err)
			}
			result[VaultTypeAzureKeyVault] = akv
		}
	}

	return result, nil
}
