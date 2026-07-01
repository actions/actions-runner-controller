package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
)

func TestCheckRunAnnotatorWithCredentials_SkipsOrgLevel(t *testing.T) {
	annotator := &CheckRunAnnotatorWithCredentials{
		Log:       logr.Discard(),
		AppConfig: &appconfig.AppConfig{Token: "fake-token"},
		ConfigURL: "https://github.com/my-org",
	}

	err := annotator.CreateErrorAnnotation(context.Background(), ErrorAnnotationOpts{
		Owner:      "my-org",
		Repository: "", // org-level, no repo
		RunnerName: "test-runner",
		Message:    "ImagePullBackOff",
		Reason:     "ImagePull",
	})

	assert.NoError(t, err)
}

func TestCheckRunAnnotatorWithCredentials_SkipsNoHeadSHA(t *testing.T) {
	annotator := &CheckRunAnnotatorWithCredentials{
		Log:       logr.Discard(),
		AppConfig: &appconfig.AppConfig{Token: "fake-token"},
		ConfigURL: "https://github.com/my-org/my-repo",
	}

	// No WorkflowRunID means no head SHA can be resolved, so annotation is skipped
	err := annotator.CreateErrorAnnotation(context.Background(), ErrorAnnotationOpts{
		Owner:         "my-org",
		Repository:    "my-repo",
		RunnerName:    "test-runner",
		Message:       "ImagePullBackOff: image not found",
		Reason:        "ImagePull",
		WorkflowRunID: 0,
	})

	assert.NoError(t, err)
}

func TestCheckRunAnnotator_RejectsEmptyRepo(t *testing.T) {
	annotator := &CheckRunAnnotator{Log: logr.Discard()}

	err := annotator.CreateErrorAnnotation(context.Background(), ErrorAnnotationOpts{
		Owner:      "my-org",
		Repository: "",
		RunnerName: "test-runner",
		Message:    "ImagePullBackOff",
	})

	assert.Error(t, err)
}
