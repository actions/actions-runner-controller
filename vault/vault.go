package vault

import (
	"context"
	"fmt"

	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
)

// Vault is the interface every vault implementation needs to adhere to
type Vault interface {
	GetSecret(ctx context.Context, name string) (string, error)
}

// VaultType represents the type of vault that can be used in the application.
// It is used to identify which vault integration should be used to resolve secrets.
type VaultType string

// VaultType is the type of vault supported
const (
	VaultTypeAzureKeyVault VaultType = "azure_key_vault"
)

func (t VaultType) String() string {
	return string(t)
}

func (t VaultType) Validate() error {
	switch t {
	case VaultTypeAzureKeyVault:
		return nil
	default:
		return fmt.Errorf("unknown vault type: %q", t)
	}
}

// Compile-time checks
var _ Vault = (*azurekeyvault.AzureKeyVault)(nil)
