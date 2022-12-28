package githubwebhookdeliveryforwarder

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/github"
	gogithub "github.com/google/go-github/v47/github"
)

type server struct {
	target string
	Repo   string
	client *github.Client
}

func New(client *github.Client, target string) *server {
	var srv server

	srv.target = target
	srv.client = client

	return &srv
}

func (s *server) Run(ctx context.Context) error {
	segments := strings.Split(s.Repo, "/")

	if len(segments) != 2 {
		return fmt.Errorf("repository must be in a form of OWNER/REPO: got %q", s.Repo)
	}

	owner, repo := segments[0], segments[1]

	hooks, _, err := s.client.Repositories.ListHooks(ctx, owner, repo, nil)
	if err != nil {
		s.Errorf("Failed listing hooks: %v", err)

		return err
	}

	var hook *gogithub.Hook

	for i := range hooks {
		hook = hooks[i]
		break
	}

	cur := &cursor{}

	cur.deliveredAt = time.Now()

	for {
		var (
			err      error
			payloads [][]byte
		)

		payloads, cur, err = s.getUnprocessedDeliveries(ctx, owner, repo, hook.GetID(), *cur)
		if err != nil {
			s.Errorf("failed getting unprocessed deliveries: %v", err)
		}

		for _, p := range payloads {
			if _, err := http.Post(s.target, "application/json", bytes.NewReader(p)); err != nil {
				s.Errorf("failed forwarding delivery: %v", err)
			}
		}

		time.Sleep(10 * time.Second)
	}
}

type cursor struct {
	deliveredAt time.Time
	id          int64
}

func (s *server) getUnprocessedDeliveries(ctx context.Context, owner, repo string, hookID int64, pos cursor) ([][]byte, *cursor, error) {
	var (
		opts gogithub.ListCursorOptions
	)

	opts.PerPage = 2

	var deliveries []*gogithub.HookDelivery

OUTER:
	for {
		ds, resp, err := s.client.Repositories.ListHookDeliveries(ctx, owner, repo, hookID, &opts)
		if err != nil {
			return nil, nil, err
		}

		opts.Cursor = resp.Cursor

		for _, d := range ds {
			d, _, err := s.client.Repositories.GetHookDelivery(ctx, owner, repo, hookID, d.GetID())
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
				s.Logf("%s is before %s so skipping all the remaining deliveries", deliveredAt, pos.deliveredAt)
				break OUTER
			}

			if pos.id != 0 && id <= pos.id {
				break OUTER
			}

			s.Logf("Received %T at %s: %v", payload, deliveredAt, payload)

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

func (s *server) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	var (
		ok bool

		err error
	)

	defer func() {
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)

			if err != nil {
				msg := err.Error()
				if _, err := w.Write([]byte(msg)); err != nil {
					s.Errorf("failed writing http error response: %v", err)
				}
			}
		}
	}()

	defer func() {
		if r.Body != nil {
			r.Body.Close()
		}
	}()

	// respond ok to GET / e.g. for health check
	if r.Method == http.MethodGet {
		fmt.Fprintln(w, "webhook server is running")
		return
	}

	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok")); err != nil {
		s.Errorf("failed writing http response: %v", err)
	}
}

func (s *server) Logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func (s *server) Errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
