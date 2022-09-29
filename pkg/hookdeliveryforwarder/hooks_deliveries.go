package hookdeliveryforwarder

import (
	"context"

	gogithub "github.com/google/go-github/v47/github"
)

type hookDeliveriesAPI struct {
	GetHookDelivery    func(ctx context.Context, id int64) (*gogithub.HookDelivery, *gogithub.Response, error)
	ListHookDeliveries func(ctx context.Context, opts *gogithub.ListCursorOptions) ([]*gogithub.HookDelivery, *gogithub.Response, error)
}

func newHookDeliveriesAPI(client *gogithub.Client, org, repo string, hookID int64) *hookDeliveriesAPI {
	var hookDeliveries *hookDeliveriesAPI

	if repo != "" {
		hookDeliveries = repoHookDeliveriesAPI(client.Repositories, org, repo, hookID)
	} else {
		hookDeliveries = orgHookDeliveriesAPI(client.Organizations, org, hookID)
	}

	return hookDeliveries
}

func repoHookDeliveriesAPI(svc *gogithub.RepositoriesService, org, repo string, hookID int64) *hookDeliveriesAPI {
	return &hookDeliveriesAPI{
		GetHookDelivery: func(ctx context.Context, id int64) (*gogithub.HookDelivery, *gogithub.Response, error) {
			return svc.GetHookDelivery(ctx, org, repo, hookID, id)
		},
		ListHookDeliveries: func(ctx context.Context, opts *gogithub.ListCursorOptions) ([]*gogithub.HookDelivery, *gogithub.Response, error) {
			return svc.ListHookDeliveries(ctx, org, repo, hookID, opts)
		},
	}
}

func orgHookDeliveriesAPI(svc *gogithub.OrganizationsService, org string, hookID int64) *hookDeliveriesAPI {
	return &hookDeliveriesAPI{
		GetHookDelivery: func(ctx context.Context, id int64) (*gogithub.HookDelivery, *gogithub.Response, error) {
			return svc.GetHookDelivery(ctx, org, hookID, id)
		},
		ListHookDeliveries: func(ctx context.Context, opts *gogithub.ListCursorOptions) ([]*gogithub.HookDelivery, *gogithub.Response, error) {
			return svc.ListHookDeliveries(ctx, org, hookID, opts)
		},
	}
}
