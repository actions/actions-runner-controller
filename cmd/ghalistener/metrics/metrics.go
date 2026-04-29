package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
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

	// Capacity-monitor specific label keys.
	// Distinct value sets per metric (e.g. labelKeyPhase is
	// {pending, running, failed, succeeded, unknown} on placeholder_pods
	// but {provisioner, reporter} on the reconcile metrics) — Prometheus
	// does not enforce shared value spaces across metrics.
	labelKeyRole   = "role"
	labelKeyPhase  = "phase"
	labelKeyResult = "result"
	labelKeyReason = "reason"
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

// Capacity-monitor metrics. These are emitted by the proactive-capacity
// sub-system (see cmd/ghalistener/capacity) via the CapacityRecorder
// interface. All entries below MUST also appear in the OSDC chart's
// `listenerMetrics` block (modules/arc-runners/templates/runner.yaml.tpl)
// — `installMetrics` silently drops anything missing from that block.
// The parity test in metrics_test.go enforces this against a fixture
// in testdata/listener_metrics.yaml.
const (
	MetricCapacityProactiveCapacity                = "gha_capacity_proactive_capacity"
	MetricCapacityHUDEnabled                       = "gha_capacity_hud_enabled"
	MetricCapacityQueuedJobs                       = "gha_capacity_queued_jobs"
	MetricCapacityDesiredPairs                     = "gha_capacity_desired_pairs"
	MetricCapacityPairs                            = "gha_capacity_pairs"
	MetricCapacityRunningPairs                     = "gha_capacity_running_pairs"
	MetricCapacityPlaceholderPods                  = "gha_capacity_placeholder_pods"
	MetricCapacityAdvertisedMaxRunners             = "gha_capacity_advertised_max_runners"
	MetricCapacityReconcileLastSuccessTimestampSec = "gha_capacity_reconcile_last_success_timestamp_seconds"
	MetricCapacityReconcileDurationSeconds         = "gha_capacity_reconcile_duration_seconds"
	MetricCapacityHUDRequestDurationSeconds        = "gha_capacity_hud_request_duration_seconds"
	MetricCapacityHUDRequestsTotal                 = "gha_capacity_hud_requests_total"
	MetricCapacityPairCreatesTotal                 = "gha_capacity_pair_creates_total"
	MetricCapacityPairDeletesTotal                 = "gha_capacity_pair_deletes_total"
	MetricCapacityReconcileSkipsTotal              = "gha_capacity_reconcile_skips_total"
)

// Explicit histogram bucket sets for capacity metrics. These are picked
// for the expected latency distribution of the corresponding code paths
// — defaultRuntimeBuckets (designed for job runtimes up to 1h) wastes
// cardinality for sub-second reconcile / HTTP loops.
var (
	capacityReconcileBuckets  = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60}
	capacityHUDRequestBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30}
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

		MetricCapacityHUDRequestsTotal:    "Total HUD API call attempts by the proactive-capacity monitor, by result.",
		MetricCapacityPairCreatesTotal:    "Total runner+placeholder pair creation attempts, by result.",
		MetricCapacityPairDeletesTotal:    "Total runner+placeholder pair deletion attempts, by reason and result.",
		MetricCapacityReconcileSkipsTotal: "Total reconcile cycles aborted before completion, by reason.",
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

		MetricCapacityProactiveCapacity:                "Configured proactiveCapacity value from listener config — target number of pre-warmed runner+placeholder pairs.",
		MetricCapacityHUDEnabled:                       "1 if HUD API client + token are configured at startup, else 0. Distinguishes 'no HUD data' from 'HUD broken'.",
		MetricCapacityQueuedJobs:                       "Queued jobs from PyTorch HUD API for this scale set's labels (external queue, distinct from gha_assigned_jobs).",
		MetricCapacityDesiredPairs:                     "Number of runner+placeholder pairs desired after applying the maxRunners cap.",
		MetricCapacityPairs:                            "Total existing runner+placeholder pairs (currentPairs).",
		MetricCapacityRunningPairs:                     "Pairs where both the runner pod and placeholder pod are in Running phase.",
		MetricCapacityPlaceholderPods:                  "Number of placeholder pods by role (runner|workflow) and phase (Pending|Running|Failed|Succeeded|Unknown). The phase values match Kubernetes corev1.PodPhase.",
		MetricCapacityAdvertisedMaxRunners:             "X-ScaleSetMaxCapacity value sent to GitHub on the most recent reporter cycle.",
		MetricCapacityReconcileLastSuccessTimestampSec: "Wall-clock seconds of last successful reconcile per sub-loop. Wedge-detection signal.",
	},
	histograms: map[string]string{
		MetricJobStartupDurationSeconds:   "Time spent waiting for workflow job to get started on the runner owned by the scale set (in seconds).",
		MetricJobExecutionDurationSeconds: "Time spent executing workflow jobs by the scale set (in seconds).",

		MetricCapacityReconcileDurationSeconds:  "Reconcile loop latency in seconds, per sub-loop (provisioner|reporter).",
		MetricCapacityHUDRequestDurationSeconds: "HUD API call latency in seconds per attempt, by result.",
	},
}

