package tests

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	actionsgithubcom "github.com/actions/actions-runner-controller/controllers/actions.github.com"
	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func TestTemplateRenderedGitHubSecretWithGitHubToken(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/githubsecret.yaml"})

	var githubSecret corev1.Secret
	helm.UnmarshalK8SYaml(t, output, &githubSecret)

	assert.Equal(t, namespaceName, githubSecret.Namespace)
	assert.Equal(t, "test-runners-gha-rs-github-secret", githubSecret.Name)
	assert.Equal(t, "gh_token12345", string(githubSecret.Data["github_token"]))
	assert.Equal(t, "actions.github.com/cleanup-protection", githubSecret.Finalizers[0])
}

func TestTemplateRenderedGitHubSecretWithGitHubApp(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                               "https://github.com/actions",
			"githubConfigSecret.github_app_id":              "10",
			"githubConfigSecret.github_app_installation_id": "100",
			"githubConfigSecret.github_app_private_key":     "private_key",
			"controllerServiceAccount.name":                 "arc",
			"controllerServiceAccount.namespace":            "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/githubsecret.yaml"})

	var githubSecret corev1.Secret
	helm.UnmarshalK8SYaml(t, output, &githubSecret)

	assert.Equal(t, namespaceName, githubSecret.Namespace)
	assert.Equal(t, "10", string(githubSecret.Data["github_app_id"]))
	assert.Equal(t, "100", string(githubSecret.Data["github_app_installation_id"]))
	assert.Equal(t, "private_key", string(githubSecret.Data["github_app_private_key"]))
}

func TestTemplateRenderedGitHubSecretErrorWithMissingAuthInput(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_app_id":   "",
			"githubConfigSecret.github_token":    "",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/githubsecret.yaml"})
	require.Error(t, err)

	assert.ErrorContains(t, err, "provide .Values.githubConfigSecret.github_token or .Values.githubConfigSecret.github_app_id")
}

func TestTemplateRenderedGitHubSecretErrorWithMissingAppInput(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_app_id":   "10",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/githubsecret.yaml"})
	require.Error(t, err)

	assert.ErrorContains(t, err, "provide .Values.githubConfigSecret.github_app_installation_id and .Values.githubConfigSecret.github_app_private_key")
}

func TestTemplateNotRenderedGitHubSecretWithPredefinedSecret(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret":                 "pre-defined-secret",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/githubsecret.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/githubsecret.yaml in chart", "secret should not be rendered since a pre-defined secret is provided")
}

func TestTemplateRenderedSetServiceAccountToNoPermission(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/no_permission_serviceaccount.yaml"})
	var serviceAccount corev1.ServiceAccount
	helm.UnmarshalK8SYaml(t, output, &serviceAccount)

	assert.Equal(t, namespaceName, serviceAccount.Namespace)
	assert.Equal(t, "test-runners-gha-rs-no-permission", serviceAccount.Name)

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, "test-runners-gha-rs-no-permission", ars.Spec.Template.Spec.ServiceAccountName)
	assert.Empty(t, ars.Annotations[actionsgithubcom.AnnotationKeyKubernetesModeServiceAccountName]) // no finalizer protections in place
}

func TestTemplateRenderedSetServiceAccountToKubeMode(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"containerMode.type":                 "kubernetes",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/kube_mode_serviceaccount.yaml"})
	var serviceAccount corev1.ServiceAccount
	helm.UnmarshalK8SYaml(t, output, &serviceAccount)

	assert.Equal(t, namespaceName, serviceAccount.Namespace)
	assert.Equal(t, "test-runners-gha-rs-kube-mode", serviceAccount.Name)
	assert.Equal(t, "actions.github.com/cleanup-protection", serviceAccount.Finalizers[0])

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/kube_mode_role.yaml"})
	var role rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &role)

	assert.Equal(t, namespaceName, role.Namespace)
	assert.Equal(t, "test-runners-gha-rs-kube-mode", role.Name)

	assert.Equal(t, "actions.github.com/cleanup-protection", role.Finalizers[0])

	assert.Len(t, role.Rules, 5, "kube mode role should have 5 rules")
	assert.Equal(t, "pods", role.Rules[0].Resources[0])
	assert.Equal(t, "pods/exec", role.Rules[1].Resources[0])
	assert.Equal(t, "pods/log", role.Rules[2].Resources[0])
	assert.Equal(t, "jobs", role.Rules[3].Resources[0])
	assert.Equal(t, "secrets", role.Rules[4].Resources[0])

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/kube_mode_role_binding.yaml"})
	var roleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &roleBinding)

	assert.Equal(t, namespaceName, roleBinding.Namespace)
	assert.Equal(t, "test-runners-gha-rs-kube-mode", roleBinding.Name)
	assert.Len(t, roleBinding.Subjects, 1)
	assert.Equal(t, "test-runners-gha-rs-kube-mode", roleBinding.Subjects[0].Name)
	assert.Equal(t, namespaceName, roleBinding.Subjects[0].Namespace)
	assert.Equal(t, "test-runners-gha-rs-kube-mode", roleBinding.RoleRef.Name)
	assert.Equal(t, "Role", roleBinding.RoleRef.Kind)
	assert.Equal(t, "actions.github.com/cleanup-protection", serviceAccount.Finalizers[0])

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	expectedServiceAccountName := "test-runners-gha-rs-kube-mode"
	assert.Equal(t, expectedServiceAccountName, ars.Spec.Template.Spec.ServiceAccountName)
	assert.Equal(t, expectedServiceAccountName, ars.Annotations[actionsgithubcom.AnnotationKeyKubernetesModeServiceAccountName])
}

func TestTemplateRenderedUserProvideSetServiceAccount(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"template.spec.serviceAccountName":   "test-service-account",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/no_permission_serviceaccount.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/no_permission_serviceaccount.yaml in chart", "no permission service account should not be rendered")

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, "test-service-account", ars.Spec.Template.Spec.ServiceAccountName)
	assert.Empty(t, ars.Annotations[actionsgithubcom.AnnotationKeyKubernetesModeServiceAccountName])
}

