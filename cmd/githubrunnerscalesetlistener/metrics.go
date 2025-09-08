package main

import (
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/prometheus/client_golang/prometheus"
)

// label names
const (
	labelKeyRunnerScaleSetName      = "name"
	labelKeyRunnerScaleSetNamespace = "namespace"
	labelKeyEnterprise              = "enterprise"
	labelKeyOrganization            = "organization"
	labelKeyRepository              = "repository"
	labelKeyJobName                 = "job_name"
	labelKeyJobWorkflowRef          = "job_workflow_ref"
	labelKeyEventName               = "event_name"
	labelKeyJobResult               = "job_result"
)

const githubScaleSetSubsystem = "gha"

// labels
var (
	scaleSetLabels = []string{
		labelKeyRunnerScaleSetName,
		labelKeyRepository,
		labelKeyOrganization,
		labelKeyEnterprise,
		labelKeyRunnerScaleSetNamespace,
	}

	jobLabels = []string{
		labelKeyRepository,
		labelKeyOrganization,
		labelKeyEnterprise,
		labelKeyJobName,
		labelKeyJobWorkflowRef,
		labelKeyEventName,
	}

	completedJobsTotalLabels   = append(jobLabels, labelKeyJobResult)
	jobExecutionDurationLabels = append(jobLabels, labelKeyJobResult)
	startedJobsTotalLabels     = jobLabels
	jobStartupDurationLabels   = []string{
		labelKeyRepository,
		labelKeyOrganization,
		labelKeyEnterprise,
		labelKeyEventName,
	}
)

// metrics
var (
	// availableJobs = prometheus.NewGaugeVec(
	// 	prometheus.GaugeOpts{
	// 		Subsystem: githubScaleSetSubsystem,
	// 		Name:      "available_jobs",
	// 		Help:      "Number of jobs with `runs-on` matching the runner scale set name. Jobs are not yet assigned to the runner scale set.",
	// 	},
	// 	scaleSetLabels,
	// )
	//
	// acquiredJobs = prometheus.NewGaugeVec(
	// 	prometheus.GaugeOpts{
	// 		Subsystem: githubScaleSetSubsystem,
	// 		Name:      "acquired_jobs",
	// 		Help:      "Number of jobs acquired by the scale set.",
	// 	},
	// 	scaleSetLabels,
	// )

	assignedJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "assigned_jobs",
			Help:      "Number of jobs assigned to this scale set.",
		},
		scaleSetLabels,
	)

	runningJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "running_jobs",
			Help:      "Number of jobs running (or about to be run).",
		},
		scaleSetLabels,
	)

	registeredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "registered_runners",
			Help:      "Number of runners registered by the scale set.",
		},
		scaleSetLabels,
	)

	busyRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "busy_runners",
			Help:      "Number of registered runners running a job.",
		},
		scaleSetLabels,
	)

	minRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "min_runners",
			Help:      "Minimum number of runners.",
		},
		scaleSetLabels,
	)

	maxRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "max_runners",
			Help:      "Maximum number of runners.",
		},
		scaleSetLabels,
	)

	desiredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "desired_runners",
			Help:      "Number of runners desired by the scale set.",
		},
		scaleSetLabels,
	)

	idleRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "idle_runners",
			Help:      "Number of registered runners not running a job.",
		},
		scaleSetLabels,
	)

	startedJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "started_jobs_total",
			Help:      "Total number of jobs started.",
		},
		startedJobsTotalLabels,
	)

	completedJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:      "completed_jobs_total",
			Help:      "Total number of jobs completed.",
			Subsystem: githubScaleSetSubsystem,
		},
		completedJobsTotalLabels,
	)

	// jobQueueDurationSeconds = prometheus.NewHistogramVec(
	// 	prometheus.HistogramOpts{
	// 		Subsystem: githubScaleSetSubsystem,
	// 		Name:      "job_queue_duration_seconds",
	// 		Help:      "Time spent waiting for workflow jobs to get assigned to the scale set after queueing (in seconds).",
	// 		Buckets:   runtimeBuckets,
	// 	},
	// 	jobLabels,
	// )

	jobStartupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "job_startup_duration_seconds",
			Help:      "Time spent waiting for workflow job to get started on the runner owned by the scale set (in seconds).",
			Buckets:   runtimeBuckets,
		},
		jobStartupDurationLabels,
	)

	jobExecutionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "job_execution_duration_seconds",
			Help:      "Time spent executing workflow jobs by the scale set (in seconds).",
			Buckets:   runtimeBuckets,
		},
		jobExecutionDurationLabels,
	)
)

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
	// Initialized during creation.
	baseLabels
}

