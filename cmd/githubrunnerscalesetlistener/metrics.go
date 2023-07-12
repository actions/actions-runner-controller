package main

import "github.com/prometheus/client_golang/prometheus"

// labels
var (
	scaleSetLabels = []string{
		"runner_scale_set_name",
		"runner_scale_set_config_url",
		"auto_scaling_runner_set_name",
		"auto_scaling_runner_set_namespace",
	}

	jobLabels = []string{
		"repository_name",
		"owner_name",
		"job_name",
		"job_workflow_ref",
		"event_name",
	}
)

// metrics
var (
	availableJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("available_jobs"),
			Help: "Number of jobs with `runs-on` matching the runner scale set name. Jobs are not yet assigned but are acquired by the runner scale set.",
		},
		scaleSetLabels,
	)

	acquiredJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("acquired_jobs"),
			Help: "Number of jobs acquired by the scale set.",
		},
		scaleSetLabels,
	)

	assignedJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("assigned_jobs"),
			Help: "Number of jobs assigned to this scale set.",
		},
		scaleSetLabels,
	)

	runningJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("running_jobs"),
			Help: "Number of jobs running (or about to be run).",
		},
		scaleSetLabels,
	)

	registeredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("registered_runners"),
			Help: "Number of registered runners.",
		},
		scaleSetLabels,
	)

	busyRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("busy_runners"),
			Help: "Number of registered runners running a job.",
		},
		scaleSetLabels,
	)

	desiredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("desired_runners"),
			Help: "Number of runners desired bu the scale set.",
		},
		scaleSetLabels,
	)

	idleRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricName("idle_runners"),
			Help: "Number of registered runners not running a job.",
		},
		scaleSetLabels,
	)

	acquiredJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricName("acquired_jobs_total"),
			Help: "Total number of jobs acquired by the scale set.",
		},
		scaleSetLabels,
	)

	assignedJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricName("assigned_jobs_total"),
			Help: "Total number of jobs assigned to the scale set.",
		},
		jobLabels,
	)

	startedJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricName("started_jobs_total"),
			Help: "Total number of jobs started.",
		},
		append(jobLabels, "runner_id"),
	)

	completedJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricName("completed_jobs_total"),
			Help: "Total number of jobs completed.",
		},
		append(jobLabels, "job_result", "runner_id", "runner_name"),
	)

	jobQueueDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    metricName("job_queue_duration_seconds"),
			Help:    "Time spent waiting for workflow jobs to get assigned to the scale set after queueing (in seconds).",
			Buckets: runtimeBuckets,
		},
		jobLabels,
	)

	jobStartupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    metricName("job_startup_duration_seconds"),
			Help:    "Time spent waiting for workflow job to get started on the runner owned by the scale set (in seconds).",
			Buckets: runtimeBuckets,
		},
		append(jobLabels, "runner_id", "runner_name"),
	)

	jobExecutionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    metricName("job_execution_duration_seconds"),
			Help:    "Time spent executing workflow jobs by the scale set (in seconds).",
			Buckets: runtimeBuckets,
		},
		append(jobLabels, "job_result", "runner_id", "runner_name"),
	)
)

func metricName(name string) string {
	return "github_runner_scale_set_" + name
}

var runtimeBuckets []float64 = []float64{
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