func TestTemplateRenderedAutoScalingRunnerSet(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, "test-runners", ars.Name)

	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "gha-rs", ars.Labels["app.kubernetes.io/part-of"])
	assert.Equal(t, "autoscaling-runner-set", ars.Labels["app.kubernetes.io/component"])
	assert.NotEmpty(t, ars.Labels["app.kubernetes.io/version"])

	assert.Equal(t, "https://github.com/actions", ars.Spec.GitHubConfigUrl)
	assert.Equal(t, "test-runners-gha-rs-github-secret", ars.Spec.GitHubConfigSecret)

	assert.Empty(t, ars.Spec.RunnerGroup, "RunnerGroup should be empty")

	assert.Nil(t, ars.Spec.MinRunners, "MinRunners should be nil")
	assert.Nil(t, ars.Spec.MaxRunners, "MaxRunners should be nil")
	assert.Nil(t, ars.Spec.Proxy, "Proxy should be nil")
	assert.Nil(t, ars.Spec.GitHubServerTLS, "GitHubServerTLS should be nil")

	assert.NotNil(t, ars.Spec.Template.Spec, "Template.Spec should not be nil")

	assert.Len(t, ars.Spec.Template.Spec.Containers, 1, "Template.Spec should have 1 container")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/actions-runner:latest", ars.Spec.Template.Spec.Containers[0].Image)
}

func TestTemplateRenderedAutoScalingRunnerSet_RunnerScaleSetName(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	nameOverride := "test-runner-scale-set-name"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"runnerScaleSetName":                 nameOverride,
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, nameOverride, ars.Name)

	assert.Equal(t, nameOverride, ars.Labels["app.kubernetes.io/name"])
	assert.Equal(t, nameOverride, ars.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, nameOverride, ars.Labels["actions.github.com/scale-set-name"])
	assert.Equal(t, namespaceName, ars.Labels["actions.github.com/scale-set-namespace"])
	assert.Equal(t, "gha-rs", ars.Labels["app.kubernetes.io/part-of"])
	assert.Equal(t, "https://github.com/actions", ars.Spec.GitHubConfigUrl)
	assert.Equal(t, nameOverride+"-gha-rs-github-secret", ars.Spec.GitHubConfigSecret)
	assert.Equal(t, "test-runner-scale-set-name", ars.Spec.RunnerScaleSetName)

	assert.Empty(t, ars.Spec.RunnerGroup, "RunnerGroup should be empty")

	assert.Nil(t, ars.Spec.MinRunners, "MinRunners should be nil")
	assert.Nil(t, ars.Spec.MaxRunners, "MaxRunners should be nil")
	assert.Nil(t, ars.Spec.Proxy, "Proxy should be nil")
	assert.Nil(t, ars.Spec.GitHubServerTLS, "GitHubServerTLS should be nil")

	assert.NotNil(t, ars.Spec.Template.Spec, "Template.Spec should not be nil")

	assert.Len(t, ars.Spec.Template.Spec.Containers, 1, "Template.Spec should have 1 container")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/actions-runner:latest", ars.Spec.Template.Spec.Containers[0].Image)
}

func TestTemplateRenderedAutoScalingRunnerSet_ProvideMetadata(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                     "https://github.com/actions",
			"githubConfigSecret.github_token":     "gh_token12345",
			"template.metadata.labels.test1":      "test1",
			"template.metadata.labels.test2":      "test2",
			"template.metadata.annotations.test3": "test3",
			"template.metadata.annotations.test4": "test4",
			"controllerServiceAccount.name":       "arc",
			"controllerServiceAccount.namespace":  "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, "test-runners", ars.Name)

	assert.NotNil(t, ars.Spec.Template.Labels, "Template.Spec.Labels should not be nil")
	assert.Equal(t, "test1", ars.Spec.Template.Labels["test1"], "Template.Spec.Labels should have test1")
	assert.Equal(t, "test2", ars.Spec.Template.Labels["test2"], "Template.Spec.Labels should have test2")

	assert.NotNil(t, ars.Spec.Template.Annotations, "Template.Spec.Annotations should not be nil")
	assert.Equal(t, "test3", ars.Spec.Template.Annotations["test3"], "Template.Spec.Annotations should have test3")
	assert.Equal(t, "test4", ars.Spec.Template.Annotations["test4"], "Template.Spec.Annotations should have test4")

	assert.NotNil(t, ars.Spec.Template.Spec, "Template.Spec should not be nil")

	assert.Len(t, ars.Spec.Template.Spec.Containers, 1, "Template.Spec should have 1 container")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/actions-runner:latest", ars.Spec.Template.Spec.Containers[0].Image)
}

func TestTemplateRenderedAutoScalingRunnerSet_MaxRunnersValidationError(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"maxRunners":                         "-1",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	require.Error(t, err)

	assert.ErrorContains(t, err, "maxRunners has to be greater or equal to 0")
}

func TestTemplateRenderedAutoScalingRunnerSet_MinRunnersValidationError(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"maxRunners":                         "1",
			"minRunners":                         "-1",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	require.Error(t, err)

	assert.ErrorContains(t, err, "minRunners has to be greater or equal to 0")
}

func TestTemplateRenderedAutoScalingRunnerSet_MinMaxRunnersValidationError(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"maxRunners":                         "0",
			"minRunners":                         "1",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	require.Error(t, err)

	assert.ErrorContains(t, err, "maxRunners has to be greater or equal to minRunners")
}

func TestTemplateRenderedAutoScalingRunnerSet_MinMaxRunnersValidationSameValue(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"maxRunners":                         "0",
			"minRunners":                         "0",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, 0, *ars.Spec.MinRunners, "MinRunners should be 0")
	assert.Equal(t, 0, *ars.Spec.MaxRunners, "MaxRunners should be 0")
}

func TestTemplateRenderedAutoScalingRunnerSet_MinMaxRunnersValidation_OnlyMin(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"minRunners":                         "5",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, 5, *ars.Spec.MinRunners, "MinRunners should be 5")
	assert.Nil(t, ars.Spec.MaxRunners, "MaxRunners should be nil")
}

func TestTemplateRenderedAutoScalingRunnerSet_MinMaxRunnersValidation_OnlyMax(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"maxRunners":                         "5",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, 5, *ars.Spec.MaxRunners, "MaxRunners should be 5")
	assert.Nil(t, ars.Spec.MinRunners, "MinRunners should be nil")
}

func TestTemplateRenderedAutoScalingRunnerSet_MinMaxRunners_FromValuesFile(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, 5, *ars.Spec.MinRunners, "MinRunners should be 5")
	assert.Equal(t, 10, *ars.Spec.MaxRunners, "MaxRunners should be 10")
}

