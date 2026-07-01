package actionsgithubcom

import (
	"context"
	"fmt"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
)

// GitHubAnnotator creates Check Run annotations on GitHub to surface pod errors in the Actions UI.
// Implementations must handle authentication and API calls to GitHub.
type GitHubAnnotator interface {
	CreateErrorAnnotation(ctx context.Context, opts ErrorAnnotationOpts) error
}

// ErrorAnnotationOpts contains the information needed to create a GitHub error annotation.
type ErrorAnnotationOpts struct {
	Owner         string
	Repository    string
	RunnerName    string
	Namespace     string
	Message       string
	Reason        string
	WorkflowRunID int64
	JobID         string

	// runner is the EphemeralRunner object, used internally by DynamicCheckRunAnnotator
	// to resolve credentials via SecretResolver.
	runner *v1alpha1.EphemeralRunner
}

// annotateRunnerFailure attempts to create a GitHub Check Run annotation for a failed runner.
// It is a best-effort operation — errors are logged but do not block the reconciliation.
func annotateRunnerFailure(ctx context.Context, annotator GitHubAnnotator, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) {
	if annotator == nil {
		return
	}

	owner, repo, err := parseOwnerRepo(ephemeralRunner.Spec.GitHubConfigURL)
	if err != nil {
		log.Info("Cannot annotate GitHub: failed to parse config URL", "error", err)
		return
	}

	opts := ErrorAnnotationOpts{
		Owner:         owner,
		Repository:    repo,
		RunnerName:    ephemeralRunner.Name,
		Namespace:     ephemeralRunner.Namespace,
		Message:       ephemeralRunner.Status.Message,
		Reason:        ephemeralRunner.Status.Reason,
		WorkflowRunID: ephemeralRunner.Status.WorkflowRunID,
		JobID:         ephemeralRunner.Status.JobID,
		runner:        ephemeralRunner,
	}

	if err := annotator.CreateErrorAnnotation(ctx, opts); err != nil {
		log.Error(err, "Failed to create GitHub error annotation (best-effort)")
	}
}

func parseOwnerRepo(githubConfigURL string) (string, string, error) {
	ghConfig, err := actions.ParseGitHubConfigFromURL(githubConfigURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse GitHub config URL: %w", err)
	}

	if ghConfig.Organization == "" {
		return "", "", fmt.Errorf("cannot determine owner from config URL %q", githubConfigURL)
	}

	return ghConfig.Organization, ghConfig.Repository, nil
}
