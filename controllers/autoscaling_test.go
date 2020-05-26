package controllers

import (
	"context"
	"fmt"
	github2 "github.com/google/go-github/v31/github"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
	"k8s.io/klog/klogr"
	"testing"
)

func TestDetermineDesiredReplicas_RepositoryRunner(t *testing.T) {
	intPtr := func(v int) *int {
		return &v
	}

	testcases := []struct {
		fixed               *int
		max                 *int
		min                 *int
		workflowRunStatuses []string
		want                int
	}{
		// 3 demanded, max at 3
		{
			min:                 intPtr(2),
			max:                 intPtr(3),
			workflowRunStatuses: []string{"queued", "in_progress", "in_progress", "completed"},
			want:                3,
		},
		// 3 demanded, max at 2
		{
			min:                 intPtr(2),
			max:                 intPtr(2),
			workflowRunStatuses: []string{"queued", "in_progress", "in_progress", "completed"},
			want:                2,
		},
		// 2 demanded, min at 2
		{
			min:                 intPtr(2),
			max:                 intPtr(3),
			workflowRunStatuses: []string{"queued", "in_progress", "completed"},
			want:                2,
		},
		// 1 demanded, min at 2
		{
			min:                 intPtr(2),
			max:                 intPtr(3),
			workflowRunStatuses: []string{"queued", "completed"},
			want:                2,
		},
		// 1 demanded, min at 2
		{
			min:                 intPtr(2),
			max:                 intPtr(3),
			workflowRunStatuses: []string{"in_progress", "completed"},
			want:                2,
		},
		// 1 demanded, min at 1
		{
			min:                 intPtr(1),
			max:                 intPtr(3),
			workflowRunStatuses: []string{"queued", "completed"},
			want:                1,
		},
		// 1 demanded, min at 1
		{
			min:                 intPtr(1),
			max:                 intPtr(3),
			workflowRunStatuses: []string{"in_progress", "completed"},
			want:                1,
		},
		// fixed at 3
		{
			fixed: intPtr(3),
			want:  3,
		},
	}

	for i := range testcases {
		tc := testcases[i]

		log := klogr.New()
		r := &RunnerDeploymentReconciler{
			Log: log,
			GitHubClient: &github.Client{
				ListRepositoryWorkflowRuns: func(ctx context.Context, owner, repo string, opts *github2.ListOptions) (runs *github2.WorkflowRuns, response *github2.Response, err error) {
					if owner != "foo" {
						return nil, nil, fmt.Errorf("unexpected repository owner: want %q, got %q", "foo", owner)
					}

					if repo != "bar" {
						return nil, nil, fmt.Errorf("unexpected repository name: want %q, got %q", "bar", repo)
					}

					runs = &github2.WorkflowRuns{}

					for i := range tc.workflowRunStatuses {
						runs.WorkflowRuns = append(runs.WorkflowRuns, &github2.WorkflowRun{
							Status: &tc.workflowRunStatuses[i],
						})
					}

					totalCount := len(runs.WorkflowRuns)

					runs.TotalCount = &totalCount

					return
				},
			},
		}

		rd := v1alpha1.RunnerDeployment{
			Spec: v1alpha1.RunnerDeploymentSpec{
				Template: v1alpha1.RunnerTemplate{
					Spec: v1alpha1.RunnerSpec{
						Repository: "foo/bar",
					},
				},
				Replicas:    tc.fixed,
				MaxReplicas: tc.max,
				MinReplicas: tc.min,
			},
		}

		got, err := r.determineDesiredReplicas(rd)
		if err != nil {
			t.Fatal(err)
		}

		if *got != tc.want {
			t.Errorf("%d: incorrect desired replicas: want %d, got %d", i, tc.want, *got)
		}
	}
}