func TestTemplateRenderedAutoScalingRunnerSet_ExtraVolumes(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_extra_volumes.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Volumes, 3, "Volumes should be 3")
	assert.Equal(t, "foo", ars.Spec.Template.Spec.Volumes[0].Name, "Volume name should be foo")
	assert.Equal(t, "bar", ars.Spec.Template.Spec.Volumes[1].Name, "Volume name should be bar")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Volumes[2].Name, "Volume name should be work")
	assert.Equal(t, "/data", ars.Spec.Template.Spec.Volumes[2].HostPath.Path, "Volume host path should be /data")
}

func TestTemplateRenderedAutoScalingRunnerSet_DinD_ExtraInitContainers(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_dind_extra_init_containers.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.InitContainers, 3, "InitContainers should be 3")
	assert.Equal(t, "kube-init", ars.Spec.Template.Spec.InitContainers[1].Name, "InitContainers[1] Name should be kube-init")
	assert.Equal(t, "runner-image:latest", ars.Spec.Template.Spec.InitContainers[1].Image, "InitContainers[1] Image should be runner-image:latest")
	assert.Equal(t, "sudo", ars.Spec.Template.Spec.InitContainers[1].Command[0], "InitContainers[1] Command[0] should be sudo")
	assert.Equal(t, "chown", ars.Spec.Template.Spec.InitContainers[1].Command[1], "InitContainers[1] Command[1] should be chown")
	assert.Equal(t, "-R", ars.Spec.Template.Spec.InitContainers[1].Command[2], "InitContainers[1] Command[2] should be -R")
	assert.Equal(t, "1001:123", ars.Spec.Template.Spec.InitContainers[1].Command[3], "InitContainers[1] Command[3] should be 1001:123")
	assert.Equal(t, "/home/runner/_work", ars.Spec.Template.Spec.InitContainers[1].Command[4], "InitContainers[1] Command[4] should be /home/runner/_work")
	assert.Equal(t, "work", ars.Spec.Template.Spec.InitContainers[1].VolumeMounts[0].Name, "InitContainers[1] VolumeMounts[0] Name should be work")
	assert.Equal(t, "/home/runner/_work", ars.Spec.Template.Spec.InitContainers[1].VolumeMounts[0].MountPath, "InitContainers[1] VolumeMounts[0] MountPath should be /home/runner/_work")

	assert.Equal(t, "ls", ars.Spec.Template.Spec.InitContainers[2].Name, "InitContainers[2] Name should be ls")
	assert.Equal(t, "ubuntu:latest", ars.Spec.Template.Spec.InitContainers[2].Image, "InitContainers[2] Image should be ubuntu:latest")
	assert.Equal(t, "ls", ars.Spec.Template.Spec.InitContainers[2].Command[0], "InitContainers[2] Command[0] should be ls")
}

func TestTemplateRenderedKubernetesModeServiceAccountAnnotations(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_kubernetes_mode_service_account_annotations.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/kube_mode_serviceaccount.yaml"})

	var sa corev1.ServiceAccount
	helm.UnmarshalK8SYaml(t, output, &sa)

	assert.Equal(t, "arn:aws:iam::123456789012:role/sample-role", sa.Annotations["eks.amazonaws.com/role-arn"], "Annotations should be arn:aws:iam::123456789012:role/sample-role")
}

func TestTemplateRenderedAutoScalingRunnerSet_DinD_ExtraVolumes(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_dind_extra_volumes.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Volumes, 5, "Volumes should be 5")
	assert.Equal(t, "dind-sock", ars.Spec.Template.Spec.Volumes[0].Name, "Volume name should be dind-sock")
	assert.Equal(t, "dind-externals", ars.Spec.Template.Spec.Volumes[1].Name, "Volume name should be dind-externals")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Volumes[2].Name, "Volume name should be work")
	assert.Equal(t, "/data", ars.Spec.Template.Spec.Volumes[2].HostPath.Path, "Volume host path should be /data")
	assert.Equal(t, "foo", ars.Spec.Template.Spec.Volumes[3].Name, "Volume name should be foo")
	assert.Equal(t, "bar", ars.Spec.Template.Spec.Volumes[4].Name, "Volume name should be bar")
}

func TestTemplateRenderedAutoScalingRunnerSet_K8S_ExtraVolumes(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_k8s_extra_volumes.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Volumes, 3, "Volumes should be 3")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Volumes[0].Name, "Volume name should be work")
	assert.Equal(t, "/data", ars.Spec.Template.Spec.Volumes[0].HostPath.Path, "Volume host path should be /data")
	assert.Equal(t, "foo", ars.Spec.Template.Spec.Volumes[1].Name, "Volume name should be foo")
	assert.Equal(t, "bar", ars.Spec.Template.Spec.Volumes[2].Name, "Volume name should be bar")
}

