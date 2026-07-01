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

// CheckRunAnnotator is a basic implementation that rejects calls without credentials.
type CheckRunAnnotator struct {
	Log logr.Logger
}

func (a *CheckRunAnnotator) CreateErrorAnnotation(ctx context.Context, opts ErrorAnnotationOpts) error {
	if opts.Repository == "" {
		return fmt.Errorf("cannot create check run annotation: repository is empty (org-level runners not supported)")
	}

	return fmt.Errorf("check run annotation requires credentials; use CheckRunAnnotatorWithCredentials or DynamicCheckRunAnnotator")
}

// CheckRunAnnotatorWithCredentials implements GitHubAnnotator with static credentials.
// Useful for testing or single-repo deployments.
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

	client, err := newGitHubClientFromConfig(a.AppConfig, a.ConfigURL)
	if err != nil {
		return fmt.Errorf("failed to create GitHub client for annotations: %w", err)
	}

	return createCheckRun(ctx, client, opts, a.Log)
}

// DynamicCheckRunAnnotator implements GitHubAnnotator by resolving credentials
// dynamically per-runner via the SecretResolver. This is the production implementation
// used when wired up in main.go.
type DynamicCheckRunAnnotator struct {
	Log            logr.Logger
	SecretResolver SecretResolver
}

func (a *DynamicCheckRunAnnotator) CreateErrorAnnotation(ctx context.Context, opts ErrorAnnotationOpts) error {
	if opts.Repository == "" {
		a.Log.Info("Skipping GitHub annotation: no repository (org-level runner)", "owner", opts.Owner)
		return nil
	}

	if opts.runner == nil {
		return fmt.Errorf("runner object is required for dynamic credential resolution")
	}

	appConfig, err := a.SecretResolver.GetAppConfig(ctx, opts.runner)
	if err != nil {
		return fmt.Errorf("failed to resolve credentials for annotation: %w", err)
	}

	client, err := newGitHubClientFromConfig(appConfig, opts.runner.GitHubConfigUrl())
	if err != nil {
		return fmt.Errorf("failed to create GitHub client: %w", err)
	}

	return createCheckRun(ctx, client, opts, a.Log)
}

func createCheckRun(ctx context.Context, client *github.Client, opts ErrorAnnotationOpts, log logr.Logger) error {
	summary := fmt.Sprintf(
		"Runner pod `%s/%s` failed to start after multiple retries.\n\n**Error:** %s",
		opts.Namespace,
		opts.RunnerName,
		opts.Message,
	)

	title := fmt.Sprintf("Runner startup failure: %s", opts.Reason)
	status := "completed"
	conclusion := "failure"

	checkRunOpts := &github.CreateCheckRunOptions{
		Name:       checkRunName,
		Status:     &status,
		Conclusion: &conclusion,
		Output: &github.CheckRunOutput{
			Title:   &title,
			Summary: &summary,
		},
	}

	if opts.WorkflowRunID > 0 {
		run, _, err := client.Actions.GetWorkflowRunByID(ctx, opts.Owner, opts.Repository, opts.WorkflowRunID)
		if err != nil {
			log.Info("Could not resolve head SHA from workflow run", "workflowRunID", opts.WorkflowRunID, "error", err)
		} else if run.GetHeadSHA() != "" {
			checkRunOpts.HeadSHA = run.GetHeadSHA()
		}
	}

	if checkRunOpts.HeadSHA == "" {
		log.Info("No head SHA available for check run, skipping annotation")
		return nil
	}

	_, _, err := client.Checks.CreateCheckRun(ctx, opts.Owner, opts.Repository, *checkRunOpts)
	if err != nil {
		return fmt.Errorf("failed to create check run: %w", err)
	}

	log.Info("Created GitHub check run annotation", "owner", opts.Owner, "repo", opts.Repository, "runner", opts.RunnerName)
	return nil
}

func newGitHubClientFromConfig(appConfig *appconfig.AppConfig, configURL string) (*github.Client, error) {
	ghConfig, err := actions.ParseGitHubConfigFromURL(configURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub config URL: %w", err)
	}

	apiURL := ghConfig.GitHubAPIURL("")

	if appConfig.Token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: appConfig.Token})
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

	appID, err := strconv.ParseInt(appConfig.AppID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse app ID: %w", err)
	}

	transport, err := ghinstallation.New(
		http.DefaultTransport,
		appID,
		appConfig.AppInstallationID,
		[]byte(appConfig.AppPrivateKey),
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
