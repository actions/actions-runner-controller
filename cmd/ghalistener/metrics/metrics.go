package metrics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
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
	labelKeyJobWorkflowRef          = "job_workflow_ref"
	labelKeyJobWorkflowName         = "job_workflow_name"
	labelKeyJobWorkflowTarget       = "job_workflow_target"
	labelKeyEventName               = "event_name"
	labelKeyJobResult               = "job_result"
)

const (
	githubScaleSetSubsystem       = "gha"
	githubScaleSetSubsystemPrefix = "gha_"
)

// Names of all metrics available on the listener
const (
	MetricAssignedJobs                = "gha_assigned_jobs"
	MetricRunningJobs                 = "gha_running_jobs"
	MetricRegisteredRunners           = "gha_registered_runners"
	MetricBusyRunners                 = "gha_busy_runners"
	MetricMinRunners                  = "gha_min_runners"
	MetricMaxRunners                  = "gha_max_runners"
	MetricDesiredRunners              = "gha_desired_runners"
	MetricIdleRunners                 = "gha_idle_runners"
	MetricStartedJobsTotal            = "gha_started_jobs_total"
	MetricCompletedJobsTotal          = "gha_completed_jobs_total"
	MetricJobStartupDurationSeconds   = "gha_job_startup_duration_seconds"
	MetricJobExecutionDurationSeconds = "gha_job_execution_duration_seconds"
)

type metricsHelpRegistry struct {
	counters   map[string]string
	gauges     map[string]string
	histograms map[string]string
}

var metricsHelp = metricsHelpRegistry{
	counters: map[string]string{
		MetricStartedJobsTotal:   "Total number of jobs started.",
		MetricCompletedJobsTotal: "Total number of jobs completed.",
	},
	gauges: map[string]string{
		MetricAssignedJobs:      "Number of jobs assigned to this scale set.",
		MetricRunningJobs:       "Number of jobs running (or about to be run).",
		MetricRegisteredRunners: "Number of runners registered by the scale set.",
		MetricBusyRunners:       "Number of registered runners running a job.",
		MetricMinRunners:        "Minimum number of runners.",
		MetricMaxRunners:        "Maximum number of runners.",
		MetricDesiredRunners:    "Number of runners desired by the scale set.",
		MetricIdleRunners:       "Number of registered runners not running a job.",
	},
	histograms: map[string]string{
		MetricJobStartupDurationSeconds:   "Time spent waiting for workflow job to get started on the runner owned by the scale set (in seconds).",
		MetricJobExecutionDurationSeconds: "Time spent executing workflow jobs by the scale set (in seconds).",
	},
}

func (e *exporter) jobLabels(jobBase *actions.JobMessageBase) prometheus.Labels {
	workflowRefInfo := ParseWorkflowRef(jobBase.JobWorkflowRef)
	return prometheus.Labels{
		labelKeyEnterprise:        e.scaleSetLabels[labelKeyEnterprise],
		labelKeyOrganization:      jobBase.OwnerName,
		labelKeyRepository:        jobBase.RepositoryName,
		labelKeyJobName:           jobBase.JobDisplayName,
		labelKeyJobWorkflowRef:    jobBase.JobWorkflowRef,
		labelKeyJobWorkflowName:   workflowRefInfo.Name,
		labelKeyJobWorkflowTarget: workflowRefInfo.Target,
		labelKeyEventName:         jobBase.EventName,
	}
}

func (e *exporter) completedJobLabels(msg *actions.JobCompleted) prometheus.Labels {
	l := e.jobLabels(&msg.JobMessageBase)
	l[labelKeyJobResult] = msg.Result
	return l
}