func TestTemplateRenderedAutoScalingRunnerSet_EnableDinD(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"containerMode.type":                 "dind",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, "test-runners", ars.Name)

	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "https://github.com/actions", ars.Spec.GitHubConfigUrl)
	assert.Equal(t, "test-runners-gha-rs-github-secret", ars.Spec.GitHubConfigSecret)

	assert.Empty(t, ars.Spec.RunnerGroup, "RunnerGroup should be empty")

	assert.Nil(t, ars.Spec.MinRunners, "MinRunners should be nil")
	assert.Nil(t, ars.Spec.MaxRunners, "MaxRunners should be nil")
	assert.Nil(t, ars.Spec.Proxy, "Proxy should be nil")
	assert.Nil(t, ars.Spec.GitHubServerTLS, "GitHubServerTLS should be nil")

	assert.NotNil(t, ars.Spec.Template.Spec, "Template.Spec should not be nil")

	assert.Len(t, ars.Spec.Template.Spec.InitContainers, 1, "Template.Spec should have 1 init container")
	assert.Equal(t, "init-dind-externals", ars.Spec.Template.Spec.InitContainers[0].Name)
	assert.Equal(t, "ghcr.io/actions/actions-runner:latest", ars.Spec.Template.Spec.InitContainers[0].Image)
	assert.Equal(t, "cp", ars.Spec.Template.Spec.InitContainers[0].Command[0])
	assert.Equal(t, "-r -v /home/runner/externals/. /home/runner/tmpDir/", strings.Join(ars.Spec.Template.Spec.InitContainers[0].Args, " "))

	assert.Len(t, ars.Spec.Template.Spec.Containers, 2, "Template.Spec should have 2 container")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/actions-runner:latest", ars.Spec.Template.Spec.Containers[0].Image)
	assert.Len(t, ars.Spec.Template.Spec.Containers[0].Env, 2, "The runner container should have 2 env vars, DOCKER_HOST and RUNNER_WAIT_FOR_DOCKER_IN_SECONDS")
	assert.Equal(t, "DOCKER_HOST", ars.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, "unix:///var/run/docker.sock", ars.Spec.Template.Spec.Containers[0].Env[0].Value)
	assert.Equal(t, "RUNNER_WAIT_FOR_DOCKER_IN_SECONDS", ars.Spec.Template.Spec.Containers[0].Env[1].Name)
	assert.Equal(t, "120", ars.Spec.Template.Spec.Containers[0].Env[1].Value)

	assert.Len(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, 2, "The runner container should have 2 volume mounts, dind-sock and work")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name)
	assert.Equal(t, "/home/runner/_work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
	assert.False(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].ReadOnly)

	assert.Equal(t, "dind-sock", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].Name)
	assert.Equal(t, "/var/run", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath)

	assert.Equal(t, "dind", ars.Spec.Template.Spec.Containers[1].Name)
	assert.Equal(t, "docker:dind", ars.Spec.Template.Spec.Containers[1].Image)
	assert.True(t, *ars.Spec.Template.Spec.Containers[1].SecurityContext.Privileged)
	assert.Len(t, ars.Spec.Template.Spec.Containers[1].VolumeMounts, 3, "The dind container should have 3 volume mounts, dind-sock, work and externals")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Containers[1].VolumeMounts[0].Name)
	assert.Equal(t, "/home/runner/_work", ars.Spec.Template.Spec.Containers[1].VolumeMounts[0].MountPath)

	assert.Equal(t, "dind-sock", ars.Spec.Template.Spec.Containers[1].VolumeMounts[1].Name)
	assert.Equal(t, "/var/run", ars.Spec.Template.Spec.Containers[1].VolumeMounts[1].MountPath)

	assert.Equal(t, "dind-externals", ars.Spec.Template.Spec.Containers[1].VolumeMounts[2].Name)
	assert.Equal(t, "/home/runner/externals", ars.Spec.Template.Spec.Containers[1].VolumeMounts[2].MountPath)

	assert.Len(t, ars.Spec.Template.Spec.Volumes, 3, "Volumes should be 3")
	assert.Equal(t, "dind-sock", ars.Spec.Template.Spec.Volumes[0].Name, "Volume name should be dind-sock")
	assert.Equal(t, "dind-externals", ars.Spec.Template.Spec.Volumes[1].Name, "Volume name should be dind-externals")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Volumes[2].Name, "Volume name should be work")
	assert.NotNil(t, ars.Spec.Template.Spec.Volumes[2].EmptyDir, "Volume work should be an emptyDir")
}

func TestTemplateRenderedAutoScalingRunnerSet_EnableKubernetesMode(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"containerMode.type":                 "kubernetes",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, "test-runners", ars.Name)

	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "https://github.com/actions", ars.Spec.GitHubConfigUrl)
	assert.Equal(t, "test-runners-gha-rs-github-secret", ars.Spec.GitHubConfigSecret)

	assert.Empty(t, ars.Spec.RunnerGroup, "RunnerGroup should be empty")
	assert.Nil(t, ars.Spec.MinRunners, "MinRunners should be nil")
	assert.Nil(t, ars.Spec.MaxRunners, "MaxRunners should be nil")
	assert.Nil(t, ars.Spec.Proxy, "Proxy should be nil")
	assert.Nil(t, ars.Spec.GitHubServerTLS, "GitHubServerTLS should be nil")

	assert.NotNil(t, ars.Spec.Template.Spec, "Template.Spec should not be nil")

	assert.Len(t, ars.Spec.Template.Spec.Containers, 1, "Template.Spec should have 1 container")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/actions-runner:latest", ars.Spec.Template.Spec.Containers[0].Image)

	assert.Equal(t, "ACTIONS_RUNNER_CONTAINER_HOOKS", ars.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, "/home/runner/k8s/index.js", ars.Spec.Template.Spec.Containers[0].Env[0].Value)
	assert.Equal(t, "ACTIONS_RUNNER_POD_NAME", ars.Spec.Template.Spec.Containers[0].Env[1].Name)
	assert.Equal(t, "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER", ars.Spec.Template.Spec.Containers[0].Env[2].Name)
	assert.Equal(t, "true", ars.Spec.Template.Spec.Containers[0].Env[2].Value)

	assert.Len(t, ars.Spec.Template.Spec.Volumes, 1, "Template.Spec should have 1 volume")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Volumes[0].Name)
	assert.NotNil(t, ars.Spec.Template.Spec.Volumes[0].Ephemeral, "Template.Spec should have 1 ephemeral volume")
}

func TestTemplateRenderedAutoscalingRunnerSet_ListenerPodTemplate(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_listener_template.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	require.NotNil(t, ars.Spec.ListenerTemplate, "ListenerPodTemplate should not be nil")

	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Hostname, "example")

	require.Len(t, ars.Spec.ListenerTemplate.Spec.Containers, 2, "ListenerPodTemplate should have 2 containers")
	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Containers[0].Name, "listener")
	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Containers[0].Image, "listener:latest")
	assert.ElementsMatch(t, ars.Spec.ListenerTemplate.Spec.Containers[0].Command, []string{"/path/to/entrypoint"})
	assert.Len(t, ars.Spec.ListenerTemplate.Spec.Containers[0].VolumeMounts, 1, "VolumeMounts should be 1")
	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Containers[0].VolumeMounts[0].Name, "work")
	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Containers[0].VolumeMounts[0].MountPath, "/home/example")

	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Containers[1].Name, "side-car")
	assert.Equal(t, ars.Spec.ListenerTemplate.Spec.Containers[1].Image, "nginx:latest")
}

func TestTemplateRenderedAutoScalingRunnerSet_UsePredefinedSecret(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret":                 "pre-defined-secrets",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, "test-runners", ars.Name)

	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-runners", ars.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "https://github.com/actions", ars.Spec.GitHubConfigUrl)
	assert.Equal(t, "pre-defined-secrets", ars.Spec.GitHubConfigSecret)
}

func TestTemplateRenderedAutoScalingRunnerSet_ErrorOnEmptyPredefinedSecret(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret":                 "",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	require.Error(t, err)

	assert.ErrorContains(t, err, "Values.githubConfigSecret is required for setting auth with GitHub server")
}

