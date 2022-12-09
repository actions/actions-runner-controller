// Package metrics provides monitoring of the GitHub related metrics.
//
// This depends on the metrics exporter of kubebuilder.
// See https://book.kubebuilder.io/reference/metrics.html for details.
package actionsmetrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func init() {
	metrics.Registry.MustRegister(
		githubWorkflowJobQueueDurationSeconds,
		githubWorkflowJobRunDurationSeconds,
		githubWorkflowJobConclusionsTotal,
		githubWorkflowJobsQueuedTotal,
		githubWorkflowJobsStartedTotal,
		githubWorkflowJobsCompletedTotal,
		githubWorkflowJobFailuresTotal,
	)
}

var (
	runtimeBuckets []float64 = []float64{
		0.01,
		0.05,
		0.1,
		0.5,
		1,
		2,
		3,
		4,
		5,
		6,
		7,
		8,
		9,
		10,
		12,
		15,
		18,
		20,
		25,
		30,
		40,
		50,
		60,
		70,
		80,
		90,
		100,
		110,
		120,
		150,
		180,
		210,
		240,
		300,
		360,
		420,
		480,
		540,
		600,
		900,
		1200,
		1800,
		2400,
		3000,
		3600,
	}
)

var (
	githubWorkflowJobQueueDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_workflow_job_queue_duration_seconds",
			Help:    "Queue times for workflow jobs in seconds",
			Buckets: runtimeBuckets,
		},
		[]string{"runs_on", "job_name"},
	)
	githubWorkflowJobRunDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_workflow_job_run_duration_seconds",
			Help:    "Run times for workflow jobs in seconds",
			Buckets: runtimeBuckets,
		},
		[]string{"runs_on", "job_name", "job_conclusion"},
	)
	githubWorkflowJobConclusionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_job_conclusions_total",
			Help: "Conclusions for tracked workflow jobs",
		},
		[]string{"runs_on", "job_name", "job_conclusion"},
	)
	githubWorkflowJobsQueuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_jobs_queued_total",
			Help: "Total count of workflow jobs queued (events where job_status=queued)",
		},
		[]string{"runs_on", "job_name"},
	)
	githubWorkflowJobsStartedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_jobs_started_total",
			Help: "Total count of workflow jobs started (events where job_status=in_progress)",
		},
		[]string{"runs_on", "job_name"},
	)
	githubWorkflowJobsCompletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_jobs_completed_total",
			Help: "Total count of workflow jobs completed (events where job_status=completed)",
		},
		[]string{"runs_on", "job_name"},
	)
	githubWorkflowJobFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_job_failures_total",
			Help: "Conclusions for tracked workflow runs",
		},
		[]string{"runs_on", "job_name", "failed_step", "exit_code"},
	)
)