func (e *exporter) startedJobLabels(msg *actions.JobStarted) prometheus.Labels {
	return e.jobLabels(&msg.JobMessageBase)
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
type ServerExporter interface {
	Publisher
	ListenAndServe(ctx context.Context) error
}

var (
	_ Publisher      = &discard{}
	_ ServerExporter = &exporter{}
)

var Discard Publisher = &discard{}

type exporter struct {
	logger         logr.Logger
	scaleSetLabels prometheus.Labels
	*metrics
	srv *http.Server
}

type metrics struct {
	counters   map[string]*counterMetric
	gauges     map[string]*gaugeMetric
	histograms map[string]*histogramMetric
}

type counterMetric struct {
	counter *prometheus.CounterVec
	config  *v1alpha1.CounterMetric
}

type gaugeMetric struct {
	gauge  *prometheus.GaugeVec
	config *v1alpha1.GaugeMetric
}

type histogramMetric struct {
	histogram *prometheus.HistogramVec
	config    *v1alpha1.HistogramMetric
}

type ExporterConfig struct {
	ScaleSetName      string
	ScaleSetNamespace string
	Enterprise        string
	Organization      string
	Repository        string
	ServerAddr        string
	ServerEndpoint    string
	Logger            logr.Logger
	Metrics           *v1alpha1.MetricsConfig
}

var defaultMetrics = v1alpha1.MetricsConfig{
	Counters: map[string]*v1alpha1.CounterMetric{
		MetricStartedJobsTotal: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyJobName,
				labelKeyEventName,
			},
		},
		MetricCompletedJobsTotal: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyJobName,
				labelKeyEventName,
				labelKeyJobResult,
			},
		},
	},
	Gauges: map[string]*v1alpha1.GaugeMetric{
		MetricAssignedJobs: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricRunningJobs: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricRegisteredRunners: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricBusyRunners: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricMinRunners: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricMaxRunners: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricDesiredRunners: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
		MetricIdleRunners: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyRunnerScaleSetName,
				labelKeyRunnerScaleSetNamespace,
			},
		},
	},
	Histograms: map[string]*v1alpha1.HistogramMetric{
		MetricJobStartupDurationSeconds: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyJobName,
				labelKeyEventName,
			},
			Buckets: defaultRuntimeBuckets,
		},
		MetricJobExecutionDurationSeconds: {
			Labels: []string{
				labelKeyEnterprise,
				labelKeyOrganization,
				labelKeyRepository,
				labelKeyJobName,
				labelKeyEventName,
				labelKeyJobResult,
			},
			Buckets: defaultRuntimeBuckets,
		},
	},
}

func (e *ExporterConfig) defaults() {
	if e.ServerAddr == "" {
		e.ServerAddr = ":8080"
	}
	if e.ServerEndpoint == "" {
		e.ServerEndpoint = "/metrics"
	}
	if e.Metrics == nil {
		defaultMetrics := defaultMetrics
		e.Metrics = &defaultMetrics
	}
}

func NewExporter(config ExporterConfig) ServerExporter {
	config.defaults()
	reg := prometheus.NewRegistry()

	metrics := installMetrics(*config.Metrics, reg, config.Logger)

	mux := http.NewServeMux()
	mux.Handle(
		config.ServerEndpoint,
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}),
	)

	return &exporter{
		logger: config.Logger.WithName("metrics"),
		scaleSetLabels: prometheus.Labels{
			labelKeyRunnerScaleSetName:      config.ScaleSetName,
			labelKeyRunnerScaleSetNamespace: config.ScaleSetNamespace,
			labelKeyEnterprise:              config.Enterprise,
			labelKeyOrganization:            config.Organization,
			labelKeyRepository:              config.Repository,
		},
		metrics: metrics,
		srv: &http.Server{
			Addr:    config.ServerAddr,
			Handler: mux,
		},
	}
}

var errUnknownMetricName = errors.New("unknown metric name")

