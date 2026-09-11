package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// The chart values files document the listener metrics as a commented-out
// example that is described as the default configuration. Uncommenting it must
// therefore produce the same metrics as leaving it unset.
func TestChartValuesMetricsExampleMatchesDefaults(t *testing.T) {
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

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := commentedMetricsExample(t, tc.path, tc.key)

			require.ElementsMatch(t, keys(defaultMetrics.Counters), keys(got.Counters))
			for name, want := range defaultMetrics.Counters {
				assert.ElementsMatch(t, want.Labels, got.Counters[name].Labels, "counter %q labels", name)
			}

			require.ElementsMatch(t, keys(defaultMetrics.Gauges), keys(got.Gauges))
			for name, want := range defaultMetrics.Gauges {
				assert.ElementsMatch(t, want.Labels, got.Gauges[name].Labels, "gauge %q labels", name)
			}

			require.ElementsMatch(t, keys(defaultMetrics.Histograms), keys(got.Histograms))
			for name, want := range defaultMetrics.Histograms {
				assert.ElementsMatch(t, want.Labels, got.Histograms[name].Labels, "histogram %q labels", name)
				assert.Equal(t, want.Buckets, got.Histograms[name].Buckets, "histogram %q buckets", name)
			}
		})
	}
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
