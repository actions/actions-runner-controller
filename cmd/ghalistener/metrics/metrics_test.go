package metrics

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

var discardLogger = slog.New(slog.DiscardHandler)

func TestInstallMetrics(t *testing.T) {
	metricsConfig := v1alpha1.MetricsConfig{
		Counters: map[string]*v1alpha1.CounterMetric{
			// unknown metric shouldn't be registered
			"gha_unknown": {
				Labels: []string{labelKeyRepository},
			},
			// gauge metric shouldn't be registered from this section
			MetricAssignedJobs: {
				Labels: []string{labelKeyRepository},
			},
			// histogram metric shouldn't be registered from this section
			MetricJobStartupDurationSeconds: {
				Labels: []string{labelKeyRepository},
			},
			// counter metric should be registered
			MetricStartedJobsTotal: {
				Labels: []string{labelKeyRepository},
			},
		},
		Gauges: map[string]*v1alpha1.GaugeMetric{
			// unknown metric shouldn't be registered
			"gha_unknown": {
				Labels: []string{labelKeyRepository},
			},
			// counter metric shouldn't be registered from this section
			MetricStartedJobsTotal: {
				Labels: []string{labelKeyRepository},
			},
			// histogram metric shouldn't be registered from this section
			MetricJobStartupDurationSeconds: {
				Labels: []string{labelKeyRepository},
			},
			// gauge metric should be registered
			MetricAssignedJobs: {
				Labels: []string{labelKeyRepository},
			},
		},
		Histograms: map[string]*v1alpha1.HistogramMetric{
			// unknown metric shouldn't be registered
			"gha_unknown": {
				Labels: []string{labelKeyRepository},
			},
			// counter metric shouldn't be registered from this section
			MetricStartedJobsTotal: {
				Labels: []string{labelKeyRepository},
			},
			// gauge metric shouldn't be registered from this section
			MetricAssignedJobs: {
				Labels: []string{labelKeyRepository},
			},
			// histogram metric should be registered
			MetricJobExecutionDurationSeconds: {
				Labels:  []string{labelKeyRepository},
				Buckets: []float64{0.1, 1},
			},
			// histogram metric should be registered with default runtime buckets
			MetricJobStartupDurationSeconds: {
				Labels: []string{labelKeyRepository},
			},
		},
	}
	reg := prometheus.NewRegistry()

	got := installMetrics(metricsConfig, reg, discardLogger)
	assert.Len(t, got.counters, 1)
	assert.Len(t, got.gauges, 1)
	assert.Len(t, got.histograms, 2)

	assert.Equal(t, got.counters[MetricStartedJobsTotal].config, metricsConfig.Counters[MetricStartedJobsTotal])
	assert.Equal(t, got.gauges[MetricAssignedJobs].config, metricsConfig.Gauges[MetricAssignedJobs])
	assert.Equal(t, got.histograms[MetricJobExecutionDurationSeconds].config, metricsConfig.Histograms[MetricJobExecutionDurationSeconds])

	duration := got.histograms[MetricJobStartupDurationSeconds]
	assert.Equal(t, duration.config.Labels, metricsConfig.Histograms[MetricJobStartupDurationSeconds].Labels)
	assert.Equal(t, duration.config.Buckets, defaultRuntimeBuckets)
}

