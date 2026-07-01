package actionsgithubcom

import (
	"context"
	"testing"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOwnerRepo_RepoLevel(t *testing.T) {
	owner, repo, err := parseOwnerRepo("https://github.com/my-org/my-repo")
	require.NoError(t, err)
	assert.Equal(t, "my-org", owner)
	assert.Equal(t, "my-repo", repo)
}

func TestParseOwnerRepo_OrgLevel(t *testing.T) {
	owner, repo, err := parseOwnerRepo("https://github.com/my-org")
	require.NoError(t, err)
	assert.Equal(t, "my-org", owner)
	assert.Equal(t, "", repo)
}

func TestParseOwnerRepo_InvalidURL(t *testing.T) {
	// URL with no path resolves to an empty organization, which we reject
	_, _, err := parseOwnerRepo("https://github.com")
	assert.Error(t, err)
}

type fakeAnnotator struct {
	called bool
	opts   ErrorAnnotationOpts
	err    error
}

func (f *fakeAnnotator) CreateErrorAnnotation(_ context.Context, opts ErrorAnnotationOpts) error {
	f.called = true
	f.opts = opts
	return f.err
}

func TestAnnotateRunnerFailure_CallsAnnotator(t *testing.T) {
	annotator := &fakeAnnotator{}
	runner := &v1alpha1.EphemeralRunner{}
	runner.Name = "test-runner"
	runner.Namespace = "test-ns"
	runner.Spec.GitHubConfigURL = "https://github.com/my-org/my-repo"
	runner.Status.Message = "container \"runner\": ImagePullBackOff: image not found"
	runner.Status.Reason = "ImagePull"

	annotateRunnerFailure(context.Background(), annotator, runner, logr.Discard())

	assert.True(t, annotator.called)
	assert.Equal(t, "my-org", annotator.opts.Owner)
	assert.Equal(t, "my-repo", annotator.opts.Repository)
	assert.Equal(t, "test-runner", annotator.opts.RunnerName)
	assert.Contains(t, annotator.opts.Message, "ImagePullBackOff")
}

func TestAnnotateRunnerFailure_NilAnnotator(t *testing.T) {
	runner := &v1alpha1.EphemeralRunner{}
	runner.Spec.GitHubConfigURL = "https://github.com/my-org/my-repo"

	// Should not panic
	annotateRunnerFailure(context.Background(), nil, runner, logr.Discard())
}
