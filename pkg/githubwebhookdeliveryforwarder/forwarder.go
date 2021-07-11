package githubwebhookdeliveryforwarder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	gogithub "github.com/google/go-github/v36/github"
)

type Forwarder struct {
	Repo   string
	Target string

	Hook gogithub.Hook

	PollingDelay time.Duration

	Client *github.Client
	logger
}

type persistentError struct {
	Err error
}

func (e persistentError) Error() string {
	return fmt.Sprintf("%v", e.Err)
}

func (f *Forwarder) Run(ctx context.Context) error {
	pollingDelay := 10 * time.Second
	if f.PollingDelay > 0 {
		pollingDelay = f.PollingDelay
	}

	segments := strings.Split(f.Repo, "/")

	if len(segments) != 2 {
		return fmt.Errorf("repository must be in a form of OWNER/REPO: got %q", f.Repo)
	}

	owner, repo := segments[0], segments[1]

	var hooksAPI *hooksAPI

	if repo != "" {
		hooksAPI = repoHooksAPI(f.Client.Repositories, owner, repo)
	} else {
		hooksAPI = orgHooksAPI(f.Client.Organizations, owner)
	}

	hooks, _, err := hooksAPI.ListHooks(ctx, nil)
	if err != nil {
		f.Errorf("Failed listing hooks: %v", err)

		return err
	}

	var hook *gogithub.Hook

	for i := range hooks {
		hook = hooks[i]
		break
	}

	if hook == nil {
		hookConfig := &f.Hook

		if _, ok := hookConfig.Config["url"]; !ok {
			return persistentError{Err: fmt.Errorf("config.url is missing in the hook config")}
		}

		if _, ok := hookConfig.Config["content_type"]; !ok {
			hookConfig.Config["content_type"] = "json"
		}

		if _, ok := hookConfig.Config["insecure_ssl"]; !ok {
			hookConfig.Config["insecure_ssl"] = 0
		}

		if len(hookConfig.Events) == 0 {
			hookConfig.Events = []string{"check_run", "push"}
		}

		if hookConfig.Active == nil {
			hookConfig.Active = gogithub.Bool(true)
		}

		h, _, err := hooksAPI.CreateHook(ctx, hookConfig)
		if err != nil {
			f.Errorf("Failed creating hook: %v", err)

			return persistentError{Err: err}
		}

		hook = h
	}

	f.Logf("Using this hook for receiving deliveries to be forwarded: %+v", *hook)

	var readerAPI *hookDeliveriesAPI

	if repo != "" {
		readerAPI = repoHookDeliveriesAPI(f.Client.Repositories, owner, repo, hook.GetID())
	} else {
		readerAPI = orgHookDeliveriesAPI(f.Client.Organizations, owner, hook.GetID())
	}

	cur := &cursor{}

	cur.deliveredAt = time.Now()

	for {
		var (
			err      error
			payloads [][]byte
		)

		payloads, cur, err = f.getUnprocessedDeliveries(ctx, readerAPI, *cur)
		if err != nil {
			f.Errorf("failed getting unprocessed deliveries: %v", err)

			if errors.Is(err, context.Canceled) {
				return err
			}
		}

		for _, p := range payloads {
			if _, err := http.Post(f.Target, "application/json", bytes.NewReader(p)); err != nil {
				f.Errorf("failed forwarding delivery: %v", err)
			} else {
				f.Logf("Successfully POSTed the payload to %s", f.Target)
			}
		}

		t := time.NewTimer(pollingDelay)

		select {
		case <-t.C:
			t.Stop()
		case <-ctx.Done():
			t.Stop()

			return ctx.Err()
		}
	}
}

type hookWriterAPI struct {
	CreateHook func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error)
}

func repoHookWriterAPI(svc *gogithub.RepositoriesService, org, repo string, hook *gogithub.Hook) *hookWriterAPI {
	return &hookWriterAPI{
		CreateHook: func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error) {
			return svc.CreateHook(ctx, org, repo, hook)
		},
	}
}

func orgHookWriterAPI(svc *gogithub.OrganizationsService, org string, hook *gogithub.Hook) *hookWriterAPI {
	return &hookWriterAPI{
		CreateHook: func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error) {
			return svc.CreateHook(ctx, org, hook)
		},
	}
}

type hooksAPI struct {
	ListHooks  func(ctx context.Context, opts *gogithub.ListOptions) ([]*gogithub.Hook, *gogithub.Response, error)
	CreateHook func(ctx context.Context, hook *gogithub.Hook) (*gogithub.Hook, *gogithub.Response, error)
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

type hookDeliveriesAPI struct {
	GetHookDelivery    func(ctx context.Context, id int64) (*gogithub.HookDelivery, *gogithub.Response, error)
	ListHookDeliveries func(ctx context.Context, opts *gogithub.ListCursorOptions) ([]*gogithub.HookDelivery, *gogithub.Response, error)
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

type cursor struct {
	deliveredAt time.Time
	id          int64
}

func (f *Forwarder) getUnprocessedDeliveries(ctx context.Context, api *hookDeliveriesAPI, pos cursor) ([][]byte, *cursor, error) {
	var (
		opts gogithub.ListCursorOptions
	)

	opts.PerPage = 2

	var deliveries []*gogithub.HookDelivery

OUTER:
	for {
		ds, resp, err := api.ListHookDeliveries(ctx, &opts)
		if err != nil {
			return nil, nil, err
		}

		opts.Cursor = resp.Cursor

		for _, d := range ds {
			d, _, err := api.GetHookDelivery(ctx, d.GetID())
			if err != nil {
				return nil, nil, err
			}

			payload, err := d.ParseRequestPayload()
			if err != nil {
				return nil, nil, err
			}

			id := d.GetID()
			deliveredAt := d.GetDeliveredAt()

			if !pos.deliveredAt.IsZero() && deliveredAt.Before(pos.deliveredAt) {
				f.Logf("%s is before %s so skipping all the remaining deliveries", deliveredAt, pos.deliveredAt)
				break OUTER
			}

			if pos.id != 0 && id <= pos.id {
				break OUTER
			}

			deliveries = append(deliveries, d)

			f.Logf("Received %T at %s: %v", payload, deliveredAt, payload)

			if deliveredAt.After(pos.deliveredAt) {
				pos.deliveredAt = deliveredAt.Time
			}

			if id > pos.id {
				pos.id = id
			}
		}

		if opts.Cursor == "" {
			break
		}

		time.Sleep(1 * time.Second)
	}

	sort.Slice(deliveries, func(a, b int) bool {
		return deliveries[b].GetDeliveredAt().After(deliveries[a].GetDeliveredAt().Time)
	})

	var payloads [][]byte

	for _, d := range deliveries {
		payloads = append(payloads, *d.Request.RawPayload)
	}

	return payloads, &pos, nil
}