func TestTemplateRenderedWithProxy(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret":                 "pre-defined-secrets",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
			"proxy.http.url":                     "http://proxy.example.com",
			"proxy.http.credentialSecretRef":     "http-secret",
			"proxy.https.url":                    "https://proxy.example.com",
			"proxy.https.credentialSecretRef":    "https-secret",
			"proxy.noProxy":                      "{example.com,example.org}",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	require.NotNil(t, ars.Spec.Proxy)
	require.NotNil(t, ars.Spec.Proxy.HTTP)
	assert.Equal(t, "http://proxy.example.com", ars.Spec.Proxy.HTTP.Url)
	assert.Equal(t, "http-secret", ars.Spec.Proxy.HTTP.CredentialSecretRef)

	require.NotNil(t, ars.Spec.Proxy.HTTPS)
	assert.Equal(t, "https://proxy.example.com", ars.Spec.Proxy.HTTPS.Url)
	assert.Equal(t, "https-secret", ars.Spec.Proxy.HTTPS.CredentialSecretRef)

	require.NotNil(t, ars.Spec.Proxy.NoProxy)
	require.Len(t, ars.Spec.Proxy.NoProxy, 2)
	assert.Contains(t, ars.Spec.Proxy.NoProxy, "example.com")
	assert.Contains(t, ars.Spec.Proxy.NoProxy, "example.org")
}

func TestTemplateRenderedWithTLS(t *testing.T) {
	t.Parallel()

	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	render := func(t *testing.T, options *helm.Options) v1alpha1.AutoscalingRunnerSet {
		// Path to the helm chart we will test
		helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
		require.NoError(t, err)

		releaseName := "test-runners"

		output := helm.RenderTemplate(
			t,
			options,
			helmChartPath,
			releaseName,
			[]string{"templates/autoscalingrunnerset.yaml"},
		)

		var ars v1alpha1.AutoscalingRunnerSet
		helm.UnmarshalK8SYaml(t, output, &ars)

		return ars
	}

	t.Run("providing githubServerTLS.runnerMountPath", func(t *testing.T) {
		t.Run("mode: default", func(t *testing.T) {
			options := &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"githubConfigUrl":    "https://github.com/actions",
					"githubConfigSecret": "pre-defined-secrets",
					"githubServerTLS.certificateFrom.configMapKeyRef.name": "certs-configmap",
					"githubServerTLS.certificateFrom.configMapKeyRef.key":  "cert.pem",
					"githubServerTLS.runnerMountPath":                      "/runner/mount/path",
					"controllerServiceAccount.name":                        "arc",
					"controllerServiceAccount.namespace":                   "arc-system",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
			}

			ars := render(t, options)

			require.NotNil(t, ars.Spec.GitHubServerTLS)
			expected := &v1alpha1.GitHubServerTLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "certs-configmap",
						},
						Key: "cert.pem",
					},
				},
			}
			assert.Equal(t, expected, ars.Spec.GitHubServerTLS)

			var volume *corev1.Volume
			for _, v := range ars.Spec.Template.Spec.Volumes {
				if v.Name == "github-server-tls-cert" {
					volume = &v
					break
				}
			}
			require.NotNil(t, volume)
			assert.Equal(t, "certs-configmap", volume.ConfigMap.LocalObjectReference.Name)
			assert.Equal(t, "cert.pem", volume.ConfigMap.Items[0].Key)
			assert.Equal(t, "cert.pem", volume.ConfigMap.Items[0].Path)

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "github-server-tls-cert",
				MountPath: "/runner/mount/path/cert.pem",
				SubPath:   "cert.pem",
			})

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/runner/mount/path/cert.pem",
			})

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "RUNNER_UPDATE_CA_CERTS",
				Value: "1",
			})
		})

		t.Run("mode: dind", func(t *testing.T) {
			options := &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"githubConfigUrl":    "https://github.com/actions",
					"githubConfigSecret": "pre-defined-secrets",
					"githubServerTLS.certificateFrom.configMapKeyRef.name": "certs-configmap",
					"githubServerTLS.certificateFrom.configMapKeyRef.key":  "cert.pem",
					"githubServerTLS.runnerMountPath":                      "/runner/mount/path/",
					"containerMode.type":                                   "dind",
					"controllerServiceAccount.name":                        "arc",
					"controllerServiceAccount.namespace":                   "arc-system",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
			}

			ars := render(t, options)

			require.NotNil(t, ars.Spec.GitHubServerTLS)
			expected := &v1alpha1.GitHubServerTLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "certs-configmap",
						},
						Key: "cert.pem",
					},
				},
			}
			assert.Equal(t, expected, ars.Spec.GitHubServerTLS)

			var volume *corev1.Volume
			for _, v := range ars.Spec.Template.Spec.Volumes {
				if v.Name == "github-server-tls-cert" {
					volume = &v
					break
				}
			}
			require.NotNil(t, volume)
			assert.Equal(t, "certs-configmap", volume.ConfigMap.LocalObjectReference.Name)
			assert.Equal(t, "cert.pem", volume.ConfigMap.Items[0].Key)
			assert.Equal(t, "cert.pem", volume.ConfigMap.Items[0].Path)

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "github-server-tls-cert",
				MountPath: "/runner/mount/path/cert.pem",
				SubPath:   "cert.pem",
			})

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/runner/mount/path/cert.pem",
			})

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "RUNNER_UPDATE_CA_CERTS",
				Value: "1",
			})
		})

		t.Run("mode: kubernetes", func(t *testing.T) {
			options := &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"githubConfigUrl":    "https://github.com/actions",
					"githubConfigSecret": "pre-defined-secrets",
					"githubServerTLS.certificateFrom.configMapKeyRef.name": "certs-configmap",
					"githubServerTLS.certificateFrom.configMapKeyRef.key":  "cert.pem",
					"githubServerTLS.runnerMountPath":                      "/runner/mount/path",
					"containerMode.type":                                   "kubernetes",
					"controllerServiceAccount.name":                        "arc",
					"controllerServiceAccount.namespace":                   "arc-system",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
			}

			ars := render(t, options)

			require.NotNil(t, ars.Spec.GitHubServerTLS)
			expected := &v1alpha1.GitHubServerTLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "certs-configmap",
						},
						Key: "cert.pem",
					},
				},
			}
			assert.Equal(t, expected, ars.Spec.GitHubServerTLS)

			var volume *corev1.Volume
			for _, v := range ars.Spec.Template.Spec.Volumes {
				if v.Name == "github-server-tls-cert" {
					volume = &v
					break
				}
			}
			require.NotNil(t, volume)
			assert.Equal(t, "certs-configmap", volume.ConfigMap.LocalObjectReference.Name)
			assert.Equal(t, "cert.pem", volume.ConfigMap.Items[0].Key)
			assert.Equal(t, "cert.pem", volume.ConfigMap.Items[0].Path)

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "github-server-tls-cert",
				MountPath: "/runner/mount/path/cert.pem",
				SubPath:   "cert.pem",
			})

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/runner/mount/path/cert.pem",
			})

			assert.Contains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "RUNNER_UPDATE_CA_CERTS",
				Value: "1",
			})
		})
	})

	t.Run("without providing githubServerTLS.runnerMountPath", func(t *testing.T) {
		t.Run("mode: default", func(t *testing.T) {
			options := &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"githubConfigUrl":    "https://github.com/actions",
					"githubConfigSecret": "pre-defined-secrets",
					"githubServerTLS.certificateFrom.configMapKeyRef.name": "certs-configmap",
					"githubServerTLS.certificateFrom.configMapKeyRef.key":  "cert.pem",
					"controllerServiceAccount.name":                        "arc",
					"controllerServiceAccount.namespace":                   "arc-system",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
			}

			ars := render(t, options)

			require.NotNil(t, ars.Spec.GitHubServerTLS)
			expected := &v1alpha1.GitHubServerTLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "certs-configmap",
						},
						Key: "cert.pem",
					},
				},
			}
			assert.Equal(t, expected, ars.Spec.GitHubServerTLS)

			var volume *corev1.Volume
			for _, v := range ars.Spec.Template.Spec.Volumes {
				if v.Name == "github-server-tls-cert" {
					volume = &v
					break
				}
			}
			assert.Nil(t, volume)

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "github-server-tls-cert",
				MountPath: "/runner/mount/path/cert.pem",
				SubPath:   "cert.pem",
			})

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/runner/mount/path/cert.pem",
			})

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "RUNNER_UPDATE_CA_CERTS",
				Value: "1",
			})
		})

		t.Run("mode: dind", func(t *testing.T) {
			options := &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"githubConfigUrl":    "https://github.com/actions",
					"githubConfigSecret": "pre-defined-secrets",
					"githubServerTLS.certificateFrom.configMapKeyRef.name": "certs-configmap",
					"githubServerTLS.certificateFrom.configMapKeyRef.key":  "cert.pem",
					"containerMode.type":                 "dind",
					"controllerServiceAccount.name":      "arc",
					"controllerServiceAccount.namespace": "arc-system",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
			}

			ars := render(t, options)

			require.NotNil(t, ars.Spec.GitHubServerTLS)
			expected := &v1alpha1.GitHubServerTLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "certs-configmap",
						},
						Key: "cert.pem",
					},
				},
			}
			assert.Equal(t, expected, ars.Spec.GitHubServerTLS)

			var volume *corev1.Volume
			for _, v := range ars.Spec.Template.Spec.Volumes {
				if v.Name == "github-server-tls-cert" {
					volume = &v
					break
				}
			}
			assert.Nil(t, volume)

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "github-server-tls-cert",
				MountPath: "/runner/mount/path/cert.pem",
				SubPath:   "cert.pem",
			})

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/runner/mount/path/cert.pem",
			})

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "RUNNER_UPDATE_CA_CERTS",
				Value: "1",
			})
		})

		t.Run("mode: kubernetes", func(t *testing.T) {
			options := &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"githubConfigUrl":    "https://github.com/actions",
					"githubConfigSecret": "pre-defined-secrets",
					"githubServerTLS.certificateFrom.configMapKeyRef.name": "certs-configmap",
					"githubServerTLS.certificateFrom.configMapKeyRef.key":  "cert.pem",
					"containerMode.type":                 "kubernetes",
					"controllerServiceAccount.name":      "arc",
					"controllerServiceAccount.namespace": "arc-system",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
			}

			ars := render(t, options)

			require.NotNil(t, ars.Spec.GitHubServerTLS)
			expected := &v1alpha1.GitHubServerTLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "certs-configmap",
						},
						Key: "cert.pem",
					},
				},
			}
			assert.Equal(t, expected, ars.Spec.GitHubServerTLS)

			var volume *corev1.Volume
			for _, v := range ars.Spec.Template.Spec.Volumes {
				if v.Name == "github-server-tls-cert" {
					volume = &v
					break
				}
			}
			assert.Nil(t, volume)

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "github-server-tls-cert",
				MountPath: "/runner/mount/path/cert.pem",
				SubPath:   "cert.pem",
			})

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "NODE_EXTRA_CA_CERTS",
				Value: "/runner/mount/path/cert.pem",
			})

			assert.NotContains(t, ars.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "RUNNER_UPDATE_CA_CERTS",
				Value: "1",
			})
		})
	})
}

