package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
)

type Chart struct {
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

func TestTemplate_RenderedDeployment_UsesChartMetadataLabels(t *testing.T) {
	t.Parallel()

	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller-experimental")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, "gha-rs-controller-"+chart.Version, deployment.Labels["helm.sh/chart"])
	assert.Equal(t, chart.AppVersion, deployment.Labels["app.kubernetes.io/version"])
}

func TestTemplate_PprofDisabledByDefault(t *testing.T) {
	t.Parallel()

	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller-experimental")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	require.NotEmpty(t, deployment.Spec.Template.Spec.Containers, "Expected at least one container")
	managerContainer := deployment.Spec.Template.Spec.Containers[0]

	// Assert no pprof arg when default values
	for _, arg := range managerContainer.Args {
		assert.NotContains(t, arg, "--pprof-addr=", "Expected no pprof arg by default")
	}

	// Assert no pprof port when default values
	for _, port := range managerContainer.Ports {
		assert.NotEqual(t, "pprof", port.Name, "Expected no pprof port by default")
	}
}

func TestTemplate_PprofEnabledWhenConfigured(t *testing.T) {
	t.Parallel()

	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller-experimental")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
		SetValues: map[string]string{
			"controller.pprof.addr": ":6060",
		},
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	require.NotEmpty(t, deployment.Spec.Template.Spec.Containers, "Expected at least one container")
	managerContainer := deployment.Spec.Template.Spec.Containers[0]

	// Assert pprof arg is present
	foundPprofArg := false
	for _, arg := range managerContainer.Args {
		if arg == "--pprof-addr=:6060" {
			foundPprofArg = true
			break
		}
	}
	assert.True(t, foundPprofArg, "Expected --pprof-addr=:6060 arg when controller.pprof.addr is configured")

	// Assert pprof port is present
	foundPprofPort := false
	for _, port := range managerContainer.Ports {
		if port.Name == "pprof" && port.ContainerPort == 6060 && port.Protocol == "TCP" {
			foundPprofPort = true
			break
		}
	}
	assert.True(t, foundPprofPort, "Expected pprof port (6060) when controller.pprof.addr is configured")
}
