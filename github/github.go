package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/metrics"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v52/github"
	"github.com/gregjones/httpcache"
	"golang.org/x/oauth2"
)

// Config contains configuration for Github client
type Config struct {
	EnterpriseURL     string `split_words:"true"`
	AppID             int64  `split_words:"true"`
	AppInstallationID int64  `split_words:"true"`
	AppPrivateKey     string `split_words:"true"`
	Token             string
	URL               string `split_words:"true"`
	UploadURL         string `split_words:"true"`
	BasicauthUsername string `split_words:"true"`
	BasicauthPassword string `split_words:"true"`
	RunnerGitHubURL   string `split_words:"true"`

	Log *logr.Logger
}

// Client wraps GitHub client with some additional
type Client struct {
	*github.Client
	regTokens map[string]*github.RegistrationToken
	mu        sync.Mutex
	// GithubBaseURL to Github without API suffix.
	GithubBaseURL string
	IsEnterprise  bool
}

type BasicAuthTransport struct {
	Username string
	Password string
}

func (p BasicAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(p.Username, p.Password)
	return http.DefaultTransport.RoundTrip(req)
}

// NewClient creates a Github Client
func (c *Config) NewClient() (*Client, error) {
	var transport http.RoundTripper
	if len(c.BasicauthUsername) > 0 && len(c.BasicauthPassword) > 0 {
		transport = BasicAuthTransport{Username: c.BasicauthUsername, Password: c.BasicauthPassword}
	} else if len(c.Token) > 0 {
		transport = oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: c.Token})).Transport
	} else {
		var tr *ghinstallation.Transport

		if _, err := os.Stat(c.AppPrivateKey); err == nil {
			tr, err = ghinstallation.NewKeyFromFile(http.DefaultTransport, c.AppID, c.AppInstallationID, c.AppPrivateKey)
			if err != nil {
				return nil, fmt.Errorf("authentication failed: using private key at %s: %v", c.AppPrivateKey, err)
			}
		} else {
			tr, err = ghinstallation.New(http.DefaultTransport, c.AppID, c.AppInstallationID, []byte(c.AppPrivateKey))
			if err != nil {
				return nil, fmt.Errorf("authentication failed: using private key of size %d (%s...): %v", len(c.AppPrivateKey), strings.Split(c.AppPrivateKey, "\n")[0], err)
			}
		}

		if len(c.EnterpriseURL) > 0 {
			githubAPIURL, err := getEnterpriseApiUrl(c.EnterpriseURL)
			if err != nil {
				return nil, fmt.Errorf("enterprise url incorrect: %v", err)
			}
			tr.BaseURL = githubAPIURL
		} else if c.URL != "" && tr.BaseURL != c.URL {
			tr.BaseURL = c.URL
		}
		transport = tr
	}

	cached := httpcache.NewTransport(httpcache.NewMemoryCache())
	cached.Transport = transport
	loggingTransport := logging.Transport{Transport: cached, Log: c.Log}
	metricsTransport := metrics.Transport{Transport: loggingTransport}
	httpClient := &http.Client{Transport: metricsTransport}

	metrics.Register()

	var client *github.Client
	var githubBaseURL string
	var isEnterprise bool
	if len(c.EnterpriseURL) > 0 {
		var err error
		isEnterprise = true
		client, err = github.NewEnterpriseClient(c.EnterpriseURL, c.EnterpriseURL, httpClient)
		if err != nil {
			return nil, fmt.Errorf("enterprise client creation failed: %v", err)
		}
		githubBaseURL = fmt.Sprintf("%s://%s%s", client.BaseURL.Scheme, client.BaseURL.Host, strings.TrimSuffix(client.BaseURL.Path, "api/v3/"))
	} else {
		client = github.NewClient(httpClient)
		githubBaseURL = "https://github.com/"

		if len(c.URL) > 0 {
			baseUrl, err := url.Parse(c.URL)
			if err != nil {
				return nil, fmt.Errorf("github client creation failed: %v", err)
			}
			if !strings.HasSuffix(baseUrl.Path, "/") {
				baseUrl.Path += "/"
			}
			client.BaseURL = baseUrl
		}

		if len(c.UploadURL) > 0 {
			uploadUrl, err := url.Parse(c.UploadURL)
			if err != nil {
				return nil, fmt.Errorf("github client creation failed: %v", err)
			}
			if !strings.HasSuffix(uploadUrl.Path, "/") {
				uploadUrl.Path += "/"
			}
			client.UploadURL = uploadUrl
		}

		if len(c.RunnerGitHubURL) > 0 {
			githubBaseURL = c.RunnerGitHubURL
			if !strings.HasSuffix(githubBaseURL, "/") {
				githubBaseURL += "/"
			}
		}
	}
	client.UserAgent = "actions-runner-controller/" + build.Version
	return &Client{
		Client:        client,
		regTokens:     map[string]*github.RegistrationToken{},
		mu:            sync.Mutex{},
		GithubBaseURL: githubBaseURL,
		IsEnterprise:  isEnterprise,
	}, nil
}

