package controllers

import (
	"fmt"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
	"github.com/summerwind/actions-runner-controller/github/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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

	metav1Now := metav1.Now()
	testcases := []struct {
		repo         string
		org          string
		fixed        *int
		max          *int
		min          *int
		sReplicas    *int
		sTime        *metav1.Time
		workflowRuns string
		want         int
		err          string
	}{
		// 3 demanded, max at 3
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// 2 demanded, max at 3, currently 3, delay scaling down due to grace period
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(3),
			sReplicas:    intPtr(3),
			sTime:        &metav1Now,
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// 3 demanded, max at 2
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(2),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 2 demanded, min at 2
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 3, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 2
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 2
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 1
		{
			repo:         "test/valid",
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			want:         1,
		},
		// 1 demanded, min at 1
		{
			repo:         "test/valid",
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         1,
		},
		// fixed at 3
		{
			repo:  "test/valid",
			fixed: intPtr(3),
			want:  3,
		},
		// org runner, fixed at 3
		{
			org:   "test",
			fixed: intPtr(3),
			want:  3,
		},
		// org runner, 1 demanded, min at 1
		{
			org:          "test",
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			err:          "Autoscaling is currently supported only when spec.repository is set",
		},
	}

	for i := range testcases {
		tc := testcases[i]

		log := zap.New(func(o *zap.Options) {
			o.Development = true
		})

		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)

		t.Run(fmt.Sprintf("case %d", i), func(t *testing.T) {
			server := fake.NewServer(fake.WithListRepositoryWorkflowRunsResponse(200, tc.workflowRuns))
			defer server.Close()
			client := newGithubClient(server)

			r := &RunnerDeploymentReconciler{
				Log:          log,
				GitHubClient: client,
				Scheme:       scheme,
			}

			rd := v1alpha1.RunnerDeployment{
				TypeMeta: metav1.TypeMeta{},
				Spec: v1alpha1.RunnerDeploymentSpec{
					Template: v1alpha1.RunnerTemplate{
						Spec: v1alpha1.RunnerSpec{
							Repository: tc.repo,
						},
					},
					Replicas:    tc.fixed,
					MaxReplicas: tc.max,
					MinReplicas: tc.min,
				},
				Status: v1alpha1.RunnerDeploymentStatus{
					Replicas:                   tc.sReplicas,
					LastSuccessfulScaleOutTime: tc.sTime,
				},
			}

			rs, err := r.newRunnerReplicaSetWithAutoscaling(rd)
			if err != nil {
				if tc.err == "" {
					t.Fatalf("unexpected error: expected none, got %v", err)
				} else if err.Error() != tc.err {
					t.Fatalf("unexpected error: expected %v, got %v", tc.err, err)
				}
				return
			}

			got := rs.Spec.Replicas

			if got == nil {
				t.Fatalf("unexpected value of rs.Spec.Replicas: nil")
			}

			if *got != tc.want {
				t.Errorf("%d: incorrect desired replicas: want %d, got %d", i, tc.want, *got)
			}
		})
	}
}
