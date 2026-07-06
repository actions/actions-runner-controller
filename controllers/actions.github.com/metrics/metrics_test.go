package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetEphemeralRunnerCountsByLifecycle(t *testing.T) {
	testCases := []struct {
		name      string
		labels    CommonLabels
		pending   int
		running   int
		succeeded int
		failed    int
		outdated  int
		deleting  int
	}{
		{
			name: "all zeros",
			labels: CommonLabels{
				Name:         "test-runner",
				Namespace:    "default",
				Repository:   "org/repo",
				Organization: "org",
				Enterprise:   "enterprise1",
			},
			pending:   0,
			running:   0,
			succeeded: 0,
			failed:    0,
			outdated:  0,
			deleting:  0,
		},
		{
			name: "mixed counts",
			labels: CommonLabels{
				Name:         "test-runner",
				Namespace:    "default",
				Repository:   "org/repo",
				Organization: "org",
				Enterprise:   "enterprise1",
			},
			pending:   5,
			running:   10,
			succeeded: 3,
			failed:    2,
			outdated:  1,
			deleting:  1,
		},
		{
			name: "all non-zero",
			labels: CommonLabels{
				Name:         "test-runner-2",
				Namespace:    "kube-system",
				Repository:   "other/repo",
				Organization: "otherorg",
				Enterprise:   "enterprise2",
			},
			pending:   1,
			running:   1,
			succeeded: 1,
			failed:    1,
			outdated:  1,
			deleting:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			SetEphemeralRunnerCountsByLifecycle(
				tc.labels,
				tc.pending,
				tc.running,
				tc.succeeded,
				tc.failed,
				tc.outdated,
				tc.deleting,
			)

			labelString := testutil.CollectAndCount(pendingEphemeralRunners)
			require.Greater(t, labelString, 0, "metrics should be registered")
		})
	}
}

func TestSetEphemeralRunnerCountsByLifecycleValues(t *testing.T) {
	lbls := CommonLabels{
		Name:         "precedence-test",
		Namespace:    "default",
		Repository:   "test/repo",
		Organization: "test",
		Enterprise:   "testent",
	}

	SetEphemeralRunnerCountsByLifecycle(lbls, 1, 2, 3, 4, 5, 6)

	assertGaugeValue(t, pendingEphemeralRunners, lbls, 1)
	assertGaugeValue(t, runningEphemeralRunners, lbls, 2)
	assertGaugeValue(t, succeededEphemeralRunners, lbls, 3)
	assertGaugeValue(t, failedEphemeralRunners, lbls, 4)
	assertGaugeValue(t, outdatedEphemeralRunners, lbls, 5)
	assertGaugeValue(t, deletingEphemeralRunners, lbls, 6)
}

func assertGaugeValue(t *testing.T, gauge *prometheus.GaugeVec, labels CommonLabels, expected float64) {
	t.Helper()
	assert.Equal(t, expected, testutil.ToFloat64(gauge.With(labels.labels())))
}

func TestCommonLabelsContract(t *testing.T) {
	lbls := CommonLabels{
		Name:         "test",
		Namespace:    "ns",
		Repository:   "repo",
		Organization: "org",
		Enterprise:   "ent",
	}

	promLabels := lbls.labels()

	expected := prometheus.Labels{
		"name":         "test",
		"namespace":    "ns",
		"repository":   "repo",
		"organization": "org",
		"enterprise":   "ent",
	}

	assert.Equal(t, expected, promLabels)
}