func TestTemplateNamingConstraints(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	setValues := map[string]string{
		"githubConfigUrl":                    "https://github.com/actions",
		"githubConfigSecret":                 "",
		"controllerServiceAccount.name":      "arc",
		"controllerServiceAccount.namespace": "arc-system",
	}

	tt := map[string]struct {
		releaseName   string
		namespaceName string
		expectedError string
	}{
		"Name too long": {
			releaseName:   strings.Repeat("a", 46),
			namespaceName: "test-" + strings.ToLower(random.UniqueId()),
			expectedError: "Name must have up to 45 characters",
		},
		"Namespace too long": {
			releaseName:   "test-" + strings.ToLower(random.UniqueId()),
			namespaceName: strings.Repeat("a", 64),
			expectedError: "Namespace must have up to 63 characters",
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			options := &helm.Options{
				Logger:         logger.Discard,
				SetValues:      setValues,
				KubectlOptions: k8s.NewKubectlOptions("", "", tc.namespaceName),
			}
			_, err = helm.RenderTemplateE(t, options, helmChartPath, tc.releaseName, []string{"templates/autoscalingrunnerset.yaml"})
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.expectedError)
		})
	}
}

func TestTemplateRenderedGitHubConfigUrlEndsWIthSlash(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions/",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, namespaceName, ars.Namespace)
	assert.Equal(t, "test-runners", ars.Name)
	assert.Equal(t, "https://github.com/actions", ars.Spec.GitHubConfigUrl)
}

func TestTemplate_CreateManagerRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_role.yaml"})

	var managerRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &managerRole)

	assert.Equal(t, namespaceName, managerRole.Namespace, "namespace should match the namespace of the Helm release")
	assert.Equal(t, "test-runners-gha-rs-manager", managerRole.Name)
	assert.Equal(t, "actions.github.com/cleanup-protection", managerRole.Finalizers[0])
	assert.Equal(t, 6, len(managerRole.Rules))

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)
}

