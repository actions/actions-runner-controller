package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	labelKeyRunnerScaleSetName      = "name"
	labelKeyRunnerScaleSetNamespace = "namespace"
	labelKeyEnterprise              = "enterprise"
	labelKeyOrganization            = "organization"
	labelKeyRepository              = "repository"
	labelKeyJobName                 = "job_name"
	labelKeyEventName               = "event_name"
	labelKeyJobResult               = "job_result"
	labelKeyRunnerPodName           = "pod_name"
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
		labelKeyEventName,
	}

	completedJobsTotalLabels       = append(jobLabels, labelKeyJobResult)
	lastJobExecutionDurationLabels = append(jobLabels, labelKeyJobResult)
	startedJobsTotalLabels         = jobLabels
	lastJobStartupDurationLabels   = jobLabels
	jobQueueDurationLabels         = jobLabels
	runnerLabels                   = append(jobLabels, labelKeyRunnerPodName)
)

var (
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

	// Becasue jobs might not run with uniform frequency calculating rates from histogram might not be suitable for all jobs.
	// With last durations we can use prometheus <aggr>_over_time functions to display the last duration of the job.
	jobLastQueueDurationSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "job_last_queue_duration_seconds",
			Help:      "Last duration spent in the queue by the job (in seconds).",
		},
		jobQueueDurationLabels,
	)

	jobLastStartupDurationSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "job_last_startup_duration_seconds",
			Help:      "The last duration spent waiting for workflow job to get started on the runner owned by the scale set (in seconds).",
		},
		lastJobStartupDurationLabels,
	)

	jobLastExecutionDurationSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "job_last_execution_duration_seconds",
			Help:      "The last duration spent executing workflow jobs by the scale set (in seconds).",
		},
		lastJobExecutionDurationLabels,
	)

	runnerJob = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetSubsystem,
			Name:      "runner_job",
			Help:      "Job information for the runner.",
		},
		runnerLabels,
	)
)

type baseLabels struct {
	scaleSetName       string
	scaleSetNamespace  string
	runnerScaleSetName string
	enterprise         string
	organization       string
	repository         string
}

func (b *baseLabels) jobLabels(jobBase *actions.JobMessageBase) prometheus.Labels {
	return prometheus.Labels{
		labelKeyRunnerScaleSetName:      b.runnerScaleSetName,
		labelKeyRunnerScaleSetNamespace: b.scaleSetNamespace,
		labelKeyEnterprise:              b.enterprise,
		labelKeyOrganization:            jobBase.OwnerName,
		labelKeyRepository:              jobBase.RepositoryName,
		labelKeyJobName:                 jobBase.JobDisplayName,
		labelKeyEventName:               jobBase.EventName,
	}
}

func (b *baseLabels) scaleSetLabels() prometheus.Labels {
	return prometheus.Labels{
		labelKeyRunnerScaleSetName:      b.runnerScaleSetName,
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

func (b *baseLabels) runnerLabels(msg *actions.JobMessageBase, runnerName string) prometheus.Labels {
	l := b.jobLabels(msg)
	l[labelKeyRunnerPodName] = runnerName
	return l
}

//go:generate mockery --name Publisher --output ./mocks --outpkg mocks --case underscore
type Publisher interface {
	PublishStatic(min, max int)
	PublishStatistics(stats *actions.RunnerScaleSetStatistic)
	PublishJobStarted(msg *actions.JobStarted)
	PublishJobCompleted(msg *actions.JobCompleted)
	PublishDesiredRunners(count int)
}

//go:generate mockery --name ServerPublisher --output ./mocks --outpkg mocks --case underscore
type ServerPublisher interface {
	Publisher
	ListenAndServe(ctx context.Context) error
}

var (
	_ Publisher       = &discard{}
	_ ServerPublisher = &exporter{}
)

var Discard Publisher = &discard{}

type exporter struct {
	logger logr.Logger
	baseLabels
	srv *http.Server
}

type ExporterConfig struct {
	ScaleSetName       string
	ScaleSetNamespace  string
	RunnerScaleSetName string
	Enterprise         string
	Organization       string
	Repository         string
	ServerAddr         string
	ServerEndpoint     string
	Logger             logr.Logger
}

func NewExporter(config ExporterConfig) ServerPublisher {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		assignedJobs,
		runningJobs,
		registeredRunners,
		busyRunners,
		minRunners,
		maxRunners,
		desiredRunners,
		idleRunners,
		startedJobsTotal,
		completedJobsTotal,
		jobLastQueueDurationSeconds,
		jobLastStartupDurationSeconds,
		jobLastExecutionDurationSeconds,
		runnerJob,
	)

	mux := http.NewServeMux()
	mux.Handle(
		config.ServerEndpoint,
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}),
	)

	return &exporter{
		logger: config.Logger.WithName("metrics"),
		baseLabels: baseLabels{
			scaleSetName:       config.ScaleSetName,
			scaleSetNamespace:  config.ScaleSetNamespace,
			runnerScaleSetName: config.RunnerScaleSetName,
			enterprise:         config.Enterprise,
			organization:       config.Organization,
			repository:         config.Repository,
		},
		srv: &http.Server{
			Addr:    config.ServerAddr,
			Handler: mux,
		},
	}
}

