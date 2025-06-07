package fake

import (
	"context"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/github/actions"
)

type MultiClientOption func(*fakeMultiClient)

func WithDefaultClient(client actions.ActionsService, err error) MultiClientOption {
	return func(f *fakeMultiClient) {
		f.defaultClient = client
		f.defaultErr = err
	}
}

type fakeMultiClient struct {
	defaultClient actions.ActionsService
	defaultErr    error
}

func NewMultiClient(opts ...MultiClientOption) actions.MultiClient {
	f := &fakeMultiClient{}

	for _, opt := range opts {
		opt(f)
	}

	if f.defaultClient == nil {
		f.defaultClient = NewFakeClient()
	}

	return f
}

func (f *fakeMultiClient) GetClientFor(ctx context.Context, githubConfigURL string, appConfig *appconfig.AppConfig, namespace string, options ...actions.ClientOption) (actions.ActionsService, error) {
	return f.defaultClient, f.defaultErr
}
