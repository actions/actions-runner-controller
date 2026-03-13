package fake

import (
	"context"

	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
)

// MultiClientOption is a functional option for configuring a fake MultiClient
type MultiClientOption func(*MultiClient)

// WithClient configures the client that GetClientFor will return
func WithClient(c multiclient.Client) MultiClientOption {
	return func(mc *MultiClient) {
		mc.client = c
	}
}

// WithGetClientForError configures an error that GetClientFor will return
func WithGetClientForError(err error) MultiClientOption {
	return func(mc *MultiClient) {
		mc.getClientForErr = err
	}
}

// MultiClient implements multiclient.MultiClient interface for testing
type MultiClient struct {
	client          multiclient.Client
	getClientForErr error
}

// Compile-time interface check
var _ multiclient.MultiClient = (*MultiClient)(nil)

// NewMultiClient creates a new fake MultiClient with the given options
func NewMultiClient(opts ...MultiClientOption) *MultiClient {
	mc := &MultiClient{}
	for _, opt := range opts {
		opt(mc)
	}
	// Default behavior: if no client configured, return a default NewClient()
	if mc.client == nil {
		mc.client = NewClient()
	}
	return mc
}

func (mc *MultiClient) GetClientFor(ctx context.Context, opts *multiclient.ClientForOptions) (multiclient.Client, error) {
	if mc.getClientForErr != nil {
		return nil, mc.getClientForErr
	}
	return mc.client, nil
}
