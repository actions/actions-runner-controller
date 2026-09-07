package metrics

import (
	"log/slog"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func newTestExporter(t *testing.T, metricsConfig v1alpha1.MetricsConfig) (*exporter, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := installMetrics(metricsConfig, reg, discardLogger)
	e := &exporter{
		scaleSetLabels: prometheus.Labels{
			labelKeyEnterprise:              "test-enterprise",
			labelKeyOrganization:            "test-org",
			labelKeyRepository:              "test-repo",
			labelKeyRunnerScaleSetName:      "test-scale-set",
			labelKeyRunnerScaleSetNamespace: "test-namespace",
		},
		metrics: m,
	}
	return e, reg
}

func gatherMetrics(t *testing.T, reg *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	result := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		result[mf.GetName()] = mf
	}
	return result
}

func counterValue(t *testing.T, metrics map[string]*dto.MetricFamily, name string) float64 {
	t.Helper()
	mf, ok := metrics[name]
	if !ok || len(mf.GetMetric()) == 0 {
		return 0
	}
	return mf.GetMetric()[0].GetCounter().GetValue()
}

func histogramSampleCount(t *testing.T, metrics map[string]*dto.MetricFamily, name string) uint64 {
	t.Helper()
	mf, ok := metrics[name]
	if !ok || len(mf.GetMetric()) == 0 {
		return 0
	}
	return mf.GetMetric()[0].GetHistogram().GetSampleCount()
}

func TestRecordJobStarted(t *testing.T) {
	startedMetrics := v1alpha1.MetricsConfig{
		Counters: map[string]*v1alpha1.CounterMetric{
			MetricStartedJobsTotal: {
				Labels: []string{
					labelKeyEnterprise, labelKeyOrganization, labelKeyRepository,
					labelKeyJobName, labelKeyJobWorkflowRef, labelKeyJobWorkflowName,
					labelKeyJobWorkflowTarget, labelKeyEventName,
				},
			},
		},
		Histograms: map[string]*v1alpha1.HistogramMetric{
			MetricJobStartupDurationSeconds: {
				Labels: []string{
					labelKeyEnterprise, labelKeyOrganization, labelKeyRepository,
					labelKeyJobName, labelKeyJobWorkflowRef, labelKeyJobWorkflowName,
					labelKeyJobWorkflowTarget, labelKeyEventName,
				},
			},
		},
	}

	now := time.Now()

	tests := []struct {
		name             string
		msg              scaleset.JobStarted
		wantCounterValue float64
		wantSampleCount  uint64
	}{
		{
			name: "zero RunnerAssignTime and ScaleSetAssignTime",
			msg: scaleset.JobStarted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:          "myorg",
					RepositoryName:     "myrepo",
					JobDisplayName:     "build",
					JobWorkflowRef:     "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:          "push",
					RunnerAssignTime:   time.Time{},
					ScaleSetAssignTime: time.Time{},
				},
			},
			wantCounterValue: 0,
			wantSampleCount:  0,
		},
		{
			name: "zero RunnerAssignTime",
			msg: scaleset.JobStarted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:          "myorg",
					RepositoryName:     "myrepo",
					JobDisplayName:     "build",
					JobWorkflowRef:     "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:          "push",
					RunnerAssignTime:   time.Time{},
					ScaleSetAssignTime: now,
				},
			},
			wantCounterValue: 0,
			wantSampleCount:  0,
		},
		{
			name: "zero ScaleSetAssignTime",
			msg: scaleset.JobStarted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:          "myorg",
					RepositoryName:     "myrepo",
					JobDisplayName:     "build",
					JobWorkflowRef:     "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:          "push",
					RunnerAssignTime:   now,
					ScaleSetAssignTime: time.Time{},
				},
			},
			wantCounterValue: 0,
			wantSampleCount:  0,
		},
		{
			name: "RunnerAssignTime before ScaleSetAssignTime",
			msg: scaleset.JobStarted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:          "myorg",
					RepositoryName:     "myrepo",
					JobDisplayName:     "build",
					JobWorkflowRef:     "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:          "push",
					RunnerAssignTime:   now,
					ScaleSetAssignTime: now.Add(10 * time.Second),
				},
			},
			wantCounterValue: 0,
			wantSampleCount:  0,
		},
		{
			name: "valid timestamps",
			msg: scaleset.JobStarted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:          "myorg",
					RepositoryName:     "myrepo",
					JobDisplayName:     "build",
					JobWorkflowRef:     "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:          "push",
					ScaleSetAssignTime: now,
					RunnerAssignTime:   now.Add(10 * time.Second),
				},
			},
			wantCounterValue: 1,
			wantSampleCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, reg := newTestExporter(t, startedMetrics)
			e.RecordJobStarted(&tt.msg)
			metrics := gatherMetrics(t, reg)
			assert.Equal(t, tt.wantCounterValue, counterValue(t, metrics, MetricStartedJobsTotal))
			assert.Equal(t, tt.wantSampleCount, histogramSampleCount(t, metrics, MetricJobStartupDurationSeconds))
		})
	}
}