func TestNewExporter(t *testing.T) {
	t.Run("with defaults metrics applied", func(t *testing.T) {
		config := ExporterConfig{
			ScaleSetName:      "test-scale-set",
			ScaleSetNamespace: "test-namespace",
			Enterprise:        "",
			Organization:      "org",
			Repository:        "repo",
			ServerAddr:        ":6060",
			ServerEndpoint:    "/metrics",
			Logger:            discardLogger,
			Metrics:           nil, // when metrics is nil, all default metrics should be registered
		}

		exporter, ok := NewExporter(config).(*exporter)
		require.True(t, ok, "expected exporter to be of type *exporter")
		require.NotNil(t, exporter)

		reg := prometheus.NewRegistry()
		wantMetrics := installMetrics(defaultMetrics, reg, config.Logger)

		assert.Equal(t, len(wantMetrics.counters), len(exporter.counters))
		for k, v := range wantMetrics.counters {
			assert.Contains(t, exporter.counters, k)
			assert.Equal(t, v.config, exporter.counters[k].config)
		}

		assert.Equal(t, len(wantMetrics.gauges), len(exporter.gauges))
		for k, v := range wantMetrics.gauges {
			assert.Contains(t, exporter.gauges, k)
			assert.Equal(t, v.config, exporter.gauges[k].config)
		}

		assert.Equal(t, len(wantMetrics.histograms), len(exporter.histograms))
		for k, v := range wantMetrics.histograms {
			assert.Contains(t, exporter.histograms, k)
			assert.Equal(t, v.config, exporter.histograms[k].config)
		}

		require.NotNil(t, exporter.srv)
		assert.Equal(t, config.ServerAddr, exporter.srv.Addr)
	})

	t.Run("with default server URL", func(t *testing.T) {
		config := ExporterConfig{
			ScaleSetName:      "test-scale-set",
			ScaleSetNamespace: "test-namespace",
			Enterprise:        "",
			Organization:      "org",
			Repository:        "repo",
			ServerAddr:        "", // empty ServerAddr should default to ":8080"
			ServerEndpoint:    "",
			Logger:            discardLogger,
			Metrics:           nil, // when metrics is nil, all default metrics should be registered
		}

		exporter, ok := NewExporter(config).(*exporter)
		require.True(t, ok, "expected exporter to be of type *exporter")
		require.NotNil(t, exporter)

		reg := prometheus.NewRegistry()
		wantMetrics := installMetrics(defaultMetrics, reg, config.Logger)

		assert.Equal(t, len(wantMetrics.counters), len(exporter.counters))
		for k, v := range wantMetrics.counters {
			assert.Contains(t, exporter.counters, k)
			assert.Equal(t, v.config, exporter.counters[k].config)
		}

		assert.Equal(t, len(wantMetrics.gauges), len(exporter.gauges))
		for k, v := range wantMetrics.gauges {
			assert.Contains(t, exporter.gauges, k)
			assert.Equal(t, v.config, exporter.gauges[k].config)
		}

		assert.Equal(t, len(wantMetrics.histograms), len(exporter.histograms))
		for k, v := range wantMetrics.histograms {
			assert.Contains(t, exporter.histograms, k)
			assert.Equal(t, v.config, exporter.histograms[k].config)
		}

		require.NotNil(t, exporter.srv)
		assert.Equal(t, exporter.srv.Addr, ":8080")
	})

	t.Run("with metrics configured", func(t *testing.T) {
		metricsConfig := v1alpha1.MetricsConfig{
			Counters: map[string]*v1alpha1.CounterMetric{
				MetricStartedJobsTotal: {
					Labels: []string{labelKeyRepository},
				},
			},
			Gauges: map[string]*v1alpha1.GaugeMetric{
				MetricAssignedJobs: {
					Labels: []string{labelKeyRepository},
				},
			},
			Histograms: map[string]*v1alpha1.HistogramMetric{
				MetricJobExecutionDurationSeconds: {
					Labels:  []string{labelKeyRepository},
					Buckets: []float64{0.1, 1},
				},
			},
		}

		config := ExporterConfig{
			ScaleSetName:      "test-scale-set",
			ScaleSetNamespace: "test-namespace",
			Enterprise:        "",
			Organization:      "org",
			Repository:        "repo",
			ServerAddr:        ":6060",
			ServerEndpoint:    "/metrics",
			Logger:            discardLogger,
			Metrics:           &metricsConfig,
		}

		exporter, ok := NewExporter(config).(*exporter)
		require.True(t, ok, "expected exporter to be of type *exporter")
		require.NotNil(t, exporter)

		reg := prometheus.NewRegistry()
		wantMetrics := installMetrics(metricsConfig, reg, config.Logger)

		assert.Equal(t, len(wantMetrics.counters), len(exporter.counters))
		for k, v := range wantMetrics.counters {
			assert.Contains(t, exporter.counters, k)
			assert.Equal(t, v.config, exporter.counters[k].config)
		}

		assert.Equal(t, len(wantMetrics.gauges), len(exporter.gauges))
		for k, v := range wantMetrics.gauges {
			assert.Contains(t, exporter.gauges, k)
			assert.Equal(t, v.config, exporter.gauges[k].config)
		}

		assert.Equal(t, len(wantMetrics.histograms), len(exporter.histograms))
		for k, v := range wantMetrics.histograms {
			assert.Contains(t, exporter.histograms, k)
			assert.Equal(t, v.config, exporter.histograms[k].config)
		}

		require.NotNil(t, exporter.srv)
		assert.Equal(t, config.ServerAddr, exporter.srv.Addr)
	})
}

