package hookdeliveryforwarder

import (
	"context"

	gogithub "github.com/google/go-github/v47/github"
)

type hooksAPI struct {
	ListHooks  func(ctx context.Context, opts *gogithub.ListOptions) ([]*gogithub.Hook, *gogithub.Response, error)
	CreateHook func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error)
}

func newHooksAPI(client *gogithub.Client, org, repo string) *hooksAPI {
	var hooksAPI *hooksAPI

	if repo != "" {
		hooksAPI = repoHooksAPI(client.Repositories, org, repo)
	} else {
		hooksAPI = orgHooksAPI(client.Organizations, org)
	}

	return hooksAPI
}

func repoHooksAPI(svc *gogithub.RepositoriesService, org, repo string) *hooksAPI {
	return &hooksAPI{
		ListHooks: func(ctx context.Context, opts *gogithub.ListOptions) ([]*gogithub.Hook, *gogithub.Response, error) {
			return svc.ListHooks(ctx, org, repo, opts)
		},
		CreateHook: func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error) {
			return svc.CreateHook(ctx, org, repo, hook)
		},
	}
}

func orgHooksAPI(svc *gogithub.OrganizationsService, org string) *hooksAPI {
	return &hooksAPI{
		ListHooks: func(ctx context.Context, opts *gogithub.ListOptions) ([]*gogithub.Hook, *gogithub.Response, error) {
			return svc.ListHooks(ctx, org, opts)
		},
		CreateHook: func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error) {
			return svc.CreateHook(ctx, org, hook)
		},
	}
}
