package hookdeliveryforwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/actions/actions-runner-controller/github"
	gogithub "github.com/google/go-github/v47/github"
)

type MultiForwarder struct {
	client *github.Client

	Rules []Rule

	Checkpointer Checkpointer

	logger
}

type RuleConfig struct {
	Repo   []string      `json:"from"`
	Target string        `json:"to"`
	Hook   gogithub.Hook `json:"hook"`
}

type Rule struct {
	Repo   string
	Target string
	Hook   gogithub.Hook
}

func New(client *github.Client, rules []string) (*MultiForwarder, error) {
	var srv MultiForwarder

	for _, r := range rules {
		var rule RuleConfig

		if err := json.Unmarshal([]byte(r), &rule); err != nil {
			return nil, fmt.Errorf("failed unmarshalling %s: %w", r, err)
		}

		if len(rule.Repo) == 0 {
			return nil, fmt.Errorf("there must be one or more sources configured via `--repo \"from=SOURCE1,SOURCE2,... to=DEST1,DEST2,...\". got %q", r)
		}

		if rule.Target == "" {
			return nil, fmt.Errorf("there must be one destination configured via `--repo \"from=SOURCE to=DEST1,DEST2,...\". got %q", r)
		}

		for _, repo := range rule.Repo {
			srv.Rules = append(srv.Rules, Rule{
				Repo:   repo,
				Target: rule.Target,
				Hook:   rule.Hook,
			})
		}
	}

	srv.client = client
	srv.Checkpointer = NewInMemoryLogPositionProvider()

	return &srv, nil
}

func (f *MultiForwarder) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	errs := make(chan error, len(f.Rules))

	for _, r := range f.Rules {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()

			errs <- f.run(ctx, r)
		}()
	}

	wg.Wait()

	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

func (f *MultiForwarder) run(ctx context.Context, rule Rule) error {
	i := &Forwarder{
		Repo:         rule.Repo,
		Target:       rule.Target,
		Hook:         rule.Hook,
		Client:       f.client,
		Checkpointer: f.Checkpointer,
	}

	return i.Run(ctx)
}

func (f *MultiForwarder) HandleReadyz(w http.ResponseWriter, r *http.Request) {
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
					f.Errorf("failed writing http error response: %v", err)
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
		f.Errorf("failed writing http response: %v", err)
	}
}
