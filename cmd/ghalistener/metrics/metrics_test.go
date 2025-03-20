package metrics

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
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