func TestTemplate_CreateManagerRole_UseConfigMaps(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                                      "https://github.com/actions",
			"githubConfigSecret.github_token":                      "gh_token12345",
			"controllerServiceAccount.name":                        "arc",
			"controllerServiceAccount.namespace":                   "arc-system",
			"githubServerTLS.certificateFrom.configMapKeyRef.name": "test",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_role.yaml"})

	var managerRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &managerRole)

	assert.Equal(t, namespaceName, managerRole.Namespace, "namespace should match the namespace of the Helm release")
	assert.Equal(t, "test-runners-gha-rs-manager", managerRole.Name)
	assert.Equal(t, "actions.github.com/cleanup-protection", managerRole.Finalizers[0])
	assert.Equal(t, 7, len(managerRole.Rules))
	assert.Equal(t, "configmaps", managerRole.Rules[6].Resources[0])
}

func TestTemplate_CreateManagerRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_role_binding.yaml"})

	var managerRoleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &managerRoleBinding)

	assert.Equal(t, namespaceName, managerRoleBinding.Namespace, "namespace should match the namespace of the Helm release")
	assert.Equal(t, "test-runners-gha-rs-manager", managerRoleBinding.Name)
	assert.Equal(t, "test-runners-gha-rs-manager", managerRoleBinding.RoleRef.Name)
	assert.Equal(t, "actions.github.com/cleanup-protection", managerRoleBinding.Finalizers[0])
	assert.Equal(t, "arc", managerRoleBinding.Subjects[0].Name)
	assert.Equal(t, "arc-system", managerRoleBinding.Subjects[0].Namespace)
}

func TestTemplateRenderedAutoScalingRunnerSet_ExtraContainers(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_extra_containers.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"}, "--debug")

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Containers, 2, "There should be 2 containers")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name, "Container name should be runner")
	assert.Equal(t, "other", ars.Spec.Template.Spec.Containers[1].Name, "Container name should be other")
	assert.Equal(t, "250m", ars.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String(), "CPU Limit should be set")
	assert.Equal(t, "64Mi", ars.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String(), "Memory Limit should be set")
	assert.Equal(t, "250m", ars.Spec.Template.Spec.Containers[1].Resources.Limits.Cpu().String(), "CPU Limit should be set")
	assert.Equal(t, "64Mi", ars.Spec.Template.Spec.Containers[1].Resources.Limits.Memory().String(), "Memory Limit should be set")
	assert.Equal(t, "SOME_ENV", ars.Spec.Template.Spec.Containers[0].Env[0].Name, "SOME_ENV should be set")
	assert.Equal(t, "SOME_VALUE", ars.Spec.Template.Spec.Containers[0].Env[0].Value, "SOME_ENV should be set to `SOME_VALUE`")
	assert.Equal(t, "MY_NODE_NAME", ars.Spec.Template.Spec.Containers[0].Env[1].Name, "MY_NODE_NAME should be set")
	assert.Equal(t, "spec.nodeName", ars.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath, "MY_NODE_NAME should be set to `spec.nodeName`")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name, "VolumeMount name should be work")
	assert.Equal(t, "/work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath, "VolumeMount mountPath should be /work")
	assert.Equal(t, "others", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].Name, "VolumeMount name should be others")
	assert.Equal(t, "/others", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath, "VolumeMount mountPath should be /others")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Volumes[0].Name, "Volume name should be work")
	assert.Equal(t, corev1.DNSNone, ars.Spec.Template.Spec.DNSPolicy, "DNS Policy should be None")
	assert.Equal(t, "192.0.2.1", ars.Spec.Template.Spec.DNSConfig.Nameservers[0], "DNS Nameserver should be set")
}

func TestTemplateRenderedAutoScalingRunnerSet_RestartPolicy(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, corev1.RestartPolicyNever, ars.Spec.Template.Spec.RestartPolicy, "RestartPolicy should be Never")

	options = &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
			"template.spec.restartPolicy":        "Always",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"}, "--debug")

	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Equal(t, corev1.RestartPolicyAlways, ars.Spec.Template.Spec.RestartPolicy, "RestartPolicy should be Always")
}

func TestTemplateRenderedAutoScalingRunnerSet_ExtraPodSpec(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_extra_pod_spec.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Containers, 1, "There should be 1 containers")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name, "Container name should be runner")
	assert.Equal(t, corev1.DNSNone, ars.Spec.Template.Spec.DNSPolicy, "DNS Policy should be None")
	assert.Equal(t, "192.0.2.1", ars.Spec.Template.Spec.DNSConfig.Nameservers[0], "DNS Nameserver should be set")
}

func TestTemplateRenderedAutoScalingRunnerSet_DinDMergePodSpec(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_dind_merge_spec.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"}, "--debug")

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Containers, 2, "There should be 2 containers")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name, "Container name should be runner")
	assert.Equal(t, "250m", ars.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String(), "CPU Limit should be set")
	assert.Equal(t, "64Mi", ars.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String(), "Memory Limit should be set")
	assert.Equal(t, "DOCKER_HOST", ars.Spec.Template.Spec.Containers[0].Env[0].Name, "DOCKER_HOST should be set")
	assert.Equal(t, "tcp://localhost:9999", ars.Spec.Template.Spec.Containers[0].Env[0].Value, "DOCKER_HOST should be set to `tcp://localhost:9999`")
	assert.Equal(t, "MY_NODE_NAME", ars.Spec.Template.Spec.Containers[0].Env[1].Name, "MY_NODE_NAME should be set")
	assert.Equal(t, "spec.nodeName", ars.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath, "MY_NODE_NAME should be set to `spec.nodeName`")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name, "VolumeMount name should be work")
	assert.Equal(t, "/work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath, "VolumeMount mountPath should be /work")
	assert.Equal(t, "others", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].Name, "VolumeMount name should be others")
	assert.Equal(t, "/others", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath, "VolumeMount mountPath should be /others")
}