func (e *exporter) ListenAndServe(ctx context.Context) error {
	e.logger.Info("starting metrics server", "addr", e.srv.Addr)
	go func() {
		<-ctx.Done()
		e.logger.Info("stopping metrics server", "err", ctx.Err())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.srv.Shutdown(ctx)
	}()
	return e.srv.ListenAndServe()
}

func (m *exporter) PublishStatic(min, max int) {
	l := m.scaleSetLabels()
	maxRunners.With(l).Set(float64(max))
	minRunners.With(l).Set(float64(min))
}

func (e *exporter) PublishStatistics(stats *actions.RunnerScaleSetStatistic) {
	l := e.scaleSetLabels()

	assignedJobs.With(l).Set(float64(stats.TotalAssignedJobs))
	runningJobs.With(l).Set(float64(stats.TotalRunningJobs))
	registeredRunners.With(l).Set(float64(stats.TotalRegisteredRunners))
	busyRunners.With(l).Set(float64(stats.TotalBusyRunners))
	idleRunners.With(l).Set(float64(stats.TotalIdleRunners))
}

func (e *exporter) PublishJobStarted(msg *actions.JobStarted) {
	l := e.startedJobLabels(msg)
	startedJobsTotal.With(l).Inc()

	if !msg.JobMessageBase.RunnerAssignTime.IsZero() && !msg.JobMessageBase.ScaleSetAssignTime.IsZero() {
		startupDuration := msg.JobMessageBase.RunnerAssignTime.Unix() - msg.JobMessageBase.ScaleSetAssignTime.Unix()
		jobLastStartupDurationSeconds.With(l).Set(float64(startupDuration))
	}

	if !msg.JobMessageBase.QueueTime.IsZero() && !msg.JobMessageBase.RunnerAssignTime.IsZero() {
		queueDuration := msg.JobMessageBase.RunnerAssignTime.Unix() - msg.JobMessageBase.QueueTime.Unix()
		jobLastQueueDurationSeconds.With(l).Set(float64(queueDuration))
	}

	rl := e.runnerLabels(&msg.JobMessageBase, msg.RunnerName)
	runnerJob.With(rl).Set(1)
}

func (e *exporter) PublishJobCompleted(msg *actions.JobCompleted) {
	l := e.completedJobLabels(msg)
	completedJobsTotal.With(l).Inc()

	if !msg.JobMessageBase.FinishTime.IsZero() && !msg.JobMessageBase.RunnerAssignTime.IsZero() {
		executionDuration := msg.JobMessageBase.FinishTime.Unix() - msg.JobMessageBase.RunnerAssignTime.Unix()
		jobLastExecutionDurationSeconds.With(l).Set(float64(executionDuration))
	}

	rl := e.runnerLabels(&msg.JobMessageBase, msg.RunnerName)
	runnerJob.Delete(rl)
}

func (m *exporter) PublishDesiredRunners(count int) {
	desiredRunners.With(m.scaleSetLabels()).Set(float64(count))
}

type discard struct{}

func (*discard) PublishStatic(int, int)                             {}
func (*discard) PublishStatistics(*actions.RunnerScaleSetStatistic) {}
func (*discard) PublishJobStarted(*actions.JobStarted)              {}
func (*discard) PublishJobCompleted(*actions.JobCompleted)          {}
func (*discard) PublishDesiredRunners(int)                          {}