func TestRecordJobCompleted(t *testing.T) {
	completedMetrics := v1alpha1.MetricsConfig{
		Counters: map[string]*v1alpha1.CounterMetric{
			MetricCompletedJobsTotal: {
				Labels: []string{
					labelKeyEnterprise, labelKeyOrganization, labelKeyRepository,
					labelKeyJobName, labelKeyJobWorkflowRef, labelKeyJobWorkflowName,
					labelKeyJobWorkflowTarget, labelKeyEventName, labelKeyJobResult,
				},
			},
		},
		Histograms: map[string]*v1alpha1.HistogramMetric{
			MetricJobExecutionDurationSeconds: {
				Labels: []string{
					labelKeyEnterprise, labelKeyOrganization, labelKeyRepository,
					labelKeyJobName, labelKeyJobWorkflowRef, labelKeyJobWorkflowName,
					labelKeyJobWorkflowTarget, labelKeyEventName, labelKeyJobResult,
				},
			},
		},
	}

	now := time.Now()

	tests := []struct {
		name             string
		msg              scaleset.JobCompleted
		wantCounterValue float64
		wantSampleCount  uint64
	}{
		{
			name: "zero RunnerAssignTime",
			msg: scaleset.JobCompleted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:        "myorg",
					RepositoryName:   "myrepo",
					JobDisplayName:   "build",
					JobWorkflowRef:   "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:        "push",
					RunnerAssignTime: time.Time{},
					FinishTime:       now,
				},
				Result: "success",
			},
			wantCounterValue: 0,
			wantSampleCount:  0,
		},
		{
			name: "FinishTime before RunnerAssignTime",
			msg: scaleset.JobCompleted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:        "myorg",
					RepositoryName:   "myrepo",
					JobDisplayName:   "build",
					JobWorkflowRef:   "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:        "push",
					RunnerAssignTime: now.Add(20 * time.Second),
					FinishTime:       now,
				},
				Result: "success",
			},
			wantCounterValue: 0,
			wantSampleCount:  0,
		},
		{
			name: "valid timestamps",
			msg: scaleset.JobCompleted{
				JobMessageBase: scaleset.JobMessageBase{
					OwnerName:        "myorg",
					RepositoryName:   "myrepo",
					JobDisplayName:   "build",
					JobWorkflowRef:   "myorg/myrepo/.github/workflows/build.yml@refs/heads/main",
					EventName:        "push",
					RunnerAssignTime: now,
					FinishTime:       now.Add(10 * time.Second),
				},
				Result: "success",
			},
			wantCounterValue: 1,
			wantSampleCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, reg := newTestExporter(t, completedMetrics)
			e.RecordJobCompleted(&tt.msg)
			metrics := gatherMetrics(t, reg)
			assert.Equal(t, tt.wantCounterValue, counterValue(t, metrics, MetricCompletedJobsTotal))
			assert.Equal(t, tt.wantSampleCount, histogramSampleCount(t, metrics, MetricJobExecutionDurationSeconds))
		})
	}
}