func installMetrics(config v1alpha1.MetricsConfig, reg *prometheus.Registry, logger logr.Logger) *metrics {
	logger.Info(
		"Registering metrics",
		"gauges",
		config.Gauges,
		"counters",
		config.Counters,
		"histograms",
		config.Histograms,
	)
	metrics := &metrics{
		counters:   make(map[string]*counterMetric, len(config.Counters)),
		gauges:     make(map[string]*gaugeMetric, len(config.Gauges)),
		histograms: make(map[string]*histogramMetric, len(config.Histograms)),
	}
	for name, cfg := range config.Gauges {
		help, ok := metricsHelp.gauges[name]
		if !ok {
			logger.Error(errUnknownMetricName, "name", name, "kind", "gauge")
			continue
		}

		g := prometheus.V2.NewGaugeVec(prometheus.GaugeVecOpts{
			GaugeOpts: prometheus.GaugeOpts{
				Subsystem: githubScaleSetSubsystem,
				Name:      strings.TrimPrefix(name, githubScaleSetSubsystemPrefix),
				Help:      help,
			},
			VariableLabels: prometheus.UnconstrainedLabels(cfg.Labels),
		})
		reg.MustRegister(g)
		metrics.gauges[name] = &gaugeMetric{
			gauge:  g,
			config: cfg,
		}
	}

	for name, cfg := range config.Counters {
		help, ok := metricsHelp.counters[name]
		if !ok {
			logger.Error(errUnknownMetricName, "name", name, "kind", "counter")
			continue
		}
		c := prometheus.V2.NewCounterVec(prometheus.CounterVecOpts{
			CounterOpts: prometheus.CounterOpts{
				Subsystem: githubScaleSetSubsystem,
				Name:      strings.TrimPrefix(name, githubScaleSetSubsystemPrefix),
				Help:      help,
			},
			VariableLabels: prometheus.UnconstrainedLabels(cfg.Labels),
		})
		reg.MustRegister(c)
		metrics.counters[name] = &counterMetric{
			counter: c,
			config:  cfg,
		}
	}

	for name, cfg := range config.Histograms {
		help, ok := metricsHelp.histograms[name]
		if !ok {
			logger.Error(errUnknownMetricName, "name", name, "kind", "histogram")
			continue
		}

		buckets := defaultRuntimeBuckets
		if len(cfg.Buckets) > 0 {
			buckets = cfg.Buckets
		}
		h := prometheus.V2.NewHistogramVec(prometheus.HistogramVecOpts{
			HistogramOpts: prometheus.HistogramOpts{
				Subsystem: githubScaleSetSubsystem,
				Name:      strings.TrimPrefix(name, githubScaleSetSubsystemPrefix),
				Help:      help,
				Buckets:   buckets,
			},
			VariableLabels: prometheus.UnconstrainedLabels(cfg.Labels),
		})
		cfg.Buckets = buckets
		reg.MustRegister(h)
		metrics.histograms[name] = &histogramMetric{
			histogram: h,
			config:    cfg,
		}
	}

	return metrics
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

func (e *exporter) setGauge(name string, allLabels prometheus.Labels, val float64) {
	m, ok := e.gauges[name]
	if !ok {
		return
	}
	labels := make(prometheus.Labels, len(m.config.Labels))
	for _, label := range m.config.Labels {
		labels[label] = allLabels[label]
	}
	m.gauge.With(labels).Set(val)
}

func (e *exporter) incCounter(name string, allLabels prometheus.Labels) {
	m, ok := e.counters[name]
	if !ok {
		return
	}
	labels := make(prometheus.Labels, len(m.config.Labels))
	for _, label := range m.config.Labels {
		labels[label] = allLabels[label]
	}
	m.counter.With(labels).Inc()
}

func (e *exporter) observeHistogram(name string, allLabels prometheus.Labels, val float64) {
	m, ok := e.histograms[name]
	if !ok {
		return
	}
	labels := make(prometheus.Labels, len(m.config.Labels))
	for _, label := range m.config.Labels {
		labels[label] = allLabels[label]
	}
	m.histogram.With(labels).Observe(val)
}

func (e *exporter) PublishStatic(min, max int) {
	e.setGauge(MetricMaxRunners, e.scaleSetLabels, float64(max))
	e.setGauge(MetricMinRunners, e.scaleSetLabels, float64(min))
}

func (e *exporter) PublishStatistics(stats *actions.RunnerScaleSetStatistic) {
	e.setGauge(MetricAssignedJobs, e.scaleSetLabels, float64(stats.TotalAssignedJobs))
	e.setGauge(MetricRunningJobs, e.scaleSetLabels, float64(stats.TotalRunningJobs))
	e.setGauge(MetricRegisteredRunners, e.scaleSetLabels, float64(stats.TotalRegisteredRunners))
	e.setGauge(MetricBusyRunners, e.scaleSetLabels, float64(stats.TotalBusyRunners))
	e.setGauge(MetricIdleRunners, e.scaleSetLabels, float64(stats.TotalIdleRunners))
}

func (e *exporter) PublishJobStarted(msg *actions.JobStarted) {
	l := e.startedJobLabels(msg)
	e.incCounter(MetricStartedJobsTotal, l)

	startupDuration := msg.RunnerAssignTime.Unix() - msg.ScaleSetAssignTime.Unix()
	e.observeHistogram(MetricJobStartupDurationSeconds, l, float64(startupDuration))
}

func (e *exporter) PublishJobCompleted(msg *actions.JobCompleted) {
	l := e.completedJobLabels(msg)
	e.incCounter(MetricCompletedJobsTotal, l)

	executionDuration := msg.FinishTime.Unix() - msg.RunnerAssignTime.Unix()
	e.observeHistogram(MetricJobExecutionDurationSeconds, l, float64(executionDuration))
}

func (e *exporter) PublishDesiredRunners(count int) {
	e.setGauge(MetricDesiredRunners, e.scaleSetLabels, float64(count))
}

type discard struct{}

func (*discard) PublishStatic(int, int)                             {}
func (*discard) PublishStatistics(*actions.RunnerScaleSetStatistic) {}
func (*discard) PublishJobStarted(*actions.JobStarted)              {}
func (*discard) PublishJobCompleted(*actions.JobCompleted)          {}
func (*discard) PublishDesiredRunners(int)                          {}

var defaultRuntimeBuckets []float64 = []float64{
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