type baseLabels struct {
	scaleSetName      string
	scaleSetNamespace string
	enterprise        string
	organization      string
	repository        string
}

func (b *baseLabels) jobLabels(jobBase *actions.JobMessageBase) prometheus.Labels {
	return prometheus.Labels{
		labelKeyEnterprise:     b.enterprise,
		labelKeyOrganization:   b.organization,
		labelKeyRepository:     b.repository,
		labelKeyJobName:        jobBase.JobDisplayName,
		labelKeyJobWorkflowRef: jobBase.JobWorkflowRef,
		labelKeyEventName:      jobBase.EventName,
	}
}

func (b *baseLabels) scaleSetLabels() prometheus.Labels {
	return prometheus.Labels{
		labelKeyRunnerScaleSetName:      b.scaleSetName,
		labelKeyRunnerScaleSetNamespace: b.scaleSetNamespace,
		labelKeyEnterprise:              b.enterprise,
		labelKeyOrganization:            b.organization,
		labelKeyRepository:              b.repository,
	}
}

func (b *baseLabels) completedJobLabels(msg *actions.JobCompleted) prometheus.Labels {
	l := b.jobLabels(&msg.JobMessageBase)
	l[labelKeyJobResult] = msg.Result
	return l
}

func (b *baseLabels) startedJobLabels(msg *actions.JobStarted) prometheus.Labels {
	l := b.jobLabels(&msg.JobMessageBase)
	return l
}

func (b *baseLabels) jobStartupDurationLabels(msg *actions.JobStarted) prometheus.Labels {
	return prometheus.Labels{
		labelKeyEnterprise:   b.enterprise,
		labelKeyOrganization: b.organization,
		labelKeyRepository:   b.repository,
		labelKeyEventName:    msg.EventName,
	}
}

func (m *metricsExporter) withBaseLabels(base baseLabels) {
	m.baseLabels = base
}

func (m *metricsExporter) publishStatic(max, min int) {
	l := m.scaleSetLabels()
	maxRunners.With(l).Set(float64(max))
	minRunners.With(l).Set(float64(min))
}

func (m *metricsExporter) publishStatistics(stats *actions.RunnerScaleSetStatistic) {
	l := m.scaleSetLabels()

	// availableJobs.With(l).Set(float64(stats.TotalAvailableJobs))
	// acquiredJobs.With(l).Set(float64(stats.TotalAcquiredJobs))
	assignedJobs.With(l).Set(float64(stats.TotalAssignedJobs))
	runningJobs.With(l).Set(float64(stats.TotalRunningJobs))
	registeredRunners.With(l).Set(float64(stats.TotalRegisteredRunners))
	busyRunners.With(l).Set(float64(stats.TotalBusyRunners))
	idleRunners.With(l).Set(float64(stats.TotalIdleRunners))
}

func (m *metricsExporter) publishJobStarted(msg *actions.JobStarted) {
	l := m.startedJobLabels(msg)
	startedJobsTotal.With(l).Inc()

	l = m.jobStartupDurationLabels(msg)
	startupDuration := msg.JobMessageBase.RunnerAssignTime.Unix() - msg.JobMessageBase.ScaleSetAssignTime.Unix()
	jobStartupDurationSeconds.With(l).Observe(float64(startupDuration))
}

// func (m *metricsExporter) publishJobAssigned(msg *actions.JobAssigned) {
// 	l := m.jobLabels(&msg.JobMessageBase)
// 	queueDuration := msg.JobMessageBase.ScaleSetAssignTime.Unix() - msg.JobMessageBase.QueueTime.Unix()
// 	jobQueueDurationSeconds.With(l).Observe(float64(queueDuration))
// }

func (m *metricsExporter) publishJobCompleted(msg *actions.JobCompleted) {
	l := m.completedJobLabels(msg)
	completedJobsTotal.With(l).Inc()

	executionDuration := msg.JobMessageBase.FinishTime.Unix() - msg.JobMessageBase.RunnerAssignTime.Unix()
	jobExecutionDurationSeconds.With(l).Observe(float64(executionDuration))
}

func (m *metricsExporter) publishDesiredRunners(count int) {
	desiredRunners.With(m.scaleSetLabels()).Set(float64(count))
}
