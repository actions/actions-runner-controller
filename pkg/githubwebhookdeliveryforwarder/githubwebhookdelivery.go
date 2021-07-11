package githubwebhookdeliveryforwarder

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/actions-runner-controller/actions-runner-controller/github"
)

type MultiForwarder struct {
	client *github.Client

	Rules []Rule

	logger
}

type Rule struct {
	Repo   string
	Target string
}

func New(client *github.Client, rules []string) (*MultiForwarder, error) {
	var srv MultiForwarder

	for i, r := range rules {
		segments := strings.SplitN(r, " ", 2)

		if len(segments) != 2 {
			return nil, fmt.Errorf("invalid rule at %d: it must be in a form of REPO=TARGET, but was %q", i, r)
		}

		var (
			repos  []string
			target string
		)

		for _, s := range segments {
			if strings.HasPrefix(s, "from=") {
				s = strings.TrimPrefix(s, "from=")
				repos = strings.Split(s, ",")
			} else if strings.HasPrefix(s, "to=") {
				s = strings.TrimPrefix(s, "to=")
				target = s
			}
		}

		if len(repos) == 0 {
			return nil, fmt.Errorf("there must be one or more sources configured via `--repo \"from=SOURCE1,SOURCE2,... to=DEST1,DEST2,...\". got %q", r)
		}

		if target == "" {
			return nil, fmt.Errorf("there must be one destination configured via `--repo \"from=SOURCE to=DEST1,DEST2,...\". got %q", r)
		}

		for _, repo := range repos {
			srv.Rules = append(srv.Rules, Rule{Repo: repo, Target: target})
		}
	}

	srv.client = client

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
	i := &Forwarder{Repo: rule.Repo, Target: rule.Target, Client: f.client}

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
