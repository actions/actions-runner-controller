package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

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

func metricLabels(base []string, extras ...string) []string {
	return append(append([]string{}, base...), extras...)
}

var (
	jobLabels = []string{
		// "request_id",
		"repository_name",
		"owner_name",
		// "workflow_run_id",
		"job_name",
		"job_workflow_ref",
		"event_name"}
	// runnerLabels = []string{
	// 	"runner_id",
	// 	"runner_name",
	// }

	// "queue_time",
	// "scale_set_assign_time",
	// "runner_assign_time",
	// "finish_time"}
	runnerScaleSetLabels = []string{
		"runner_scale_set_name",
		"runner_scale_set_config_url",
		"auto_scaling_runner_set_name",
		"auto_scaling_runner_set_namespace"}

	/////////////////////
	githubRunnerScaleSetAvailableJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_available_jobs",
			Help: "Number of jobs with `runs-on` match the runner scale set name and haven't been assigned to this runner scale set.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetAcquiredJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_acquired_jobs",
			Help: "Number of jobs with `runs-on` match the runner scale set name and haven't been assigned but already acquired by this runner scale set.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetAssignedJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_assigned_jobs",
			Help: "Number of jobs has assigned to this runner scale set.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetRunningJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_running_jobs",
			Help: "Number of jobs has been assigned to this runner scale set and is running (or about to run) on a runner.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetRegisteredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_registered_runners",
			Help: "Number of runners registered to this runner scale set.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetBusyRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_busy_runners",
			Help: "Number of runners registered to this runner scale set and is running a job.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetIdleRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_idle_runners",
			Help: "Number of runners registered to this runner scale set and is not running a job.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetAcquireJobTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_runner_scale_set_acquire_job_total",
			Help: "Total count of workflow jobs acquired by this runner scale set.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetDesiredEphemeralRunnerPods = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_scale_set_desired_ephemeral_runner_pods",
			Help: "Number of ephemeral runner pods desired by this runner scale set.",
		},
		runnerScaleSetLabels,
	)

	githubRunnerScaleSetJobAvailableTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_runner_scale_set_job_available_total",
			Help: "Total count of workflow jobs queued and available for this runner scale set (runs-on match and all runner group permission check pass).",
		},
		metricLabels(jobLabels),
	)

	githubRunnerScaleSetJobAssignedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_runner_scale_set_job_assigned_total",
			Help: "Total count of workflow jobs assigned to this runner scale set.",
		},
		metricLabels(jobLabels),
	)

	githubRunnerScaleSetJobStartedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_runner_scale_set_job_started_total",
			Help: "Total count of workflow jobs started on a runner of this runner scale set.",
		},
		metricLabels(jobLabels, "runner_id", "runner_name"),
	)

	githubRunnerScaleSetJobCompletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "github_runner_scale_set_job_completed_total",
			Help: "Total count of workflow jobs completed on this runner scale set.",
		},
		metricLabels(jobLabels, "job_result", "runner_id", "runner_name"),
	)

	githubRunnerScaleSetJobQueueDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_runner_scale_set_job_queue_duration_seconds",
			Help:    "Wait times for workflow jobs get assigned to the runner scale set after queuing. (in seconds)",
			Buckets: runtimeBuckets,
		},
		metricLabels(jobLabels),
	)

	githubRunnerScaleSetJobStartDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_runner_scale_set_job_start_duration_seconds",
			Help:    "Wait times for workflow jobs get started on a runner of the runner scale set after being assigned. (in seconds)",
			Buckets: runtimeBuckets,
		},
		metricLabels(jobLabels, "runner_id", "runner_name"),
	)

	githubRunnerScaleSetJobRunDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "github_runner_scale_set_job_run_duration_seconds",
			Help:    "Total execution times for a workflow job runs on a runner of this runner scale set. (in seconds)",
			Buckets: runtimeBuckets,
		},
		metricLabels(jobLabels, "job_result", "runner_id", "runner_name"),
	)
)
