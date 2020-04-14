package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/v31/github"
	"golang.org/x/oauth2"
)

type Client struct {
	*github.Client
	regTokens map[string]*github.RegistrationToken
	mu        sync.Mutex
}

// NewClient returns a client authenticated as a GitHub App.
func NewClient(appID, installationID int64, privateKeyPath string) (*Client, error) {
	tr, err := ghinstallation.NewKeyFromFile(http.DefaultTransport, appID, installationID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %v", err)
	}

	return &Client{
		Client:    github.NewClient(&http.Client{Transport: tr}),
		regTokens: map[string]*github.RegistrationToken{},
		mu:        sync.Mutex{},
	}, nil
}

// NewClient returns a client authenticated with personal access token.
func NewClientWithAccessToken(token string) (*Client, error) {
	tc := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	))

	return &Client{
		Client:    github.NewClient(tc),
		regTokens: map[string]*github.RegistrationToken{},
		mu:        sync.Mutex{},
	}, nil
}

// GetRegistrationToken returns a registration token tied with the name of repository and runner.
func (c *Client) GetRegistrationToken(ctx context.Context, repository, name string) (*github.RegistrationToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	owner, repo, err := splitOwnerAndRepo(repository)
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%s/%s", repo, name)
	rt, ok := c.regTokens[key]
	if ok && rt.GetExpiresAt().After(time.Now().Add(-10*time.Minute)) {
		return rt, nil
	}

	rt, res, err := c.Client.Actions.CreateRegistrationToken(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to create registration token: %v", err)
	}

	if res.StatusCode != 201 {
		return nil, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	c.regTokens[key] = rt
	go func() {
		c.cleanup()
	}()

	return rt, nil
}

// RemoveRunner removes a runner with specified runner ID from repocitory.
func (c *Client) RemoveRunner(ctx context.Context, repository string, runnerID int64) error {
	owner, repo, err := splitOwnerAndRepo(repository)
	if err != nil {
		return err
	}

	res, err := c.Client.Actions.RemoveRunner(ctx, owner, repo, runnerID)
	if err != nil {
		return fmt.Errorf("failed to remove runner: %v", err)
	}

	if res.StatusCode != 204 {
		return fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return nil
}

// ListRunners returns a list of runners of specified repository name.
func (c *Client) ListRunners(ctx context.Context, repository string) ([]*github.Runner, error) {
	var runners []*github.Runner

	owner, repo, err := splitOwnerAndRepo(repository)
	if err != nil {
		return runners, err
	}

	opts := github.ListOptions{PerPage: 10}
	for {
		list, res, err := c.Client.Actions.ListRunners(ctx, owner, repo, &opts)
		if err != nil {
			return runners, fmt.Errorf("failed to remove runner: %v", err)
		}

		runners = append(runners, list.Runners...)
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}

	return runners, nil
}

// cleanup removes expired registration tokens.
func (c *Client) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, rt := range c.regTokens {
		if rt.GetExpiresAt().Before(time.Now()) {
			delete(c.regTokens, key)
		}
	}
}

// splitOwnerAndRepo splits specified repository name to the owner and repo name.
func splitOwnerAndRepo(repo string) (string, string, error) {
	chunk := strings.Split(repo, "/")
	if len(chunk) != 2 {
		return "", "", errors.New("invalid repository name")
	}
	return chunk[0], chunk[1], nil
}