// GetRegistrationToken returns a registration token tied with the name of repository and runner.
func (c *Client) GetRegistrationToken(ctx context.Context, enterprise, org, repo, name string) (*github.RegistrationToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := getRegistrationKey(org, repo, enterprise)
	rt, ok := c.regTokens[key]

	// We'd like to allow the runner just starting up to miss the expiration date by a bit.
	// Note that this means that we're going to cache Creation Registraion Token API response longer than the
	// recommended cache duration.
	//
	// https://docs.github.com/en/rest/reference/actions#create-a-registration-token-for-a-repository
	// https://docs.github.com/en/rest/reference/actions#create-a-registration-token-for-an-organization
	// https://docs.github.com/en/rest/reference/actions#create-a-registration-token-for-an-enterprise
	// https://docs.github.com/en/rest/overview/resources-in-the-rest-api#conditional-requests
	//
	// This is currently set to 30 minutes as the result of the discussion took place at the following issue:
	// https://github.com/actions/actions-runner-controller/issues/1295
	runnerStartupTimeout := 30 * time.Minute

	if ok && rt.GetExpiresAt().After(time.Now().Add(runnerStartupTimeout)) {
		return rt, nil
	}

	enterprise, owner, repo, err := getEnterpriseOrganizationAndRepo(enterprise, org, repo)

	if err != nil {
		return rt, err
	}

	rt, res, err := c.createRegistrationToken(ctx, enterprise, owner, repo)

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
func (c *Client) RemoveRunner(ctx context.Context, enterprise, org, repo string, runnerID int64) error {
	enterprise, owner, repo, err := getEnterpriseOrganizationAndRepo(enterprise, org, repo)

	if err != nil {
		return err
	}

	res, err := c.removeRunner(ctx, enterprise, owner, repo, runnerID)

	if err != nil {
		return fmt.Errorf("failed to remove runner: %w", err)
	}

	if res.StatusCode != 204 {
		return fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return nil
}

// ListRunners returns a list of runners of specified owner/repository name.
func (c *Client) ListRunners(ctx context.Context, enterprise, org, repo string) ([]*github.Runner, error) {
	enterprise, owner, repo, err := getEnterpriseOrganizationAndRepo(enterprise, org, repo)

	if err != nil {
		return nil, err
	}

	var runners []*github.Runner

	opts := github.ListOptions{PerPage: 100}
	for {
		list, res, err := c.listRunners(ctx, enterprise, owner, repo, &opts)

		if err != nil {
			return runners, fmt.Errorf("failed to list runners: %w", err)
		}

		runners = append(runners, list.Runners...)
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}

	return runners, nil
}

// ListOrganizationRunnerGroupsForRepository returns all the runner groups defined in the organization and
// inherited to the organization from an enterprise.
// We can remove this when google/go-github library is updated to support this.
func (c *Client) ListOrganizationRunnerGroupsForRepository(ctx context.Context, org, repo string) ([]*github.RunnerGroup, error) {
	var runnerGroups []*github.RunnerGroup

	var opts github.ListOrgRunnerGroupOptions

	opts.PerPage = 100

	repoName := repo
	parts := strings.Split(repo, "/")
	if len(parts) == 2 {
		repoName = parts[1]
	}
	// This must be the repo name without the owner part, so in case the repo is "myorg/myrepo" the repo name
	// passed to visible_to_repository must be "myrepo".
	opts.VisibleToRepository = repoName

	for {
		list, res, err := c.Actions.ListOrganizationRunnerGroups(ctx, org, &opts)
		if err != nil {
			return runnerGroups, fmt.Errorf("failed to list organization runner groups: %w", err)
		}

		runnerGroups = append(runnerGroups, list.RunnerGroups...)
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}

	return runnerGroups, nil
}

func (c *Client) ListRunnerGroupRepositoryAccesses(ctx context.Context, org string, runnerGroupId int64) ([]*github.Repository, error) {
	var repos []*github.Repository

	opts := github.ListOptions{PerPage: 100}
	for {
		list, res, err := c.Actions.ListRepositoryAccessRunnerGroup(ctx, org, runnerGroupId, &opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repository access for runner group: %w", err)
		}

		repos = append(repos, list.Repositories...)
		if res.NextPage == 0 {
			break
		}

		opts.Page = res.NextPage
	}

	return repos, nil
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

// wrappers for github functions (switch between enterprise/organization/repository mode)
// so the calling functions don't need to switch and their code is a bit cleaner

func (c *Client) createRegistrationToken(ctx context.Context, enterprise, org, repo string) (*github.RegistrationToken, *github.Response, error) {
	if len(repo) > 0 {
		return c.Actions.CreateRegistrationToken(ctx, org, repo)
	}
	if len(org) > 0 {
		return c.Actions.CreateOrganizationRegistrationToken(ctx, org)
	}
	return c.Enterprise.CreateRegistrationToken(ctx, enterprise)
}

func (c *Client) removeRunner(ctx context.Context, enterprise, org, repo string, runnerID int64) (*github.Response, error) {
	if len(repo) > 0 {
		return c.Actions.RemoveRunner(ctx, org, repo, runnerID)
	}
	if len(org) > 0 {
		return c.Actions.RemoveOrganizationRunner(ctx, org, runnerID)
	}
	return c.Enterprise.RemoveRunner(ctx, enterprise, runnerID)
}

func (c *Client) listRunners(ctx context.Context, enterprise, org, repo string, opts *github.ListOptions) (*github.Runners, *github.Response, error) {
	if len(repo) > 0 {
		return c.Actions.ListRunners(ctx, org, repo, opts)
	}
	if len(org) > 0 {
		return c.Actions.ListOrganizationRunners(ctx, org, opts)
	}
	return c.Enterprise.ListRunners(ctx, enterprise, opts)
}

func (c *Client) ListRepositoryWorkflowRuns(ctx context.Context, user string, repoName string) ([]*github.WorkflowRun, error) {
	queued, err := c.listRepositoryWorkflowRuns(ctx, user, repoName, "queued")
	if err != nil {
		return nil, fmt.Errorf("listing queued workflow runs: %w", err)
	}

	inProgress, err := c.listRepositoryWorkflowRuns(ctx, user, repoName, "in_progress")
	if err != nil {
		return nil, fmt.Errorf("listing in_progress workflow runs: %w", err)
	}

	var workflowRuns []*github.WorkflowRun

	workflowRuns = append(workflowRuns, queued...)
	workflowRuns = append(workflowRuns, inProgress...)

	return workflowRuns, nil
}

func (c *Client) listRepositoryWorkflowRuns(ctx context.Context, user string, repoName, status string) ([]*github.WorkflowRun, error) {
	var workflowRuns []*github.WorkflowRun

	opts := github.ListWorkflowRunsOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
		Status: status,
	}

	for {
		list, res, err := c.Actions.ListRepositoryWorkflowRuns(ctx, user, repoName, &opts)

		if err != nil {
			return workflowRuns, fmt.Errorf("failed to list workflow runs: %v", err)
		}

		workflowRuns = append(workflowRuns, list.WorkflowRuns...)
		if res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}

	return workflowRuns, nil
}

// Validates enterprise, organization and repo arguments. Both are optional, but at least one should be specified
func getEnterpriseOrganizationAndRepo(enterprise, org, repo string) (string, string, string, error) {
	if len(repo) > 0 {
		owner, repository, err := splitOwnerAndRepo(repo)
		return "", owner, repository, err
	}
	if len(org) > 0 {
		return "", org, "", nil
	}
	if len(enterprise) > 0 {
		return enterprise, "", "", nil
	}
	return "", "", "", fmt.Errorf("enterprise, organization and repository are all empty")
}

func getRegistrationKey(org, repo, enterprise string) string {
	return fmt.Sprintf("org=%s,repo=%s,enterprise=%s", org, repo, enterprise)
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

type RunnerNotFound struct {
	runnerName string
}

func (e *RunnerNotFound) Error() string {
	return fmt.Sprintf("runner %q not found", e.runnerName)
}

type RunnerOffline struct {
	runnerName string
}

func (e *RunnerOffline) Error() string {
	return fmt.Sprintf("runner %q offline", e.runnerName)
}

func (r *Client) IsRunnerBusy(ctx context.Context, enterprise, org, repo, name string) (bool, error) {
	runners, err := r.ListRunners(ctx, enterprise, org, repo)
	if err != nil {
		return false, err
	}

	for _, runner := range runners {
		if runner.GetName() == name {
			if runner.GetStatus() == "offline" {
				return runner.GetBusy(), &RunnerOffline{runnerName: name}
			}
			return runner.GetBusy(), nil
		}
	}

	return false, &RunnerNotFound{runnerName: name}
}