func TestTemplateRenderedAutoScalingRunnerSet_KubeModeMergePodSpec(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values_k8s_merge_spec.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"}, "--debug")

	var ars v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &ars)

	assert.Len(t, ars.Spec.Template.Spec.Containers, 1, "There should be 1 containers")
	assert.Equal(t, "runner", ars.Spec.Template.Spec.Containers[0].Name, "Container name should be runner")
	assert.Equal(t, "250m", ars.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String(), "CPU Limit should be set")
	assert.Equal(t, "64Mi", ars.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String(), "Memory Limit should be set")
	assert.Equal(t, "ACTIONS_RUNNER_CONTAINER_HOOKS", ars.Spec.Template.Spec.Containers[0].Env[0].Name, "ACTIONS_RUNNER_CONTAINER_HOOKS should be set")
	assert.Equal(t, "/k8s/index.js", ars.Spec.Template.Spec.Containers[0].Env[0].Value, "ACTIONS_RUNNER_CONTAINER_HOOKS should be set to `/k8s/index.js`")
	assert.Equal(t, "MY_NODE_NAME", ars.Spec.Template.Spec.Containers[0].Env[1].Name, "MY_NODE_NAME should be set")
	assert.Equal(t, "spec.nodeName", ars.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath, "MY_NODE_NAME should be set to `spec.nodeName`")
	assert.Equal(t, "ACTIONS_RUNNER_POD_NAME", ars.Spec.Template.Spec.Containers[0].Env[2].Name, "ACTIONS_RUNNER_POD_NAME should be set")
	assert.Equal(t, "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER", ars.Spec.Template.Spec.Containers[0].Env[3].Name, "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER should be set")
	assert.Equal(t, "work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name, "VolumeMount name should be work")
	assert.Equal(t, "/work", ars.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath, "VolumeMount mountPath should be /work")
	assert.Equal(t, "others", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].Name, "VolumeMount name should be others")
	assert.Equal(t, "/others", ars.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath, "VolumeMount mountPath should be /others")
}

func TestTemplateRenderedAutoscalingRunnerSetAnnotation_GitHubSecret(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	annotationExpectedTests := map[string]*helm.Options{
		"GitHub token": {
			Logger: logger.Discard,
			SetValues: map[string]string{
				"githubConfigUrl":                    "https://github.com/actions",
				"githubConfigSecret.github_token":    "gh_token12345",
				"controllerServiceAccount.name":      "arc",
				"controllerServiceAccount.namespace": "arc-system",
			},
			KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
		},
		"GitHub app": {
			Logger: logger.Discard,
			SetValues: map[string]string{
				"githubConfigUrl":                               "https://github.com/actions",
				"githubConfigSecret.github_app_id":              "10",
				"githubConfigSecret.github_app_installation_id": "100",
				"githubConfigSecret.github_app_private_key":     "private_key",
				"controllerServiceAccount.name":                 "arc",
				"controllerServiceAccount.namespace":            "arc-system",
			},
			KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
		},
	}

	for name, options := range annotationExpectedTests {
		t.Run("Annotation set: "+name, func(t *testing.T) {
			output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
			var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
			helm.UnmarshalK8SYaml(t, output, &autoscalingRunnerSet)

			assert.NotEmpty(t, autoscalingRunnerSet.Annotations[actionsgithubcom.AnnotationKeyGitHubSecretName])
		})
	}

	t.Run("Annotation should not be set", func(t *testing.T) {
		options := &helm.Options{
			Logger: logger.Discard,
			SetValues: map[string]string{
				"githubConfigUrl":                    "https://github.com/actions",
				"githubConfigSecret":                 "pre-defined-secret",
				"controllerServiceAccount.name":      "arc",
				"controllerServiceAccount.namespace": "arc-system",
			},
			KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
		}
		output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
		var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
		helm.UnmarshalK8SYaml(t, output, &autoscalingRunnerSet)

		assert.Empty(t, autoscalingRunnerSet.Annotations[actionsgithubcom.AnnotationKeyGitHubSecretName])
	})
}

func TestTemplateRenderedAutoscalingRunnerSetAnnotation_KubernetesModeCleanup(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
			"containerMode.type":                 "kubernetes",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &autoscalingRunnerSet)

	annotationValues := map[string]string{
		actionsgithubcom.AnnotationKeyGitHubSecretName:                 "test-runners-gha-rs-github-secret",
		actionsgithubcom.AnnotationKeyManagerRoleName:                  "test-runners-gha-rs-manager",
		actionsgithubcom.AnnotationKeyManagerRoleBindingName:           "test-runners-gha-rs-manager",
		actionsgithubcom.AnnotationKeyKubernetesModeServiceAccountName: "test-runners-gha-rs-kube-mode",
		actionsgithubcom.AnnotationKeyKubernetesModeRoleName:           "test-runners-gha-rs-kube-mode",
		actionsgithubcom.AnnotationKeyKubernetesModeRoleBindingName:    "test-runners-gha-rs-kube-mode",
	}

	for annotation, value := range annotationValues {
		assert.Equal(t, value, autoscalingRunnerSet.Annotations[annotation], fmt.Sprintf("Annotation %q does not match the expected value", annotation))
	}
}

func TestRunnerContainerEnvNotEmptyMap(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	type testModel struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []map[string]any `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}

	var m testModel
	helm.UnmarshalK8SYaml(t, output, &m)
	_, ok := m.Spec.Template.Spec.Containers[0]["env"]
	assert.False(t, ok, "env should not be set")
}

func TestRunnerContainerVolumeNotEmptyMap(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	testValuesPath, err := filepath.Abs("../tests/values.yaml")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		ValuesFiles:    []string{testValuesPath},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})
	type testModel struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []map[string]any `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}

	var m testModel
	helm.UnmarshalK8SYaml(t, output, &m)
	_, ok := m.Spec.Template.Spec.Containers[0]["volumeMounts"]
	assert.False(t, ok, "volumeMounts should not be set")
}

func TestAutoscalingRunnerSetAnnotationValuesHash(t *testing.T) {
	t.Parallel()

	const valuesHash = "actions.github.com/values-hash"

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	releaseName := "test-runners"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token12345",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
	helm.UnmarshalK8SYaml(t, output, &autoscalingRunnerSet)

	firstHash := autoscalingRunnerSet.Annotations["actions.github.com/values-hash"]
	assert.NotEmpty(t, firstHash)
	assert.LessOrEqual(t, len(firstHash), 63)

	helmChartPath, err = filepath.Abs("../../gha-runner-scale-set")
	require.NoError(t, err)

	options = &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"githubConfigUrl":                    "https://github.com/actions",
			"githubConfigSecret.github_token":    "gh_token1234567890",
			"controllerServiceAccount.name":      "arc",
			"controllerServiceAccount.namespace": "arc-system",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/autoscalingrunnerset.yaml"})

	helm.UnmarshalK8SYaml(t, output, &autoscalingRunnerSet)
	secondHash := autoscalingRunnerSet.Annotations[valuesHash]
	assert.NotEmpty(t, secondHash)
	assert.NotEqual(t, firstHash, secondHash)
	assert.LessOrEqual(t, len(secondHash), 63)
}
