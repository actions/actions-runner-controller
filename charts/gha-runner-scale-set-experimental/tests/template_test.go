package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

type Chart struct {
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

func TestTemplate_RenderedAutoscalingRunnerSet_UsesChartMetadataLabels(t *testing.T) {
	t.Parallel()

	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-experimental")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"scaleset.name":                      "test",
			"auth.url":                           "https://github.com/actions",
			"auth.githubToken":                   "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnserset.yaml"})

	var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &autoscalingRunnerSet)

	assert.Equal(t, "gha-rs-"+chart.Version, autoscalingRunnerSet.Labels["helm.sh/chart"])
	assert.Equal(t, chart.AppVersion, autoscalingRunnerSet.Labels["app.kubernetes.io/version"])
	assert.Equal(t, "gha-rs-"+chart.Version, autoscalingRunnerSet.Spec.Template.Labels["helm.sh/chart"])
	assert.Equal(t, chart.AppVersion, autoscalingRunnerSet.Spec.Template.Labels["app.kubernetes.io/version"])
}
