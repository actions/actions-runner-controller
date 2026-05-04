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
