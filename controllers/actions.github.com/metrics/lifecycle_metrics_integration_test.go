package metrics_test

import (
	"fmt"
	"sync"
	"testing"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	actionsgithubcom "github.com/actions/actions-runner-controller/controllers/actions.github.com"
	githubmetrics "github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerMetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var registerMetricsOnce sync.Once

func TestEphemeralRunnerLifecycleBucketsSetPrometheusMetrics(t *testing.T) {
	registerMetricsOnce.Do(githubmetrics.RegisterMetrics)

	now := metav1.Now()
	runners := []v1alpha1.EphemeralRunner{
		{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhasePending}},
		{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
		{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
		{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded}},
		{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed}},
		{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated}},
		{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning},
		},
	}

	buckets := actionsgithubcom.AggregateEphemeralRunnerLifecycle(runners)
	labels := githubmetrics.CommonLabels{
		Name:         "lifecycle-metrics-test",
		Namespace:    "default",
		Repository:   "test/repo",
		Organization: "test-org",
		Enterprise:   "test-enterprise",
	}

	githubmetrics.SetEphemeralRunnerCountsByLifecycle(
		labels,
		buckets.Pending,
		buckets.Running,
		buckets.Succeeded,
		buckets.Failed,
		buckets.Outdated,
		buckets.Deleting,
	)

	metricFamilies, err := controllerMetrics.Registry.Gather()
	require.NoError(t, err)

	labelSet := commonLabelSet(labels)
	assertGatheredGauge(t, metricFamilies, "gha_controller_pending_ephemeral_runners", labelSet, 1)
	assertGatheredGauge(t, metricFamilies, "gha_controller_running_ephemeral_runners", labelSet, 2)
	assertGatheredGauge(t, metricFamilies, "gha_controller_succeeded_ephemeral_runners", labelSet, 1)
	assertGatheredGauge(t, metricFamilies, "gha_controller_failed_ephemeral_runners", labelSet, 1)
	assertGatheredGauge(t, metricFamilies, "gha_controller_outdated_ephemeral_runners", labelSet, 1)
	assertGatheredGauge(t, metricFamilies, "gha_controller_deleting_ephemeral_runners", labelSet, 1)
}

func assertGatheredGauge(t *testing.T, metricFamilies []*dto.MetricFamily, metricName string, expectedLabels map[string]string, expectedValue float64) {
	t.Helper()

	for _, family := range metricFamilies {
		if family.GetName() != metricName {
			continue
		}

		for _, metric := range family.GetMetric() {
			if !metricHasLabels(metric, expectedLabels) {
				continue
			}

			require.NotNil(t, metric.Gauge, "metric %q should be a gauge", metricName)
			assert.Equal(t, expectedValue, metric.Gauge.GetValue(), "metric %q value mismatch", metricName)
			return
		}

		require.Fail(t, fmt.Sprintf("metric %q with expected labels was not gathered", metricName))
	}

	require.Fail(t, fmt.Sprintf("metric family %q was not gathered", metricName))
}

func metricHasLabels(metric *dto.Metric, expectedLabels map[string]string) bool {
	actualLabels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		actualLabels[label.GetName()] = label.GetValue()
	}

	for name, expectedValue := range expectedLabels {
		if actualLabels[name] != expectedValue {
			return false
		}
	}

	return true
}

func commonLabelSet(labels githubmetrics.CommonLabels) map[string]string {
	return map[string]string{
		"name":         labels.Name,
		"namespace":    labels.Namespace,
		"repository":   labels.Repository,
		"organization": labels.Organization,
		"enterprise":   labels.Enterprise,
	}
}
