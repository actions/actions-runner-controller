// Package metrics provides monitoring of the GitHub related metrics.
//
// This depends on the metrics exporter of kubebuilder.
// See https://book.kubebuilder.io/reference/metrics.html for details.
package actionsmetrics

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	prometheusRunBucketIntervalsName   = "PROMETHEUS_RUN_BUCKET_INTERVALS"
	prometheusQueueBucketIntervalsName = "PROMETHEUS_QUEUE_BUCKET_INTERVALS"
)

func init() {
	queueBuckets := defaultRuntimeBuckets
	if _, ok := os.LookupEnv(prometheusQueueBucketIntervalsName); ok {
		buckets, err := parseBucketsString(os.Getenv(prometheusQueueBucketIntervalsName))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse %s, using default buckets: %s\n", prometheusQueueBucketIntervalsName, err)
		} else {
			queueBuckets = buckets
		}
	}

	runBuckets := defaultRuntimeBuckets
	if _, ok := os.LookupEnv(prometheusRunBucketIntervalsName); ok {
		buckets, err := parseBucketsString(os.Getenv(prometheusRunBucketIntervalsName))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse %s, using default buckets: %s\n", prometheusRunBucketIntervalsName, err)
		} else {
			runBuckets = buckets
		}
	}

	githubWorkflowJobQueueDurationSeconds = initGithubWorkflowJobQueueDurationSeconds(queueBuckets)
	githubWorkflowJobRunDurationSeconds = initGithubWorkflowJobRunDurationSeconds(runBuckets)

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
	defaultRuntimeBuckets = []float64{
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

func metricLabels(extras ...string) []string {
	return append(append([]string{}, commonLabels...), extras...)
}

func parseBucketsString(value string) ([]float64, error) {
	valuesStr := strings.Split(value, ",")
	buckets := make([]float64, 0, len(valuesStr))

	for _, str := range valuesStr {
		val, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, val)
	}

	return buckets, nil
}

func initGithubWorkflowJobQueueDurationSeconds(buckets []float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_workflow_job_queue_duration_seconds",
			Help:    "Queue times for workflow jobs in seconds",
			Buckets: buckets,
		},
		metricLabels(),
	)
}

func initGithubWorkflowJobRunDurationSeconds(buckets []float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_workflow_job_run_duration_seconds",
			Help:    "Run times for workflow jobs in seconds",
			Buckets: buckets,
		},
		metricLabels("job_conclusion"),
	)
}

var (
	commonLabels                          = []string{"runs_on", "job_name", "organization", "repository", "owner", "workflow_name", "is_main_branch"}
	githubWorkflowJobQueueDurationSeconds *prometheus.HistogramVec
	githubWorkflowJobRunDurationSeconds   *prometheus.HistogramVec
	githubWorkflowJobConclusionsTotal     = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_job_conclusions_total",
			Help: "Conclusions for tracked workflow jobs",
		},
		metricLabels("job_conclusion"),
	)
	githubWorkflowJobsQueuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_jobs_queued_total",
			Help: "Total count of workflow jobs queued (events where job_status=queued)",
		},
		metricLabels(),
	)
	githubWorkflowJobsStartedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_jobs_started_total",
			Help: "Total count of workflow jobs started (events where job_status=in_progress)",
		},
		metricLabels(),
	)
	githubWorkflowJobsCompletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_jobs_completed_total",
			Help: "Total count of workflow jobs completed (events where job_status=completed)",
		},
		metricLabels("runner_name"),
	)
	githubWorkflowJobFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_workflow_job_failures_total",
			Help: "Conclusions for tracked workflow runs",
		},
		metricLabels("failed_step", "exit_code"),
	)
)
