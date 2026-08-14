package actionsgithubcom

import (
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestJobCompletionMatchesRunner(t *testing.T) {
	finishedAt := metav1.NewTime(time.Now())
	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{Name: "runner-1"},
		Status: v1alpha1.EphemeralRunnerStatus{
			RunnerID:      42,
			RunnerName:    "runner-1",
			JobID:         "job-1",
			WorkflowRunID: 100,
			JobCompletion: &v1alpha1.EphemeralRunnerJobCompletion{
				Result:        "cancelled",
				RunnerID:      42,
				JobID:         "job-1",
				WorkflowRunID: 100,
				FinishedAt:    finishedAt,
			},
		},
	}

	assert.True(t, jobCompletionMatchesRunner(runner))

	tests := map[string]func(*v1alpha1.EphemeralRunner){
		"runner id":       func(r *v1alpha1.EphemeralRunner) { r.Status.JobCompletion.RunnerID++ },
		"runner name":     func(r *v1alpha1.EphemeralRunner) { r.Status.RunnerName = "another-runner" },
		"job id":          func(r *v1alpha1.EphemeralRunner) { r.Status.JobCompletion.JobID = "another-job" },
		"workflow run id": func(r *v1alpha1.EphemeralRunner) { r.Status.JobCompletion.WorkflowRunID++ },
	}
	for name, mutate := range tests {
		t.Run("rejects mismatched "+name, func(t *testing.T) {
			candidate := runner.DeepCopy()
			mutate(candidate)
			assert.False(t, jobCompletionMatchesRunner(candidate))
		})
	}
}