// newCapacityTestExporter constructs an *exporter with the full
// defaultMetrics set registered against a fresh registry. This is the
// fixture for unit-testing capacity-recorder methods — it gives us the
// exact same setup as production NewExporter without binding a server
// socket.
func newCapacityTestExporter(t *testing.T) *exporter {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := installMetrics(defaultMetrics, reg, discardLogger)
	return &exporter{
		logger: discardLogger,
		scaleSetLabels: prometheus.Labels{
			labelKeyRunnerScaleSetName:      "scale-set-x",
			labelKeyRunnerScaleSetNamespace: "ns-x",
			labelKeyEnterprise:              "ent",
			labelKeyOrganization:            "org",
			labelKeyRepository:              "repo",
		},
		metrics: m,
	}
}

// gaugeValue extracts the current value of a single-series gauge
// (the metric must have been Set exactly once with `extras` matching
// the registered label set on top of the scale-set 5-tuple).
func gaugeValue(t *testing.T, e *exporter, name string, extras ...string) float64 {
	t.Helper()
	m, ok := e.gauges[name]
	require.True(t, ok, "gauge %q not registered", name)
	labels := e.capacityLabels(extras...)
	want := make(prometheus.Labels, len(m.config.Labels))
	for _, k := range m.config.Labels {
		want[k] = labels[k]
	}
	pb := &dto.Metric{}
	require.NoError(t, m.gauge.With(want).Write(pb))
	require.NotNil(t, pb.Gauge, "gauge %q has no value", name)
	return pb.Gauge.GetValue()
}

func counterValue(t *testing.T, e *exporter, name string, extras ...string) float64 {
	t.Helper()
	m, ok := e.counters[name]
	require.True(t, ok, "counter %q not registered", name)
	labels := e.capacityLabels(extras...)
	want := make(prometheus.Labels, len(m.config.Labels))
	for _, k := range m.config.Labels {
		want[k] = labels[k]
	}
	pb := &dto.Metric{}
	require.NoError(t, m.counter.With(want).Write(pb))
	require.NotNil(t, pb.Counter, "counter %q has no value", name)
	return pb.Counter.GetValue()
}

func histogramSampleCount(t *testing.T, e *exporter, name string, extras ...string) uint64 {
	t.Helper()
	m, ok := e.histograms[name]
	require.True(t, ok, "histogram %q not registered", name)
	labels := e.capacityLabels(extras...)
	want := make(prometheus.Labels, len(m.config.Labels))
	for _, k := range m.config.Labels {
		want[k] = labels[k]
	}
	obs, err := m.histogram.GetMetricWith(want)
	require.NoError(t, err)
	pb := &dto.Metric{}
	require.NoError(t, obs.(prometheus.Histogram).Write(pb))
	require.NotNil(t, pb.Histogram, "histogram %q has no value", name)
	return pb.Histogram.GetSampleCount()
}

// histogramBuckets returns the configured bucket boundaries for a
// histogram, by inspecting an observed (zero-sample-count is fine)
// metric protobuf. Used to assert plan-mandated bucket sets.
func histogramBuckets(t *testing.T, e *exporter, name string, extras ...string) []float64 {
	t.Helper()
	m, ok := e.histograms[name]
	require.True(t, ok, "histogram %q not registered", name)
	labels := e.capacityLabels(extras...)
	want := make(prometheus.Labels, len(m.config.Labels))
	for _, k := range m.config.Labels {
		want[k] = labels[k]
	}
	obs, err := m.histogram.GetMetricWith(want)
	require.NoError(t, err)
	pb := &dto.Metric{}
	require.NoError(t, obs.(prometheus.Histogram).Write(pb))
	require.NotNil(t, pb.Histogram, "histogram %q has no value", name)
	out := make([]float64, 0, len(pb.Histogram.Bucket))
	for _, b := range pb.Histogram.Bucket {
		out = append(out, b.GetUpperBound())
	}
	return out
}

