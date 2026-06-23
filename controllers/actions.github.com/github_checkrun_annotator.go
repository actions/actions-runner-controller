package actionsgithubcom

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v52/github"
	"golang.org/x/oauth2"
)

const checkRunName = "runner-pod-failure"

// CheckRunAnnotator implements GitHubAnnotator by creating GitHub Check Runs
// using the go-github client library.
type CheckRunAnnotator struct {
	Log logr.Logger
}

func (a *CheckRunAnnotator) CreateErrorAnnotation(ctx context.Context, opts ErrorAnnotationOpts) error {
	if opts.Repository == "" {
		return fmt.Errorf("cannot create check run annotation: repository is empty (org-level runners not supported)")
	}

	return fmt.Errorf("check run annotation requires a GitHub client; use NewCheckRunAnnotatorWithConfig instead")
}

// CheckRunAnnotatorWithCredentials implements GitHubAnnotator with credential resolution.
// It creates a go-github client per call using the provided AppConfig.
type CheckRunAnnotatorWithCredentials struct {
	Log       logr.Logger
	AppConfig *appconfig.AppConfig
	ConfigURL string
}

func (a *CheckRunAnnotatorWithCredentials) CreateErrorAnnotation(ctx context.Context, opts ErrorAnnotationOpts) error {
	if opts.Repository == "" {
		a.Log.Info("Skipping GitHub annotation: no repository (org-level runner)", "owner", opts.Owner)
		return nil
	}

	client, err := a.newGitHubClient()
	if err != nil {
		return fmt.Errorf("failed to create GitHub client for annotations: %w", err)
	}

	summary := fmt.Sprintf(
		"Runner pod `%s/%s` failed to start after %s retries.\n\n**Error:** %s",
		opts.Namespace,
		opts.RunnerName,
		"multiple",
		opts.Message,
	)

	title := fmt.Sprintf("Runner startup failure: %s", opts.Reason)

	status := "completed"
	conclusion := "failure"

	checkRun := &github.CreateCheckRunOptions{
		Name:       checkRunName,
		Status:     &status,
		Conclusion: &conclusion,
		Output: &github.CheckRunOutput{
			Title:   &title,
			Summary: &summary,
		},
	}

	// If we have a specific workflow run, try to get its head SHA for the check run
	if opts.WorkflowRunID > 0 {
		run, _, err := client.Actions.GetWorkflowRunByID(ctx, opts.Owner, opts.Repository, opts.WorkflowRunID)
		if err == nil && run.GetHeadSHA() != "" {
			checkRun.HeadSHA = run.GetHeadSHA()
		} else {
			a.Log.Info("Could not resolve head SHA from workflow run, using default branch", "workflowRunID", opts.WorkflowRunID)
		}
	}

	// If no head SHA resolved, we cannot create a check run (it's required)
	if checkRun.HeadSHA == "" {
		a.Log.Info("No head SHA available for check run, skipping annotation")
		return nil
	}

	_, _, err = client.Checks.CreateCheckRun(ctx, opts.Owner, opts.Repository, *checkRun)
	if err != nil {
		return fmt.Errorf("failed to create check run: %w", err)
	}

	a.Log.Info("Created GitHub check run annotation", "owner", opts.Owner, "repo", opts.Repository, "runner", opts.RunnerName)
	return nil
}

func (a *CheckRunAnnotatorWithCredentials) newGitHubClient() (*github.Client, error) {
	ghConfig, err := actions.ParseGitHubConfigFromURL(a.ConfigURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub config URL: %w", err)
	}

	apiURL := ghConfig.GitHubAPIURL("")

	if a.AppConfig.Token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: a.AppConfig.Token})
		httpClient := oauth2.NewClient(context.Background(), ts)
		if apiURL.Host == "api.github.com" {
			return github.NewClient(httpClient), nil
		}
		client, err := github.NewEnterpriseClient(apiURL.String(), apiURL.String(), httpClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create enterprise client with PAT: %w", err)
		}
		return client, nil
	}

	appID, err := strconv.ParseInt(a.AppConfig.AppID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse app ID: %w", err)
	}

	transport, err := ghinstallation.New(
		http.DefaultTransport,
		appID,
		a.AppConfig.AppInstallationID,
		[]byte(a.AppConfig.AppPrivateKey),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub App transport: %w", err)
	}

	if apiURL.Host != "api.github.com" {
		transport.BaseURL = apiURL.String()
	}

	httpClient := &http.Client{Transport: transport}
	if apiURL.Host == "api.github.com" {
		return github.NewClient(httpClient), nil
	}
	client, err := github.NewEnterpriseClient(apiURL.String(), apiURL.String(), httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create enterprise client with GitHub App: %w", err)
	}
	return client, nil
}
