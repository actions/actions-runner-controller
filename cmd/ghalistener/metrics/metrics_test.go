package metrics

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	got := installMetrics(metricsConfig, reg, logr.Discard())
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
			Logger:            logr.Discard(),
			Metrics:           nil, // when metrics is nil, all default metrics should be registered
		}

		exporter, ok := NewExporter(config).(*exporter)
		require.True(t, ok, "expected exporter to be of type *exporter")
		require.NotNil(t, exporter)

		reg := prometheus.NewRegistry()
		wantMetrics := installMetrics(defaultMetrics, reg, config.Logger)

		assert.Equal(t, len(wantMetrics.counters), len(exporter.metrics.counters))
		for k, v := range wantMetrics.counters {
			assert.Contains(t, exporter.metrics.counters, k)
			assert.Equal(t, v.config, exporter.metrics.counters[k].config)
		}

		assert.Equal(t, len(wantMetrics.gauges), len(exporter.metrics.gauges))
		for k, v := range wantMetrics.gauges {
			assert.Contains(t, exporter.metrics.gauges, k)
			assert.Equal(t, v.config, exporter.metrics.gauges[k].config)
		}

		assert.Equal(t, len(wantMetrics.histograms), len(exporter.metrics.histograms))
		for k, v := range wantMetrics.histograms {
			assert.Contains(t, exporter.metrics.histograms, k)
			assert.Equal(t, v.config, exporter.metrics.histograms[k].config)
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
			Logger:            logr.Discard(),
			Metrics:           nil, // when metrics is nil, all default metrics should be registered
		}

		exporter, ok := NewExporter(config).(*exporter)
		require.True(t, ok, "expected exporter to be of type *exporter")
		require.NotNil(t, exporter)

		reg := prometheus.NewRegistry()
		wantMetrics := installMetrics(defaultMetrics, reg, config.Logger)

		assert.Equal(t, len(wantMetrics.counters), len(exporter.metrics.counters))
		for k, v := range wantMetrics.counters {
			assert.Contains(t, exporter.metrics.counters, k)
			assert.Equal(t, v.config, exporter.metrics.counters[k].config)
		}

		assert.Equal(t, len(wantMetrics.gauges), len(exporter.metrics.gauges))
		for k, v := range wantMetrics.gauges {
			assert.Contains(t, exporter.metrics.gauges, k)
			assert.Equal(t, v.config, exporter.metrics.gauges[k].config)
		}

		assert.Equal(t, len(wantMetrics.histograms), len(exporter.metrics.histograms))
		for k, v := range wantMetrics.histograms {
			assert.Contains(t, exporter.metrics.histograms, k)
			assert.Equal(t, v.config, exporter.metrics.histograms[k].config)
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
			Logger:            logr.Discard(),
			Metrics:           &metricsConfig,
		}

		exporter, ok := NewExporter(config).(*exporter)
		require.True(t, ok, "expected exporter to be of type *exporter")
		require.NotNil(t, exporter)

		reg := prometheus.NewRegistry()
		wantMetrics := installMetrics(metricsConfig, reg, config.Logger)

		assert.Equal(t, len(wantMetrics.counters), len(exporter.metrics.counters))
		for k, v := range wantMetrics.counters {
			assert.Contains(t, exporter.metrics.counters, k)
			assert.Equal(t, v.config, exporter.metrics.counters[k].config)
		}

		assert.Equal(t, len(wantMetrics.gauges), len(exporter.metrics.gauges))
		for k, v := range wantMetrics.gauges {
			assert.Contains(t, exporter.metrics.gauges, k)
			assert.Equal(t, v.config, exporter.metrics.gauges[k].config)
		}

		assert.Equal(t, len(wantMetrics.histograms), len(exporter.metrics.histograms))
		for k, v := range wantMetrics.histograms {
			assert.Contains(t, exporter.metrics.histograms, k)
			assert.Equal(t, v.config, exporter.metrics.histograms[k].config)
		}

		require.NotNil(t, exporter.srv)
		assert.Equal(t, config.ServerAddr, exporter.srv.Addr)
	})
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
		Logger:            logr.Discard(),
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
		Logger:            logr.Discard(),
		Metrics:           &defaultMetrics, // when metrics is nil, all default metrics should be registered
	}

	assert.Equal(t, want, config)
}
