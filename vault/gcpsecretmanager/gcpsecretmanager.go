package gcpsecretmanager

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/googleapis/gax-go/v2"
	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
)

type secretVersionAccessor interface {
	AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
}

// GCPSecretManager is a vault backed by Google Cloud Secret Manager.
type GCPSecretManager struct {
	config Config
	client secretVersionAccessor
}

// New creates a Secret Manager vault authenticated with Application Default Credentials.
func New(ctx context.Context, cfg Config) (*GCPSecretManager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %v", err)
	}

	httpClient, err := cfg.httpClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	return newWithClientOptions(ctx, cfg, option.WithHTTPClient(httpClient))
}

func newWithClientOptions(ctx context.Context, cfg Config, opts ...option.ClientOption) (*GCPSecretManager, error) {
	// The REST transport lets the proxy and authentication be configured
	// through a plain *http.Client, the same way as the other vaults.
	client, err := secretmanager.NewRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}

	return &GCPSecretManager{config: cfg, client: client}, nil
}

// GetSecret returns the payload of the configured version of the secret with the given secret ID.
func (v *GCPSecretManager) GetSecret(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("secret name is empty")
	}

	resp, err := v.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: v.config.secretVersionName(name),
	})
	if err != nil {
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}

	payload := resp.GetPayload()
	if payload == nil {
		return "", errors.New("secret payload is nil")
	}

	if payload.DataCrc32C != nil {
		checksum := crc32.Checksum(payload.GetData(), crc32.MakeTable(crc32.Castagnoli))
		if int64(checksum) != payload.GetDataCrc32C() {
			return "", errors.New("secret payload checksum mismatch")
		}
	}

	return string(payload.GetData()), nil
}

// IsNotFound reports whether err means the secret or secret version does not exist.
// Permission and quota errors are not reported as not found, so they are retried.
func IsNotFound(err error) bool {
	var apiErr *apierror.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.HTTPCode() == http.StatusNotFound {
		return true
	}
	if s := apiErr.GRPCStatus(); s != nil && s.Code() == codes.NotFound {
		return true
	}
	return false
}
