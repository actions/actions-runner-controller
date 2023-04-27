package main

import (
	"strconv"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/prometheus/client_golang/prometheus"
)

// label names
const (
	labelKeyRunnerScaleSetName            = "runner_scale_set_name"
	labelKeyRunnerScaleSetConfigURL       = "runner_scale_set_config_url"
	labelKeyAutoScalingRunnerSetName      = "auto_scaling_runner_set_name"
	labelKeyAutoScalingRunnerSetNamespace = "auto_scaling_runner_set_namespace"
	labelKeyRepositoryName                = "repository_name"
	labelKeyOwnerName                     = "owner_name"
	labelKeyJobName                       = "job_name"
	labelKeyJobWorkflowRef                = "job_workflow_ref"
	labelKeyEventName                     = "event_name"
	labelKeyJobResult                     = "job_result"
	labelKeyRunnerID                      = "runner_id"
	labelKeyRunnerName                    = "runner_name"
)

// labels
var (
	scaleSetLabels = []string{
		labelKeyRunnerScaleSetName,
		labelKeyRunnerScaleSetConfigURL,
		labelKeyAutoScalingRunnerSetName,
		labelKeyAutoScalingRunnerSetNamespace,
	}

	jobLabels = []string{
		labelKeyRepositoryName,
		labelKeyOwnerName,
		labelKeyJobName,
		labelKeyJobWorkflowRef,
		labelKeyEventName,
	}

	completedJobsTotalLabels   = append(jobLabels, labelKeyJobResult, labelKeyRunnerID, labelKeyRunnerName)
	startedJobsTotalLabels     = append(jobLabels, labelKeyRunnerID)
	jobStartupDurationLabels   = append(jobLabels, labelKeyRunnerID, labelKeyRunnerName)
	jobExecutionDurationLabels = append(jobLabels, labelKeyJobResult, labelKeyRunnerID, labelKeyRunnerName)
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

	availableJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricName("available_jobs_total"),
			Help: "Total number of jobs available for the scale set (runs-on matches and scale set passes all of the runner group permission checks).",
		},
		jobLabels,
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
		startedJobsTotalLabels,
	)

	completedJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: metricName("completed_jobs_total"),
			Help: "Total number of jobs completed.",
		},
		completedJobsTotalLabels,
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
		jobStartupDurationLabels,
	)

	jobExecutionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    metricName("job_execution_duration_seconds"),
			Help:    "Time spent executing workflow jobs by the scale set (in seconds).",
			Buckets: runtimeBuckets,
		},
		jobExecutionDurationLabels,
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

type metricsExporter struct {
	// fields updated on each iteration
	stats         *actions.RunnerScaleSetStatistic
	exportFuncs   []func()
	jobsAvailable []*actions.JobAvailable
	jobsAssigned  []*actions.JobAssigned
	jobsStarted   []*actions.JobStarted
	jobsCompleted []*actions.JobCompleted
	jobsAcquired  float64

	// Initialized during creation.
	scaleSetLabels             prometheus.Labels
	jobLabels                  prometheus.Labels
	completedJobsTotalLabels   prometheus.Labels
	startedJobsTotalLabels     prometheus.Labels
	jobStartupDurationLabels   prometheus.Labels
	jobExecutionDurationLabels prometheus.Labels
}

func (m *metricsExporter) withLabels(l prometheus.Labels) {
	if l == nil {
		return
	}

	m.scaleSetLabels = makeLabels(l, scaleSetLabels)
	m.jobLabels = makeLabels(l, jobLabels)
	m.completedJobsTotalLabels = makeLabels(l, completedJobsTotalLabels)
	m.startedJobsTotalLabels = makeLabels(l, startedJobsTotalLabels)
	m.jobStartupDurationLabels = makeLabels(l, jobStartupDurationLabels)
	m.jobExecutionDurationLabels = makeLabels(l, jobExecutionDurationLabels)
}

func makeLabels(labels prometheus.Labels, names []string) prometheus.Labels {
	l := make(prometheus.Labels, len(labels))
	for _, lbl := range names {
		l[lbl] = labels[lbl]
	}
	return l
}

func (m *metricsExporter) reset() {
	m.exportFuncs = nil
}

func (m *metricsExporter) withStatistics(stats *actions.RunnerScaleSetStatistic) {
	if len(m.scaleSetLabels) == 0 {
		return
	}
	m.exportFuncs = append(m.exportFuncs, func() {
		availableJobs.With(m.scaleSetLabels).Set(float64(m.stats.TotalAvailableJobs))
		acquiredJobs.With(m.scaleSetLabels).Set(float64(m.stats.TotalAcquiredJobs))
		assignedJobs.With(m.scaleSetLabels).Set(float64(m.stats.TotalAssignedJobs))
		runningJobs.With(m.scaleSetLabels).Set(float64(m.stats.TotalAssignedJobs))
		registeredRunners.With(m.scaleSetLabels).Set(float64(m.stats.TotalRegisteredRunners))
		busyRunners.With(m.scaleSetLabels).Set(float64(m.stats.TotalBusyRunners))
		idleRunners.With(m.scaleSetLabels).Set(float64(m.stats.TotalIdleRunners))
	})
	m.stats = stats
}

func (m *metricsExporter) withJobAvailable(msg *actions.JobAvailable) {
	if len(m.jobLabels) == 0 {
		return
	}

	m.exportFuncs = append(m.exportFuncs, func() {
		availableJobsTotal.With(m.jobLabels).Inc()
	})
}

func (m *metricsExporter) withJobStarted(msg *actions.JobStarted) {
	if len(m.startedJobsTotalLabels) == 0 {
		return
	}
	m.exportFuncs = append(m.exportFuncs, func() {
		l := newPrometheusLabelsFrom(m.jobLabels)
		l[labelKeyRunnerID] = strconv.Itoa(msg.RunnerId)
		startedJobsTotal.With(l).Inc()
	})
}

func (m *metricsExporter) withJobAssigned(msg *actions.JobAssigned) {
	if len(m.jobLabels) == 0 {
		return
	}
	m.exportFuncs = append(m.exportFuncs, func() {
		assignedJobsTotal.With(m.jobLabels).Inc()
	})
}

func (m *metricsExporter) withJobCompleted(msg *actions.JobCompleted) {
	if len(m.completedJobsTotalLabels) == 0 {
		return
	}
	m.exportFuncs = append(m.exportFuncs, func() {
		l := newPrometheusLabelsFrom(m.jobLabels)
		l[labelKeyRunnerID] = strconv.Itoa(msg.RunnerId)
		l[labelKeyJobResult] = msg.Result
		l[labelKeyRunnerName] = msg.RunnerName
		completedJobsTotal.With(l).Inc()
	})
}

func newPrometheusLabelsFrom(from prometheus.Labels) prometheus.Labels {
	l := make(prometheus.Labels)
	for k, v := range from {
		l[k] = v
	}
	return l
}

func (m *metricsExporter) withJobsAcquired(count int) {
	if count == 0 {
		return
	}
	m.exportFuncs = append(m.exportFuncs, func() {
		acquiredJobsTotal.With(m.scaleSetLabels).Add(float64(count))
	})
}

func (m *metricsExporter) withDesiredRunners(count int) {
	m.exportFuncs = append(m.exportFuncs, func() {
		desiredRunners.With(m.scaleSetLabels).Add(float64(count))
	})
}

func (m *metricsExporter) do() {
	for _, f := range m.exportFuncs {
		f()
	}
}
