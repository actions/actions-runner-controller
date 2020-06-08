package controllers

import (
	"fmt"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
	"github.com/summerwind/actions-runner-controller/github/fake"
	"net/http/httptest"
	"net/url"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"testing"
)

func newGithubClient(server *httptest.Server) *github.Client {
	client, err := github.NewClientWithAccessToken("token")
	if err != nil {
		panic(err)
	}

	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		panic(err)
	}
	client.Client.BaseURL = baseURL

	return client
}

func TestDetermineDesiredReplicas_RepositoryRunner(t *testing.T) {
	intPtr := func(v int) *int {
		return &v
	}

	testcases := []struct {
		fixed        *int
		max          *int
		min          *int
		workflowRuns string
		want         int
	}{
		// 3 demanded, max at 3
		{
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// 3 demanded, max at 2
		{
			min:          intPtr(2),
			max:          intPtr(2),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 2 demanded, min at 2
		{
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 3, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 2
		{
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 2
		{
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 1
		{
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			want:         1,
		},
		// 1 demanded, min at 1
		{
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         1,
		},
		// fixed at 3
		{
			fixed: intPtr(3),
			want:  3,
		},
	}

	for i := range testcases {
		tc := testcases[i]

		log := zap.New(func(o *zap.Options) {
			o.Development = true
		})

		t.Run(fmt.Sprintf("case %d", i), func(t *testing.T) {
			server := fake.NewServer(fake.WithListRepositoryWorkflowRunsResponse(200, tc.workflowRuns))
			defer server.Close()
			client := newGithubClient(server)

			r := &RunnerDeploymentReconciler{
				Log:          log,
				GitHubClient: client,
			}

			rd := v1alpha1.RunnerDeployment{
				Spec: v1alpha1.RunnerDeploymentSpec{
					Template: v1alpha1.RunnerTemplate{
						Spec: v1alpha1.RunnerSpec{
							Repository: "test/valid",
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
		})
	}
}
