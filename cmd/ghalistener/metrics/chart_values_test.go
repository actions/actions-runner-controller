package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// The chart values files document the listener metrics as a commented-out
// example. The example lists every metric and every label the exporter can
// populate, with the default buckets; the labels it marks as "not exposed by
// default" are exactly the ones missing from defaultMetrics.
func TestChartValuesMetricsExample(t *testing.T) {
	tests := map[string]struct {
		path string
		key  string
	}{
		"gha-runner-scale-set": {
			path: filepath.Join("..", "..", "..", "charts", "gha-runner-scale-set", "values.yaml"),
			key:  "listenerMetrics",
		},
		"gha-runner-scale-set-experimental": {
			path: filepath.Join("..", "..", "..", "charts", "gha-runner-scale-set-experimental", "values.yaml"),
			key:  "metrics",
		},
	}

	// Labels documented in the values files as not being part of the defaults.
	optInLabels := []string{labelKeyJobWorkflowRef, labelKeyJobWorkflowName, labelKeyJobWorkflowTarget}

	// Every label the exporter populates, per metric.
	e := &exporter{
		scaleSetLabels: prometheus.Labels{
			labelKeyRunnerScaleSetName:      "",
			labelKeyRunnerScaleSetNamespace: "",
			labelKeyEnterprise:              "",
			labelKeyOrganization:            "",
			labelKeyRepository:              "",
		},
	}
	startedLabels := keys(e.startedJobLabels(&scaleset.JobStarted{}))
	completedLabels := keys(e.completedJobLabels(&scaleset.JobCompleted{}))
	allLabels := map[string][]string{
		MetricStartedJobsTotal:            startedLabels,
		MetricCompletedJobsTotal:          completedLabels,
		MetricJobStartupDurationSeconds:   startedLabels,
		MetricJobExecutionDurationSeconds: completedLabels,
	}
	for name := range defaultMetrics.Gauges {
		allLabels[name] = keys(e.scaleSetLabels)
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := commentedMetricsExample(t, tc.path, tc.key)

			require.ElementsMatch(t, keys(defaultMetrics.Counters), keys(got.Counters))
			for name, want := range defaultMetrics.Counters {
				assertExampleLabels(t, "counter "+name, got.Counters[name].Labels, allLabels[name], want.Labels, optInLabels)
			}

			require.ElementsMatch(t, keys(defaultMetrics.Gauges), keys(got.Gauges))
			for name, want := range defaultMetrics.Gauges {
				assertExampleLabels(t, "gauge "+name, got.Gauges[name].Labels, allLabels[name], want.Labels, nil)
			}

			require.ElementsMatch(t, keys(defaultMetrics.Histograms), keys(got.Histograms))
			for name, want := range defaultMetrics.Histograms {
				assertExampleLabels(t, "histogram "+name, got.Histograms[name].Labels, allLabels[name], want.Labels, optInLabels)
				assert.Equal(t, want.Buckets, got.Histograms[name].Buckets, "histogram %q buckets", name)
			}
		})
	}
}

// assertExampleLabels checks that the example lists every label the exporter
// populates for a metric, and that the labels beyond the defaults are exactly
// the documented opt-in ones.
func assertExampleLabels(t *testing.T, metric string, example, all, defaults, optIn []string) {
	t.Helper()

	assert.ElementsMatch(t, all, example, "%s labels", metric)
	assert.Subset(t, example, defaults, "%s default labels", metric)

	var extra []string
	for _, l := range example {
		if !slices.Contains(defaults, l) {
			extra = append(extra, l)
		}
	}
	assert.ElementsMatch(t, optIn, extra, "%s labels not exposed by default", metric)
}

// commentedMetricsExample uncomments the "# <key>:" block of the values file
// and decodes it into a MetricsConfig.
func commentedMetricsExample(t *testing.T, path, key string) v1alpha1.MetricsConfig {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var (
		lines  []string
		indent string
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if lines == nil {
			if strings.TrimLeft(line, " ") != "# "+key+":" {
				continue
			}
			indent = line[:strings.Index(line, "#")]
		}
		if !strings.HasPrefix(line, indent+"#") {
			break
		}
		lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, indent+"#"), " "))
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, lines, "commented %q block not found in %s", key, path)

	var values map[string]v1alpha1.MetricsConfig
	require.NoError(t, yaml.UnmarshalStrict([]byte(strings.Join(lines, "\n")), &values))
	require.Contains(t, values, key)
	return values[key]
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
