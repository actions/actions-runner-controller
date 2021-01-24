package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/v33/github"
	"golang.org/x/oauth2"
)

// Config contains configuration for Github client
type Config struct {
	EnterpriseURL     string `split_words:"true"`
	AppID             int64  `split_words:"true"`
	AppInstallationID int64  `split_words:"true"`
	AppPrivateKey     string `split_words:"true"`
	Token             string
}

// Client wraps GitHub client with some additional
type Client struct {
	*github.Client
	regTokens map[string]*github.RegistrationToken
	mu        sync.Mutex
	// GithubBaseURL to Github without API suffix.
	GithubBaseURL string
}

// NewClient creates a Github Client
func (c *Config) NewClient() (*Client, error) {
	var (
		httpClient *http.Client
		client     *github.Client
	)
	githubBaseURL := "https://github.com/"
	if len(c.Token) > 0 {
		httpClient = oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: c.Token},
		))
	} else {
		tr, err := ghinstallation.NewKeyFromFile(http.DefaultTransport, c.AppID, c.AppInstallationID, c.AppPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("authentication failed: %v", err)
		}
		if len(c.EnterpriseURL) > 0 {
			githubAPIURL, err := getEnterpriseApiUrl(c.EnterpriseURL)
			if err != nil {
				return nil, fmt.Errorf("enterprise url incorrect: %v", err)
			}
			tr.BaseURL = githubAPIURL
		}
		httpClient = &http.Client{Transport: tr}
	}

	if len(c.EnterpriseURL) > 0 {
		var err error
		client, err = github.NewEnterpriseClient(c.EnterpriseURL, c.EnterpriseURL, httpClient)
		if err != nil {
			return nil, fmt.Errorf("enterprise client creation failed: %v", err)
		}
		githubBaseURL = fmt.Sprintf("%s://%s%s", client.BaseURL.Scheme, client.BaseURL.Host, strings.TrimSuffix(client.BaseURL.Path, "api/v3/"))
	} else {
		client = github.NewClient(httpClient)
	}

	return &Client{
		Client:        client,
		regTokens:     map[string]*github.RegistrationToken{},
		mu:            sync.Mutex{},
		GithubBaseURL: githubBaseURL,
	}, nil
}

// GetRegistrationToken returns a registration token tied with the name of repository and runner.
func (c *Client) GetRegistrationToken(ctx context.Context, org, repo, name string) (*github.RegistrationToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := getRegistrationKey(org, repo)
	rt, ok := c.regTokens[key]

	if ok && rt.GetExpiresAt().After(time.Now()) {
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

// RemoveRunner removes a runner with specified runner ID from repository.
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
			return runners, fmt.Errorf("failed to list runners: %v", err)
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
	}

	return c.Client.Actions.CreateOrganizationRegistrationToken(ctx, owner)
}

func (c *Client) removeRunner(ctx context.Context, owner, repo string, runnerID int64) (*github.Response, error) {
	if len(repo) > 0 {
		return c.Client.Actions.RemoveRunner(ctx, owner, repo, runnerID)
	}

	return c.Client.Actions.RemoveOrganizationRunner(ctx, owner, runnerID)
}

func (c *Client) listRunners(ctx context.Context, owner, repo string, opts *github.ListOptions) (*github.Runners, *github.Response, error) {
	if len(repo) > 0 {
		return c.Client.Actions.ListRunners(ctx, owner, repo, opts)
	}

	return c.Client.Actions.ListOrganizationRunners(ctx, owner, opts)
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
	}
	return repo
}

func splitOwnerAndRepo(repo string) (string, string, error) {
	chunk := strings.Split(repo, "/")
	if len(chunk) != 2 {
		return "", "", fmt.Errorf("invalid repository name: '%s'", repo)
	}
	return chunk[0], chunk[1], nil
}

func getEnterpriseApiUrl(baseURL string) (string, error) {
	baseEndpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(baseEndpoint.Path, "/") {
		baseEndpoint.Path += "/"
	}
	if !strings.HasSuffix(baseEndpoint.Path, "/api/v3/") &&
		!strings.HasPrefix(baseEndpoint.Host, "api.") &&
		!strings.Contains(baseEndpoint.Host, ".api.") {
		baseEndpoint.Path += "api/v3/"
	}

	// Trim trailing slash, otherwise there's double slash added to token endpoint
	return fmt.Sprintf("%s://%s%s", baseEndpoint.Scheme, baseEndpoint.Host, strings.TrimSuffix(baseEndpoint.Path, "/")), nil
}