func TestExporterCapacityRecorder_Gauges(t *testing.T) {
	e := newCapacityTestExporter(t)

	e.SetProactiveCapacity(7)
	assert.Equal(t, 7.0, gaugeValue(t, e, MetricCapacityProactiveCapacity))

	e.SetHUDEnabled(true)
	assert.Equal(t, 1.0, gaugeValue(t, e, MetricCapacityHUDEnabled))
	e.SetHUDEnabled(false)
	assert.Equal(t, 0.0, gaugeValue(t, e, MetricCapacityHUDEnabled))

	e.SetQueuedJobs(13)
	assert.Equal(t, 13.0, gaugeValue(t, e, MetricCapacityQueuedJobs))

	e.SetDesiredPairs(4)
	assert.Equal(t, 4.0, gaugeValue(t, e, MetricCapacityDesiredPairs))

	e.SetPairs(5)
	assert.Equal(t, 5.0, gaugeValue(t, e, MetricCapacityPairs))

	e.SetRunningPairs(3)
	assert.Equal(t, 3.0, gaugeValue(t, e, MetricCapacityRunningPairs))

	e.SetAdvertisedMaxRunners(99)
	assert.Equal(t, 99.0, gaugeValue(t, e, MetricCapacityAdvertisedMaxRunners))

	now := time.Unix(1_700_000_000, 0)
	e.SetReconcileLastSuccess("provisioner", now)
	assert.Equal(t,
		float64(now.Unix()),
		gaugeValue(t, e, MetricCapacityReconcileLastSuccessTimestampSec, labelKeyPhase, "provisioner"),
	)
	then := time.Unix(1_700_000_500, 0)
	e.SetReconcileLastSuccess("reporter", then)
	assert.Equal(t,
		float64(then.Unix()),
		gaugeValue(t, e, MetricCapacityReconcileLastSuccessTimestampSec, labelKeyPhase, "reporter"),
	)
}

func TestExporterCapacityRecorder_PlaceholderPods(t *testing.T) {
	e := newCapacityTestExporter(t)

	// Each (role, phase) tuple should produce an isolated series.
	cases := []struct {
		role, phase string
		val         int
	}{
		{"runner", "pending", 1},
		{"runner", "running", 2},
		{"runner", "failed", 3},
		{"runner", "succeeded", 4},
		{"runner", "unknown", 5},
		{"workflow", "pending", 6},
		{"workflow", "running", 7},
		{"workflow", "failed", 8},
		{"workflow", "succeeded", 9},
		{"workflow", "unknown", 10},
	}
	for _, c := range cases {
		e.SetPlaceholderPods(c.role, c.phase, c.val)
	}
	for _, c := range cases {
		got := gaugeValue(t, e,
			MetricCapacityPlaceholderPods,
			labelKeyRole, c.role, labelKeyPhase, c.phase,
		)
		assert.Equalf(t, float64(c.val), got,
			"placeholder_pods{role=%s,phase=%s}", c.role, c.phase)
	}
}

