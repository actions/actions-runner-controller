package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/v31/github"
	"golang.org/x/oauth2"
)

// Client wraps GitHub client with some additional
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

	gh := github.NewClient(&http.Client{Transport: tr})

	return &Client{
		Client:    gh,
		regTokens: map[string]*github.RegistrationToken{},
		mu:        sync.Mutex{},
	}, nil
}

// NewClientWithAccessToken returns a client authenticated with personal access token.
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
func (c *Client) GetRegistrationToken(ctx context.Context, org, repo, name string) (*github.RegistrationToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := getRegistrationKey(org, repo)
	rt, ok := c.regTokens[key]

	if ok && rt.GetExpiresAt().After(time.Now().Add(-10*time.Minute)) {
		return rt, nil
	}

	owner, repo, err := getOwnerAndRepo(org, repo)

	if err != nil {
		return rt, err
	}

	rt, res, err := c.createRegistrationToken(ctx, owner, repo)

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
func (c *Client) RemoveRunner(ctx context.Context, org, repo string, runnerID int64) error {
	owner, repo, err := getOwnerAndRepo(org, repo)

	if err != nil {
		return err
	}

	res, err := c.removeRunner(ctx, owner, repo, runnerID)

	if err != nil {
		return fmt.Errorf("failed to remove runner: %v", err)
	}

	if res.StatusCode != 204 {
		return fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return nil
}

// ListRunners returns a list of runners of specified owner/repository name.
func (c *Client) ListRunners(ctx context.Context, org, repo string) ([]*github.Runner, error) {
	owner, repo, err := getOwnerAndRepo(org, repo)

	if err != nil {
		return nil, err
	}

	var runners []*github.Runner

	opts := github.ListOptions{PerPage: 10}
	for {
		list, res, err := c.listRunners(ctx, owner, repo, &opts)

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

// wrappers for github functions (switch between organization/repository mode)
// so the calling functions don't need to switch and their code is a bit cleaner

func (c *Client) createRegistrationToken(ctx context.Context, owner, repo string) (*github.RegistrationToken, *github.Response, error) {
	if len(repo) > 0 {
		return c.Client.Actions.CreateRegistrationToken(ctx, owner, repo)
	} else {
		return CreateOrganizationRegistrationToken(ctx, c, owner)
	}
}

func (c *Client) removeRunner(ctx context.Context, owner, repo string, runnerID int64) (*github.Response, error) {
	if len(repo) > 0 {
		return c.Client.Actions.RemoveRunner(ctx, owner, repo, runnerID)
	} else {
		return RemoveOrganizationRunner(ctx, c, owner, runnerID)
	}
}

func (c *Client) listRunners(ctx context.Context, owner, repo string, opts *github.ListOptions) (*github.Runners, *github.Response, error) {
	if len(repo) > 0 {
		return c.Client.Actions.ListRunners(ctx, owner, repo, opts)
	} else {
		return ListOrganizationRunners(ctx, c, owner, opts)
	}
}

// Validates owner and repo arguments. Both are optional, but at least one should be specified
func getOwnerAndRepo(org, repo string) (string, string, error) {
	if len(repo) > 0 {
		return splitOwnerAndRepo(repo)
	}
	if len(org) > 0 {
		return org, "", nil
	}
	return "", "", fmt.Errorf("organization and repository are both empty")
}

func getRegistrationKey(org, repo string) string {
	if len(org) > 0 {
		return org
	} else {
		return repo
	}
}

func splitOwnerAndRepo(repo string) (string, string, error) {
	chunk := strings.Split(repo, "/")
	if len(chunk) != 2 {
		return "", "", fmt.Errorf("invalid repository name: '%s'", repo)
	}
	return chunk[0], chunk[1], nil
}
