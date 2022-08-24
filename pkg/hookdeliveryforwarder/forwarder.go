package hookdeliveryforwarder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	gogithub "github.com/google/go-github/v47/github"
)

type Forwarder struct {
	Repo   string
	Target string

	Hook gogithub.Hook

	PollingDelay time.Duration

	Client *github.Client

	Checkpointer Checkpointer

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

	owner := segments[0]

	var repo string

	if len(segments) > 1 {
		repo = segments[1]
	}

	hooksAPI := newHooksAPI(f.Client.Client, owner, repo)

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

		if _, ok := hookConfig.Config["secret"]; !ok {
			hookConfig.Config["secret"] = os.Getenv("GITHUB_HOOK_SECRET")
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

	hookDeliveries := newHookDeliveriesAPI(f.Client.Client, owner, repo, hook.GetID())

	cur, err := f.Checkpointer.GetOrCreate(hook.GetID())
	if err != nil {
		f.Errorf("Failed to get or create log position: %v", err)

		return persistentError{Err: err}
	}

LOOP:
	for {
		var (
			err      error
			payloads [][]byte
		)

		payloads, cur, err = f.getUnprocessedDeliveries(ctx, hookDeliveries, *cur)
		if err != nil {
			f.Errorf("failed getting unprocessed deliveries: %v", err)

			if errors.Is(err, context.Canceled) {
				return err
			}
		}

		for _, p := range payloads {
			if _, err := http.Post(f.Target, "application/json", bytes.NewReader(p)); err != nil {
				f.Errorf("failed forwarding delivery: %v", err)

				retryDelay := 5 * time.Second
				t := time.NewTimer(retryDelay)

				select {
				case <-t.C:
					t.Stop()
				case <-ctx.Done():
					t.Stop()

					return ctx.Err()
				}

				continue LOOP
			} else {
				f.Logf("Successfully POSTed the payload to %s", f.Target)
			}
		}

		if err := f.Checkpointer.Update(hook.GetID(), cur); err != nil {
			return fmt.Errorf("failed updating checkpoint: %w", err)
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

type State struct {
	DeliveredAt time.Time
	ID          int64
}

func (f *Forwarder) getUnprocessedDeliveries(ctx context.Context, hookDeliveries *hookDeliveriesAPI, pos State) ([][]byte, *State, error) {
	var (
		opts gogithub.ListCursorOptions
	)

	opts.PerPage = 2

	var deliveries []*gogithub.HookDelivery

OUTER:
	for {
		ds, resp, err := hookDeliveries.ListHookDeliveries(ctx, &opts)
		if err != nil {
			return nil, nil, err
		}

		opts.Cursor = resp.Cursor

		for _, d := range ds {
			d, _, err := hookDeliveries.GetHookDelivery(ctx, d.GetID())
			if err != nil {
				return nil, nil, err
			}

			payload, err := d.ParseRequestPayload()
			if err != nil {
				return nil, nil, err
			}

			id := d.GetID()
			deliveredAt := d.GetDeliveredAt()

			if !pos.DeliveredAt.IsZero() && deliveredAt.Before(pos.DeliveredAt) {
				f.Logf("%s is before %s so skipping all the remaining deliveries", deliveredAt, pos.DeliveredAt)
				break OUTER
			}

			if pos.ID != 0 && id <= pos.ID {
				break OUTER
			}

			deliveries = append(deliveries, d)

			f.Logf("Received %T at %s: %v", payload, deliveredAt, payload)

			if deliveredAt.After(pos.DeliveredAt) {
				pos.DeliveredAt = deliveredAt.Time
			}

			if id > pos.ID {
				pos.ID = id
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