func TestExporterCapacityRecorder_Counters(t *testing.T) {
	e := newCapacityTestExporter(t)

	e.IncHUDRequests("success")
	e.IncHUDRequests("success")
	e.IncHUDRequests("error")
	assert.Equal(t, 2.0, counterValue(t, e, MetricCapacityHUDRequestsTotal, labelKeyResult, "success"))
	assert.Equal(t, 1.0, counterValue(t, e, MetricCapacityHUDRequestsTotal, labelKeyResult, "error"))

	e.IncPairCreates("success")
	e.IncPairCreates("error")
	e.IncPairCreates("success")
	assert.Equal(t, 2.0, counterValue(t, e, MetricCapacityPairCreatesTotal, labelKeyResult, "success"))
	assert.Equal(t, 1.0, counterValue(t, e, MetricCapacityPairCreatesTotal, labelKeyResult, "error"))

	for _, reason := range []string{"timeout", "orphan", "excess"} {
		e.IncPairDeletes(reason, "success")
		e.IncPairDeletes(reason, "error")
		e.IncPairDeletes(reason, "success")
		assert.Equalf(t, 2.0,
			counterValue(t, e, MetricCapacityPairDeletesTotal, labelKeyReason, reason, labelKeyResult, "success"),
			"pair_deletes{reason=%s,result=success}", reason)
		assert.Equalf(t, 1.0,
			counterValue(t, e, MetricCapacityPairDeletesTotal, labelKeyReason, reason, labelKeyResult, "error"),
			"pair_deletes{reason=%s,result=error}", reason)
	}

	for _, reason := range []string{
		"provisioner_list_pairs",
		"reporter_list_pairs",
		"reporter_count_runners",
		"hud_api_failed",
	} {
		e.IncReconcileSkips(reason)
		e.IncReconcileSkips(reason)
		assert.Equalf(t, 2.0,
			counterValue(t, e, MetricCapacityReconcileSkipsTotal, labelKeyReason, reason),
			"reconcile_skips{reason=%s}", reason)
	}
}

func TestExporterCapacityRecorder_Histograms(t *testing.T) {
	e := newCapacityTestExporter(t)

	e.ObserveReconcileDuration("provisioner", 250*time.Millisecond)
	e.ObserveReconcileDuration("provisioner", 1500*time.Millisecond)
	e.ObserveReconcileDuration("reporter", 50*time.Millisecond)
	assert.Equal(t, uint64(2),
		histogramSampleCount(t, e, MetricCapacityReconcileDurationSeconds, labelKeyPhase, "provisioner"))
	assert.Equal(t, uint64(1),
		histogramSampleCount(t, e, MetricCapacityReconcileDurationSeconds, labelKeyPhase, "reporter"))
	assert.Equal(t,
		[]float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		histogramBuckets(t, e, MetricCapacityReconcileDurationSeconds, labelKeyPhase, "provisioner"),
	)

	e.ObserveHUDRequest("success", 75*time.Millisecond)
	e.ObserveHUDRequest("error", 5*time.Second)
	assert.Equal(t, uint64(1),
		histogramSampleCount(t, e, MetricCapacityHUDRequestDurationSeconds, labelKeyResult, "success"))
	assert.Equal(t, uint64(1),
		histogramSampleCount(t, e, MetricCapacityHUDRequestDurationSeconds, labelKeyResult, "error"))
	assert.Equal(t,
		[]float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		histogramBuckets(t, e, MetricCapacityHUDRequestDurationSeconds, labelKeyResult, "success"),
	)
}

// TestDiscardCapacity_NoOpAndAlive asserts every method on the no-op
// implementation is callable without panicking and that DiscardCapacity
// is a non-nil package-level CapacityRecorder.
func TestDiscardCapacity_NoOpAndAlive(t *testing.T) {
	require.NotNil(t, DiscardCapacity)
	d := DiscardCapacity
	// Each method must return without panic.
	assert.NotPanics(t, func() { d.SetProactiveCapacity(1) })
	assert.NotPanics(t, func() { d.SetHUDEnabled(true) })
	assert.NotPanics(t, func() { d.SetHUDEnabled(false) })
	assert.NotPanics(t, func() { d.SetQueuedJobs(2) })
	assert.NotPanics(t, func() { d.SetDesiredPairs(3) })
	assert.NotPanics(t, func() { d.SetPairs(4) })
	assert.NotPanics(t, func() { d.SetRunningPairs(5) })
	assert.NotPanics(t, func() { d.SetPlaceholderPods("runner", "pending", 6) })
	assert.NotPanics(t, func() { d.SetAdvertisedMaxRunners(7) })
	assert.NotPanics(t, func() { d.SetReconcileLastSuccess("provisioner", time.Now()) })
	assert.NotPanics(t, func() { d.ObserveReconcileDuration("provisioner", time.Second) })
	assert.NotPanics(t, func() { d.ObserveHUDRequest("success", time.Second) })
	assert.NotPanics(t, func() { d.IncHUDRequests("success") })
	assert.NotPanics(t, func() { d.IncPairCreates("success") })
	assert.NotPanics(t, func() { d.IncPairDeletes("timeout", "success") })
	assert.NotPanics(t, func() { d.IncReconcileSkips("hud_api_failed") })
}