func (e *exporter) jobLabels(jobBase *scaleset.JobMessageBase) prometheus.Labels {
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

func (e *exporter) completedJobLabels(msg *scaleset.JobCompleted) prometheus.Labels {
	l := e.jobLabels(&msg.JobMessageBase)
	l[labelKeyJobResult] = msg.Result
	return l
}

func (e *exporter) startedJobLabels(msg *scaleset.JobStarted) prometheus.Labels {
	return e.jobLabels(&msg.JobMessageBase)
}

type Recorder interface {
	RecordStatic(min, max int)
	RecordStatistics(stats *scaleset.RunnerScaleSetStatistic)
	RecordJobStarted(msg *scaleset.JobStarted)
	RecordJobCompleted(msg *scaleset.JobCompleted)
	RecordDesiredRunners(count int)
}

// CapacityRecorder is the metric surface used by the proactive-capacity
// monitor (see cmd/ghalistener/capacity). It is intentionally separate
// from Recorder so the listener does not have to implement capacity
// methods. Implementations: *exporter (real) and *discard (no-op).
//
// All methods are scoped to the scale set that owns the exporter; the
// exporter implementation injects the scale-set 5-tuple labels under
// the hood. Callers only supply the metric-specific extra labels.
type CapacityRecorder interface {
	SetProactiveCapacity(value int)
	SetHUDEnabled(enabled bool)
	SetQueuedJobs(value int)
	SetDesiredPairs(value int)
	SetPairs(value int)
	SetRunningPairs(value int)
	SetPlaceholderPods(role, phase string, value int)
	SetAdvertisedMaxRunners(value int)
	SetReconcileLastSuccess(phase string, t time.Time)
	ObserveReconcileDuration(phase string, d time.Duration)
	ObserveHUDRequest(result string, d time.Duration)
	IncHUDRequests(result string)
	IncPairCreates(result string)
	IncPairDeletes(reason, result string)
	IncReconcileSkips(reason string)
}

type ServerExporter interface {
	Recorder
	CapacityRecorder
	ListenAndServe(ctx context.Context) error
}

var (
	_ Recorder         = &discard{}
	_ CapacityRecorder = &discard{}
	_ ServerExporter   = &exporter{}
	_ CapacityRecorder = &exporter{}
)

var Discard Recorder = &discard{}

// DiscardCapacity is the no-op CapacityRecorder. Use as the default
// when capacity metrics are not wired up (e.g. in tests).
var DiscardCapacity CapacityRecorder = &discard{}

type exporter struct {
	logger         *slog.Logger
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
	Logger            *slog.Logger
	Metrics           *v1alpha1.MetricsConfig
}

// scaleSetTupleLabels is the 5-tuple identifying a scale set, used as
// the base label set for every capacity-monitor metric.
var scaleSetTupleLabels = []string{
	labelKeyRunnerScaleSetName,
	labelKeyRunnerScaleSetNamespace,
	labelKeyEnterprise,
	labelKeyOrganization,
	labelKeyRepository,
}

// withExtraLabels returns a new slice composed of the scale-set 5-tuple
// followed by the provided extra labels — used to build per-metric label
// lists for capacity metrics without mutating the shared slice.
func withExtraLabels(extra ...string) []string {
	out := make([]string, 0, len(scaleSetTupleLabels)+len(extra))
	out = append(out, scaleSetTupleLabels...)
	out = append(out, extra...)
	return out
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
		MetricCapacityHUDRequestsTotal: {
			Labels: withExtraLabels(labelKeyResult),
		},
		MetricCapacityPairCreatesTotal: {
			Labels: withExtraLabels(labelKeyResult),
		},
		MetricCapacityPairDeletesTotal: {
			Labels: withExtraLabels(labelKeyReason, labelKeyResult),
		},
		MetricCapacityReconcileSkipsTotal: {
			Labels: withExtraLabels(labelKeyReason),
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
		MetricCapacityProactiveCapacity: {
			Labels: withExtraLabels(),
		},
		MetricCapacityHUDEnabled: {
			Labels: withExtraLabels(),
		},
		MetricCapacityQueuedJobs: {
			Labels: withExtraLabels(),
		},
		MetricCapacityDesiredPairs: {
			Labels: withExtraLabels(),
		},
		MetricCapacityPairs: {
			Labels: withExtraLabels(),
		},
		MetricCapacityRunningPairs: {
			Labels: withExtraLabels(),
		},
		MetricCapacityPlaceholderPods: {
			Labels: withExtraLabels(labelKeyRole, labelKeyPhase),
		},
		MetricCapacityAdvertisedMaxRunners: {
			Labels: withExtraLabels(),
		},
		MetricCapacityReconcileLastSuccessTimestampSec: {
			Labels: withExtraLabels(labelKeyPhase),
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
		MetricCapacityReconcileDurationSeconds: {
			Labels:  withExtraLabels(labelKeyPhase),
			Buckets: capacityReconcileBuckets,
		},
		MetricCapacityHUDRequestDurationSeconds: {
			Labels:  withExtraLabels(labelKeyResult),
			Buckets: capacityHUDRequestBuckets,
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
		logger: config.Logger.With("component", "metrics exporter"),
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

func installMetrics(config v1alpha1.MetricsConfig, reg *prometheus.Registry, logger *slog.Logger) *metrics {
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
			logger.Error(
				"unknown metric name",
				slog.String("name", name),
				slog.String("kind", "gauge"),
			)
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
			logger.Error(
				"unknown metric name",
				slog.String("name", name),
				slog.String("kind", "counter"),
			)
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
			logger.Error(
				"unknown metric name",
				slog.String("name", name),
				slog.String("kind", "histogram"),
			)
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

func (e *exporter) RecordStatic(min, max int) {
	e.setGauge(MetricMaxRunners, e.scaleSetLabels, float64(max))
	e.setGauge(MetricMinRunners, e.scaleSetLabels, float64(min))
}

func (e *exporter) RecordStatistics(stats *scaleset.RunnerScaleSetStatistic) {
	e.setGauge(MetricAssignedJobs, e.scaleSetLabels, float64(stats.TotalAssignedJobs))
	e.setGauge(MetricRunningJobs, e.scaleSetLabels, float64(stats.TotalRunningJobs))
	e.setGauge(MetricRegisteredRunners, e.scaleSetLabels, float64(stats.TotalRegisteredRunners))
	e.setGauge(MetricBusyRunners, e.scaleSetLabels, float64(stats.TotalBusyRunners))
	e.setGauge(MetricIdleRunners, e.scaleSetLabels, float64(stats.TotalIdleRunners))
}

func (e *exporter) RecordJobStarted(msg *scaleset.JobStarted) {
	l := e.startedJobLabels(msg)
	e.incCounter(MetricStartedJobsTotal, l)

	startupDuration := msg.RunnerAssignTime.Unix() - msg.ScaleSetAssignTime.Unix()
	e.observeHistogram(MetricJobStartupDurationSeconds, l, float64(startupDuration))
}

func (e *exporter) RecordJobCompleted(msg *scaleset.JobCompleted) {
	l := e.completedJobLabels(msg)
	e.incCounter(MetricCompletedJobsTotal, l)

	executionDuration := msg.FinishTime.Unix() - msg.RunnerAssignTime.Unix()
	e.observeHistogram(MetricJobExecutionDurationSeconds, l, float64(executionDuration))
}

func (e *exporter) RecordDesiredRunners(count int) {
	e.setGauge(MetricDesiredRunners, e.scaleSetLabels, float64(count))
}

// capacityLabels returns a labels map composed of the scale-set 5-tuple
// plus the provided extra label key/value pairs (variadic, alternating).
// Caller is responsible for passing an even number of strings; a stray
// trailing key is dropped silently to keep the helper a one-liner at
// call sites (the metric registration step would already have failed
// noisily if the label set were malformed).
func (e *exporter) capacityLabels(extras ...string) prometheus.Labels {
	labels := make(prometheus.Labels, len(e.scaleSetLabels)+len(extras)/2)
	for k, v := range e.scaleSetLabels {
		labels[k] = v
	}
	for i := 0; i+1 < len(extras); i += 2 {
		labels[extras[i]] = extras[i+1]
	}
	return labels
}

func (e *exporter) SetProactiveCapacity(value int) {
	e.setGauge(MetricCapacityProactiveCapacity, e.scaleSetLabels, float64(value))
}

func (e *exporter) SetHUDEnabled(enabled bool) {
	v := 0.0
	if enabled {
		v = 1.0
	}
	e.setGauge(MetricCapacityHUDEnabled, e.scaleSetLabels, v)
}

func (e *exporter) SetQueuedJobs(value int) {
	e.setGauge(MetricCapacityQueuedJobs, e.scaleSetLabels, float64(value))
}

func (e *exporter) SetDesiredPairs(value int) {
	e.setGauge(MetricCapacityDesiredPairs, e.scaleSetLabels, float64(value))
}

func (e *exporter) SetPairs(value int) {
	e.setGauge(MetricCapacityPairs, e.scaleSetLabels, float64(value))
}

func (e *exporter) SetRunningPairs(value int) {
	e.setGauge(MetricCapacityRunningPairs, e.scaleSetLabels, float64(value))
}

func (e *exporter) SetPlaceholderPods(role, phase string, value int) {
	e.setGauge(
		MetricCapacityPlaceholderPods,
		e.capacityLabels(labelKeyRole, role, labelKeyPhase, phase),
		float64(value),
	)
}

func (e *exporter) SetAdvertisedMaxRunners(value int) {
	e.setGauge(MetricCapacityAdvertisedMaxRunners, e.scaleSetLabels, float64(value))
}

func (e *exporter) SetReconcileLastSuccess(phase string, t time.Time) {
	e.setGauge(
		MetricCapacityReconcileLastSuccessTimestampSec,
		e.capacityLabels(labelKeyPhase, phase),
		float64(t.Unix()),
	)
}

func (e *exporter) ObserveReconcileDuration(phase string, d time.Duration) {
	e.observeHistogram(
		MetricCapacityReconcileDurationSeconds,
		e.capacityLabels(labelKeyPhase, phase),
		d.Seconds(),
	)
}

func (e *exporter) ObserveHUDRequest(result string, d time.Duration) {
	e.observeHistogram(
		MetricCapacityHUDRequestDurationSeconds,
		e.capacityLabels(labelKeyResult, result),
		d.Seconds(),
	)
}

func (e *exporter) IncHUDRequests(result string) {
	e.incCounter(MetricCapacityHUDRequestsTotal, e.capacityLabels(labelKeyResult, result))
}

func (e *exporter) IncPairCreates(result string) {
	e.incCounter(MetricCapacityPairCreatesTotal, e.capacityLabels(labelKeyResult, result))
}

func (e *exporter) IncPairDeletes(reason, result string) {
	e.incCounter(
		MetricCapacityPairDeletesTotal,
		e.capacityLabels(labelKeyReason, reason, labelKeyResult, result),
	)
}

func (e *exporter) IncReconcileSkips(reason string) {
	e.incCounter(MetricCapacityReconcileSkipsTotal, e.capacityLabels(labelKeyReason, reason))
}

type discard struct{}

func (*discard) RecordStatic(int, int)                              {}
func (*discard) RecordStatistics(*scaleset.RunnerScaleSetStatistic) {}
func (*discard) RecordJobStarted(*scaleset.JobStarted)              {}
func (*discard) RecordJobCompleted(*scaleset.JobCompleted)          {}
func (*discard) RecordDesiredRunners(int)                           {}

// CapacityRecorder no-op implementations. Each method is a stub so
// callers can hold a CapacityRecorder of `&discard{}` (i.e. DiscardCapacity)
// without nil-checking before every call.
func (*discard) SetProactiveCapacity(int)                       {}
func (*discard) SetHUDEnabled(bool)                             {}
func (*discard) SetQueuedJobs(int)                              {}
func (*discard) SetDesiredPairs(int)                            {}
func (*discard) SetPairs(int)                                   {}
func (*discard) SetRunningPairs(int)                            {}
func (*discard) SetPlaceholderPods(string, string, int)         {}
func (*discard) SetAdvertisedMaxRunners(int)                    {}
func (*discard) SetReconcileLastSuccess(string, time.Time)      {}
func (*discard) ObserveReconcileDuration(string, time.Duration) {}
func (*discard) ObserveHUDRequest(string, time.Duration)        {}
func (*discard) IncHUDRequests(string)                          {}
func (*discard) IncPairCreates(string)                          {}
func (*discard) IncPairDeletes(string, string)                  {}
func (*discard) IncReconcileSkips(string)                       {}

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
