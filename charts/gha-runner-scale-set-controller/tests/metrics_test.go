package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func TestControllerMetricsAddress(t *testing.T) {
	for _, chartName := range []string{"gha-runner-scale-set-controller", "gha-runner-scale-set-controller-experimental"} {
		t.Run(chartName, func(t *testing.T) {
			chart, err := filepath.Abs("../../" + chartName)
			require.NoError(t, err)
			for _, tc := range []struct {
				address string
				port    int32
			}{
				{":8080", 8080}, {"127.0.0.1:8080", 8080}, {"[::]:8080", 8080},
				{"[2001:db8::1]:9090", 9090}, {"[::ffff:127.0.0.1]:8080", 8080},
				{"[fe80::1%eth0]:8080", 8080}, {"localhost:8080", 8080},
				{"metrics.example.com:8080", 8080}, {":1", 1}, {":65535", 65535}, {"0", 0},
				{"", 0},
			} {
				t.Run(tc.address, func(t *testing.T) {
					values := ""
					if tc.address != "" {
						values = fmt.Sprintf("metrics:\n  controllerManagerAddr: %q\n  listenerAddr: ':9090'\n  listenerEndpoint: /metrics\n", tc.address)
						if chartName == "gha-runner-scale-set-controller-experimental" {
							values = "controller:\n" + indentMetricsValues(values)
						}
					}
					path := filepath.Join(t.TempDir(), "values.yaml")
					require.NoError(t, os.WriteFile(path, []byte(values), 0600))
					output := helm.RenderTemplateContext(t, t.Context(), &helm.Options{
						Logger: logger.Discard, ValuesFiles: []string{path},
					}, chart, "metrics", []string{"templates/deployment.yaml"})
					var deployment appsv1.Deployment
					require.NoError(t, yaml.UnmarshalStrict([]byte(output), &deployment))
					c := deployment.Spec.Template.Spec.Containers[0]
					if tc.port == 0 {
						assert.Empty(t, c.Ports)
						assert.Contains(t, c.Args, "--metrics-addr=0")
					} else {
						assert.Equal(t, []corev1.ContainerPort{{Name: "metrics", ContainerPort: tc.port, Protocol: corev1.ProtocolTCP}}, c.Ports)
						assert.Contains(t, c.Args, "--metrics-addr="+tc.address)
					}
					if tc.address != "" {
						assert.Contains(t, c.Args, "--listener-metrics-addr=:9090")
						assert.Contains(t, c.Args, "--listener-metrics-endpoint=/metrics")
					}
				})
			}
		})
	}
}

func indentMetricsValues(values string) string {
	result := ""
	for line := range strings.SplitSeq(strings.TrimSuffix(values, "\n"), "\n") {
		result += "  " + line + "\n"
	}
	return result
}

func TestExperimentalControllerRejectsInvalidMetricsAddress(t *testing.T) {
	chart, err := filepath.Abs("../../gha-runner-scale-set-controller-experimental")
	require.NoError(t, err)
	for _, address := range []string{
		"", "8080", ":0", ":65536", ":-1", ":http", ":1.5", ":999999999999999999999",
		"127.0.0.1", "http://localhost:8080", "::1:8080", "[::]:http", "[::1:8080",
		"[not-an-ip]:8080", "[::::]:8080", "[1:2:3]:8080", "[:1::]:8080", "[::1:]:8080",
		"[::1::2]:8080", "[1:2:3:4:5:6:7:8::]:8080", "host name:8080", "bad/host:8080",
		"999.999.999.999:8080",
	} {
		t.Run(address, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "values.yaml")
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("controller:\n  metrics:\n    controllerManagerAddr: %q\n    listenerAddr: ':9090'\n    listenerEndpoint: /metrics\n", address)), 0600))
			_, err := helm.RenderTemplateContextE(t, t.Context(), &helm.Options{
				Logger: logger.Discard, ValuesFiles: []string{path},
			}, chart, "metrics", []string{"templates/deployment.yaml"})
			require.ErrorContains(t, err, "controller.metrics.controllerManagerAddr")
		})
	}
}

func TestExperimentalControllerMetricsDecimalPort(t *testing.T) {
	chart, err := filepath.Abs("../../gha-runner-scale-set-controller-experimental")
	require.NoError(t, err)
	for address, port := range map[string]int32{":08080": 8080, "localhost:008080": 8080, ":010": 10} {
		t.Run(address, func(t *testing.T) {
			output := helm.RenderTemplateContext(t, t.Context(), &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"controller.metrics.controllerManagerAddr": address,
					"controller.metrics.listenerAddr":          ":9090",
					"controller.metrics.listenerEndpoint":      "/metrics",
				},
			}, chart, "metrics", []string{"templates/deployment.yaml"})
			var deployment appsv1.Deployment
			require.NoError(t, yaml.UnmarshalStrict([]byte(output), &deployment))
			assert.Equal(t, port, deployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
			assert.Contains(t, deployment.Spec.Template.Spec.Containers[0].Args, "--metrics-addr="+address)
		})
	}
}

func TestControllerMetricsDisabledKeepsPprof(t *testing.T) {
	chart, err := filepath.Abs("..")
	require.NoError(t, err)
	output := helm.RenderTemplateContext(t, t.Context(), &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"metrics.controllerManagerAddr": "0",
			"metrics.listenerAddr":          ":9090",
			"metrics.listenerEndpoint":      "/metrics",
			"pprof.addr":                    ":6060",
		},
	}, chart, "metrics", []string{"templates/deployment.yaml"})
	var deployment appsv1.Deployment
	require.NoError(t, yaml.UnmarshalStrict([]byte(output), &deployment))
	c := deployment.Spec.Template.Spec.Containers[0]
	assert.Equal(t, []corev1.ContainerPort{{Name: "pprof", ContainerPort: 6060, Protocol: corev1.ProtocolTCP}}, c.Ports)
	assert.Contains(t, c.Args, "--metrics-addr=0")
	assert.Contains(t, c.Args, "--listener-metrics-addr=:9090")
	assert.Contains(t, c.Args, "--pprof-addr=:6060")
}
