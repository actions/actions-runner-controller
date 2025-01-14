package vault

import "context"

type Vault interface {
	GetSecret(ctx context.Context, name, version string) (string, error)
}