// listenerMetricsFixture mirrors the YAML structure of the OSDC chart's
// listenerMetrics block. Per-metric value type is intentionally
// permissive (only key presence matters for the parity test).
type listenerMetricsFixture struct {
	ListenerMetrics struct {
		Counters   map[string]any `json:"counters"`
		Gauges     map[string]any `json:"gauges"`
		Histograms map[string]any `json:"histograms"`
	} `json:"listenerMetrics"`
}

// TestListenerMetricsParity asserts every metric registered in the Go
// `metricsHelp` registry is present in the deployer's listenerMetrics
// allowlist (and vice versa). The deployer silently drops anything not
// in its allowlist, so this test catches drift between the two.
//
// This fixture mirrors the deployer's listenerMetrics allowlist; keep in
// sync with whatever chart/config consumes the metrics.
func TestListenerMetricsParity(t *testing.T) {
	path := filepath.Join("testdata", "listener_metrics.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "fixture %s not readable", path)

	var fx listenerMetricsFixture
	require.NoError(t, yaml.Unmarshal(raw, &fx), "fixture %s not parseable as YAML", path)

	type kindCheck struct {
		kind         string
		fixtureNames map[string]any
		helpNames    map[string]string
	}
	cases := []kindCheck{
		{kind: "counter", fixtureNames: fx.ListenerMetrics.Counters, helpNames: metricsHelp.counters},
		{kind: "gauge", fixtureNames: fx.ListenerMetrics.Gauges, helpNames: metricsHelp.gauges},
		{kind: "histogram", fixtureNames: fx.ListenerMetrics.Histograms, helpNames: metricsHelp.histograms},
	}

	for _, c := range cases {
		// Every Go-registered metric must appear in the chart fixture.
		for name := range c.helpNames {
			_, ok := c.fixtureNames[name]
			assert.Truef(t, ok,
				"%s %q is in metricsHelp but missing from the OSDC chart "+
					"listenerMetrics allowlist (testdata/listener_metrics.yaml). "+
					"Add it to runner.yaml.tpl listenerMetrics.%ss or the chart "+
					"will silently drop it in production.",
				c.kind, name, c.kind)
		}
		// Every chart-listed metric must appear in metricsHelp (otherwise
		// installMetrics will skip it with an "unknown metric name" log).
		for name := range c.fixtureNames {
			_, ok := c.helpNames[name]
			assert.Truef(t, ok,
				"%s %q is in the OSDC chart listenerMetrics allowlist but "+
					"missing from metricsHelp.%ss. Either remove it from the "+
					"chart or add it to metrics.go (and defaultMetrics).",
				c.kind, name, c.kind)
		}
	}
}

func TestExporterConfigDefaults(t *testing.T) {
	config := ExporterConfig{
		ScaleSetName:      "test-scale-set",
		ScaleSetNamespace: "test-namespace",
		Enterprise:        "",
		Organization:      "org",
		Repository:        "repo",
		ServerAddr:        "",
		ServerEndpoint:    "",
		Logger:            discardLogger,
		Metrics:           nil, // when metrics is nil, all default metrics should be registered
	}

	config.defaults()
	want := ExporterConfig{
		ScaleSetName:      "test-scale-set",
		ScaleSetNamespace: "test-namespace",
		Enterprise:        "",
		Organization:      "org",
		Repository:        "repo",
		ServerAddr:        ":8080",    // default server address
		ServerEndpoint:    "/metrics", // default server endpoint
		Logger:            discardLogger,
		Metrics:           &defaultMetrics, // when metrics is nil, all default metrics should be registered
	}

	assert.Equal(t, want, config)
}
