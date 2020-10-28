package controllers

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
	"github.com/summerwind/actions-runner-controller/github/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func newGithubClient(server *httptest.Server) *github.Client {
	c := github.Config{
		Token: "token",
	}
	client, err := c.NewClient()
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
		workflowJobs map[int]string
		want         int
		err          string
	}{
		// Legacy functionality
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
			repo:         "test/valid",
			min:          intPtr(1),
			max:          intPtr(3),
			fixed:        intPtr(3),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},

		// Job-level autoscaling
		// 5 requested from 3 workflows
		{
			repo:         "test/valid",
			min:          intPtr(2),
			max:          intPtr(10),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued"}, {"status":"queued"}]}`,
				2: `{"jobs": [{"status": "in_progress"}, {"status":"completed"}]}`,
				3: `{"jobs": [{"status": "in_progress"}, {"status":"queued"}]}`,
			},
			want: 5,
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
			server := fake.NewServer(fake.WithListRepositoryWorkflowRunsResponse(200, tc.workflowRuns), fake.WithListWorkflowJobsResponse(200, tc.workflowJobs))
			defer server.Close()
			client := newGithubClient(server)

			h := &HorizontalRunnerAutoscalerReconciler{
				Log:          log,
				GitHubClient: client,
				Scheme:       scheme,
			}

			rd := v1alpha1.RunnerDeployment{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: "testrd",
				},
				Spec: v1alpha1.RunnerDeploymentSpec{
					Template: v1alpha1.RunnerTemplate{
						Spec: v1alpha1.RunnerSpec{
							Repository: tc.repo,
						},
					},
					Replicas: tc.fixed,
				},
				Status: v1alpha1.RunnerDeploymentStatus{
					Replicas: tc.sReplicas,
				},
			}

			hra := v1alpha1.HorizontalRunnerAutoscaler{
				Spec: v1alpha1.HorizontalRunnerAutoscalerSpec{
					MaxReplicas: tc.max,
					MinReplicas: tc.min,
				},
				Status: v1alpha1.HorizontalRunnerAutoscalerStatus{
					DesiredReplicas:            tc.sReplicas,
					LastSuccessfulScaleOutTime: tc.sTime,
				},
			}

			got, err := h.computeReplicas(rd, hra)
			if err != nil {
				if tc.err == "" {
					t.Fatalf("unexpected error: expected none, got %v", err)
				} else if err.Error() != tc.err {
					t.Fatalf("unexpected error: expected %v, got %v", tc.err, err)
				}
				return
			}

			if got == nil {
				t.Fatalf("unexpected value of rs.Spec.Replicas: nil")
			}

			if *got != tc.want {
				t.Errorf("%d: incorrect desired replicas: want %d, got %d", i, tc.want, *got)
			}
		})
	}
}

func TestDetermineDesiredReplicas_OrganizationalRunner(t *testing.T) {
	intPtr := func(v int) *int {
		return &v
	}

	metav1Now := metav1.Now()
	testcases := []struct {
		repos        []string
		org          string
		fixed        *int
		max          *int
		min          *int
		sReplicas    *int
		sTime        *metav1.Time
		workflowRuns string
		workflowJobs map[int]string
		want         int
		err          string
	}{
		// 3 demanded, max at 3
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// 2 demanded, max at 3, currently 3, delay scaling down due to grace period
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(3),
			sReplicas:    intPtr(3),
			sTime:        &metav1Now,
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// 3 demanded, max at 2
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(2),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 2 demanded, min at 2
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 3, "workflow_runs":[{"status":"queued"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 2
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 2
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         2,
		},
		// 1 demanded, min at 1
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"completed"}]}"`,
			want:         1,
		},
		// 1 demanded, min at 1
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         1,
		},
		// fixed at 3
		{
			org:          "test",
			repos:        []string{"valid"},
			fixed:        intPtr(1),
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// org runner, fixed at 3
		{
			org:          "test",
			repos:        []string{"valid"},
			fixed:        intPtr(1),
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`,
			want:         3,
		},
		// org runner, 1 demanded, min at 1, no repos
		{
			org:          "test",
			min:          intPtr(1),
			max:          intPtr(3),
			workflowRuns: `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"completed"}]}"`,
			err:          "validating autoscaling metrics: spec.autoscaling.metrics[].repositoryNames is required and must have one more more entries for organizational runner deployment",
		},

		// Job-level autoscaling
		// 5 requested from 3 workflows
		{
			org:          "test",
			repos:        []string{"valid"},
			min:          intPtr(2),
			max:          intPtr(10),
			workflowRuns: `{"total_count": 4, "workflow_runs":[{"id": 1, "status":"queued"}, {"id": 2, "status":"in_progress"}, {"id": 3, "status":"in_progress"}, {"status":"completed"}]}"`,
			workflowJobs: map[int]string{
				1: `{"jobs": [{"status":"queued"}, {"status":"queued"}]}`,
				2: `{"jobs": [{"status": "in_progress"}, {"status":"completed"}]}`,
				3: `{"jobs": [{"status": "in_progress"}, {"status":"queued"}]}`,
			},
			want: 5,
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
			server := fake.NewServer(fake.WithListRepositoryWorkflowRunsResponse(200, tc.workflowRuns), fake.WithListWorkflowJobsResponse(200, tc.workflowJobs))
			defer server.Close()
			client := newGithubClient(server)

			h := &HorizontalRunnerAutoscalerReconciler{
				Log:          log,
				Scheme:       scheme,
				GitHubClient: client,
			}

			rd := v1alpha1.RunnerDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "testrd",
				},
				Spec: v1alpha1.RunnerDeploymentSpec{
					Template: v1alpha1.RunnerTemplate{
						Spec: v1alpha1.RunnerSpec{
							Organization: tc.org,
						},
					},
					Replicas: tc.fixed,
				},
				Status: v1alpha1.RunnerDeploymentStatus{
					Replicas: tc.sReplicas,
				},
			}

			hra := v1alpha1.HorizontalRunnerAutoscaler{
				Spec: v1alpha1.HorizontalRunnerAutoscalerSpec{
					ScaleTargetRef: v1alpha1.ScaleTargetRef{
						Name: "testrd",
					},
					MaxReplicas: tc.max,
					MinReplicas: tc.min,
					Metrics: []v1alpha1.MetricSpec{
						{
							Type:            v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns,
							RepositoryNames: tc.repos,
						},
					},
				},
				Status: v1alpha1.HorizontalRunnerAutoscalerStatus{
					DesiredReplicas:            tc.sReplicas,
					LastSuccessfulScaleOutTime: tc.sTime,
				},
			}

			got, err := h.computeReplicas(rd, hra)
			if err != nil {
				if tc.err == "" {
					t.Fatalf("unexpected error: expected none, got %v", err)
				} else if err.Error() != tc.err {
					t.Fatalf("unexpected error: expected %v, got %v", tc.err, err)
				}
				return
			}

			if got == nil {
				t.Fatalf("unexpected value of rs.Spec.Replicas: nil, wanted %v", tc.want)
			}

			if *got != tc.want {
				t.Errorf("%d: incorrect desired replicas: want %d, got %d", i, tc.want, *got)
			}
		})
	}
}
