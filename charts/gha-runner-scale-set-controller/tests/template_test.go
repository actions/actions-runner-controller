package tests

import (
	"fmt"
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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Chart struct {
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

func TestTemplate_CreateServiceAccount(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create":          "true",
			"serviceAccount.annotations.foo": "bar",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/serviceaccount.yaml"})

	var serviceAccount corev1.ServiceAccount
	helm.UnmarshalK8SYaml(t, output, &serviceAccount)

	assert.Equal(t, namespaceName, serviceAccount.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller", serviceAccount.Name)
	assert.Equal(t, "bar", string(serviceAccount.Annotations["foo"]))
}

func TestTemplate_CreateServiceAccount_OverwriteName(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create":          "true",
			"serviceAccount.name":            "overwritten-name",
			"serviceAccount.annotations.foo": "bar",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/serviceaccount.yaml"})

	var serviceAccount corev1.ServiceAccount
	helm.UnmarshalK8SYaml(t, output, &serviceAccount)

	assert.Equal(t, namespaceName, serviceAccount.Namespace)
	assert.Equal(t, "overwritten-name", serviceAccount.Name)
	assert.Equal(t, "bar", string(serviceAccount.Annotations["foo"]))
}

func TestTemplate_CreateServiceAccount_CannotUseDefaultServiceAccount(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create":          "true",
			"serviceAccount.name":            "default",
			"serviceAccount.annotations.foo": "bar",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/serviceaccount.yaml"})
	assert.ErrorContains(t, err, "serviceAccount.name cannot be set to 'default'", "We should get an error because the default service account cannot be used")
}

func TestTemplate_NotCreateServiceAccount(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create":          "false",
			"serviceAccount.name":            "overwritten-name",
			"serviceAccount.annotations.foo": "bar",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/serviceaccount.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/serviceaccount.yaml in chart", "We should get an error because the template should be skipped")
}

func TestTemplate_NotCreateServiceAccount_ServiceAccountNotSet(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create":          "false",
			"serviceAccount.annotations.foo": "bar",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})
	assert.ErrorContains(t, err, "serviceAccount.name must be set if serviceAccount.create is false", "We should get an error because the default service account cannot be used")
}

func TestTemplate_CreateManagerClusterRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		SetValues:      map[string]string{},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_cluster_role.yaml"})

	var managerClusterRole rbacv1.ClusterRole
	helm.UnmarshalK8SYaml(t, output, &managerClusterRole)

	assert.Empty(t, managerClusterRole.Namespace, "ClusterRole should not have a namespace")
	assert.Equal(t, "test-arc-gha-rs-controller", managerClusterRole.Name)
	assert.Equal(t, 16, len(managerClusterRole.Rules))

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_controller_role.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/manager_single_namespace_controller_role.yaml in chart", "We should get an error because the template should be skipped")

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_watch_role.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/manager_single_namespace_watch_role.yaml in chart", "We should get an error because the template should be skipped")
}

func TestTemplate_ManagerClusterRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create": "true",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_cluster_role_binding.yaml"})

	var managerClusterRoleBinding rbacv1.ClusterRoleBinding
	helm.UnmarshalK8SYaml(t, output, &managerClusterRoleBinding)

	assert.Empty(t, managerClusterRoleBinding.Namespace, "ClusterRoleBinding should not have a namespace")
	assert.Equal(t, "test-arc-gha-rs-controller", managerClusterRoleBinding.Name)
	assert.Equal(t, "test-arc-gha-rs-controller", managerClusterRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-rs-controller", managerClusterRoleBinding.Subjects[0].Name)
	assert.Equal(t, namespaceName, managerClusterRoleBinding.Subjects[0].Namespace)

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_controller_role_binding.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/manager_single_namespace_controller_role_binding.yaml in chart", "We should get an error because the template should be skipped")

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_watch_role_binding.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/manager_single_namespace_watch_role_binding.yaml in chart", "We should get an error because the template should be skipped")
}

func TestTemplate_CreateManagerListenerRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger:         logger.Discard,
		SetValues:      map[string]string{},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_listener_role.yaml"})

	var managerListenerRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &managerListenerRole)

	assert.Equal(t, namespaceName, managerListenerRole.Namespace, "Role should have a namespace")
	assert.Equal(t, "test-arc-gha-rs-controller-listener", managerListenerRole.Name)
	assert.Equal(t, 4, len(managerListenerRole.Rules))
	assert.Equal(t, "pods", managerListenerRole.Rules[0].Resources[0])
	assert.Equal(t, "pods/status", managerListenerRole.Rules[1].Resources[0])
	assert.Equal(t, "secrets", managerListenerRole.Rules[2].Resources[0])
	assert.Equal(t, "serviceaccounts", managerListenerRole.Rules[3].Resources[0])
}

func TestTemplate_ManagerListenerRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create": "true",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_listener_role_binding.yaml"})

	var managerListenerRoleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &managerListenerRoleBinding)

	assert.Equal(t, namespaceName, managerListenerRoleBinding.Namespace, "RoleBinding should have a namespace")
	assert.Equal(t, "test-arc-gha-rs-controller-listener", managerListenerRoleBinding.Name)
	assert.Equal(t, "test-arc-gha-rs-controller-listener", managerListenerRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-rs-controller", managerListenerRoleBinding.Subjects[0].Name)
	assert.Equal(t, namespaceName, managerListenerRoleBinding.Subjects[0].Namespace)
}

func TestTemplate_ControllerDeployment_Defaults(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"image.tag": "dev",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Name)
	assert.Equal(t, "gha-rs-controller-"+chart.Version, deployment.Labels["helm.sh/chart"])
	assert.Equal(t, "gha-rs-controller", deployment.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, chart.AppVersion, deployment.Labels["app.kubernetes.io/version"])
	assert.Equal(t, "Helm", deployment.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, namespaceName, deployment.Labels["actions.github.com/controller-service-account-namespace"])
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Labels["actions.github.com/controller-service-account-name"])
	assert.NotContains(t, deployment.Labels, "actions.github.com/controller-watch-single-namespace")
	assert.Equal(t, "gha-rs-controller", deployment.Labels["app.kubernetes.io/part-of"])

	assert.Equal(t, int32(1), *deployment.Spec.Replicas)

	assert.Equal(t, "gha-rs-controller", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/instance"])

	assert.Equal(t, "gha-rs-controller", deployment.Spec.Template.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Template.Labels["app.kubernetes.io/instance"])

	assert.Equal(t, "manager", deployment.Spec.Template.Annotations["kubectl.kubernetes.io/default-container"])

	assert.Len(t, deployment.Spec.Template.Spec.ImagePullSecrets, 0)
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Spec.Template.Spec.ServiceAccountName)
	assert.Nil(t, deployment.Spec.Template.Spec.SecurityContext)
	assert.Empty(t, deployment.Spec.Template.Spec.PriorityClassName)
	assert.Equal(t, int64(10), *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Len(t, deployment.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Volumes[0].Name)
	assert.NotNil(t, 10, deployment.Spec.Template.Spec.Volumes[0].EmptyDir)

	assert.Len(t, deployment.Spec.Template.Spec.NodeSelector, 0)
	assert.Nil(t, deployment.Spec.Template.Spec.Affinity)
	assert.Len(t, deployment.Spec.Template.Spec.TopologySpreadConstraints, 0)
	assert.Len(t, deployment.Spec.Template.Spec.Tolerations, 0)

	managerImage := "ghcr.io/actions/gha-runner-scale-set-controller:dev"

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, managerImage, deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullIfNotPresent, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	expectedArgs := []string{
		"--auto-scaling-runner-set-only",
		"--log-level=debug",
		"--log-format=text",
		"--update-strategy=immediate",
		"--metrics-addr=0",
		"--listener-metrics-addr=0",
		"--listener-metrics-endpoint=",
		"--runner-max-concurrent-reconciles=2",
	}
	assert.ElementsMatch(t, expectedArgs, deployment.Spec.Template.Spec.Containers[0].Args)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Env, 2)
	assert.Equal(t, "CONTROLLER_MANAGER_CONTAINER_IMAGE", deployment.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, managerImage, deployment.Spec.Template.Spec.Containers[0].Env[0].Value)

	assert.Equal(t, "CONTROLLER_MANAGER_POD_NAMESPACE", deployment.Spec.Template.Spec.Containers[0].Env[1].Name)
	assert.Equal(t, "metadata.namespace", deployment.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath)

	assert.Empty(t, deployment.Spec.Template.Spec.Containers[0].Resources)
	assert.Nil(t, deployment.Spec.Template.Spec.Containers[0].SecurityContext)
	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name)
	assert.Equal(t, "/tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
}

func TestTemplate_ControllerDeployment_Customize(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"labels.foo":                   "bar",
			"labels.github":                "actions",
			"labels.team":                  "GitHub Team",
			"labels.teamMail":              "team@github.com",
			"replicaCount":                 "1",
			"image.pullPolicy":             "Always",
			"image.tag":                    "dev",
			"imagePullSecrets[0].name":     "dockerhub",
			"nameOverride":                 "gha-rs-controller-override",
			"fullnameOverride":             "gha-rs-controller-fullname-override",
			"env[0].name":                  "ENV_VAR_NAME_1",
			"env[0].value":                 "ENV_VAR_VALUE_1",
			"serviceAccount.name":          "gha-rs-controller-sa",
			"podAnnotations.foo":           "bar",
			"podSecurityContext.fsGroup":   "1000",
			"securityContext.runAsUser":    "1000",
			"securityContext.runAsNonRoot": "true",
			"resources.limits.cpu":         "500m",
			"nodeSelector.foo":             "bar",
			"tolerations[0].key":           "foo",
			"affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key":      "foo",
			"affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator": "bar",
			"topologySpreadConstraints[0].labelSelector.matchLabels.foo":                                                             "bar",
			"topologySpreadConstraints[0].maxSkew":                                                                                   "1",
			"topologySpreadConstraints[0].topologyKey":                                                                               "foo",
			"priorityClassName":         "test-priority-class",
			"flags.updateStrategy":      "eventual",
			"flags.logLevel":            "info",
			"flags.logFormat":           "json",
			"volumes[0].name":           "customMount",
			"volumes[0].configMap.name": "my-configmap",
			"volumeMounts[0].name":      "customMount",
			"volumeMounts[0].mountPath": "/my/mount/path",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "gha-rs-controller-fullname-override", deployment.Name)
	assert.Equal(t, "gha-rs-controller-"+chart.Version, deployment.Labels["helm.sh/chart"])
	assert.Equal(t, "gha-rs-controller-override", deployment.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, chart.AppVersion, deployment.Labels["app.kubernetes.io/version"])
	assert.Equal(t, "Helm", deployment.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "gha-rs-controller", deployment.Labels["app.kubernetes.io/part-of"])
	assert.Equal(t, "bar", deployment.Labels["foo"])
	assert.Equal(t, "actions", deployment.Labels["github"])
	assert.Equal(t, "GitHub Team", deployment.Labels["team"])
	assert.Equal(t, "team@github.com", deployment.Labels["teamMail"])

	assert.Equal(t, int32(1), *deployment.Spec.Replicas)

	assert.Equal(t, "gha-rs-controller-override", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/instance"])

	assert.Equal(t, "gha-rs-controller-override", deployment.Spec.Template.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Template.Labels["app.kubernetes.io/instance"])

	assert.Equal(t, "bar", deployment.Spec.Template.Annotations["foo"])
	assert.Equal(t, "manager", deployment.Spec.Template.Annotations["kubectl.kubernetes.io/default-container"])

	assert.Equal(t, "ENV_VAR_NAME_1", deployment.Spec.Template.Spec.Containers[0].Env[2].Name)
	assert.Equal(t, "ENV_VAR_VALUE_1", deployment.Spec.Template.Spec.Containers[0].Env[2].Value)

	assert.Len(t, deployment.Spec.Template.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "dockerhub", deployment.Spec.Template.Spec.ImagePullSecrets[0].Name)
	assert.Equal(t, "gha-rs-controller-sa", deployment.Spec.Template.Spec.ServiceAccountName)
	assert.Equal(t, int64(1000), *deployment.Spec.Template.Spec.SecurityContext.FSGroup)
	assert.Equal(t, "test-priority-class", deployment.Spec.Template.Spec.PriorityClassName)
	assert.Equal(t, int64(10), *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Len(t, deployment.Spec.Template.Spec.Volumes, 2)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Volumes[0].Name)
	assert.NotNil(t, deployment.Spec.Template.Spec.Volumes[0].EmptyDir)
	assert.Equal(t, "customMount", deployment.Spec.Template.Spec.Volumes[1].Name)
	assert.Equal(t, "my-configmap", deployment.Spec.Template.Spec.Volumes[1].ConfigMap.Name)

	assert.Len(t, deployment.Spec.Template.Spec.NodeSelector, 1)
	assert.Equal(t, "bar", deployment.Spec.Template.Spec.NodeSelector["foo"])

	assert.NotNil(t, deployment.Spec.Template.Spec.Affinity.NodeAffinity)
	assert.Equal(t, "foo", deployment.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Key)
	assert.Equal(t, "bar", string(deployment.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Operator))

	assert.Len(t, deployment.Spec.Template.Spec.TopologySpreadConstraints, 1)
	assert.Equal(t, "bar", deployment.Spec.Template.Spec.TopologySpreadConstraints[0].LabelSelector.MatchLabels["foo"])
	assert.Equal(t, int32(1), deployment.Spec.Template.Spec.TopologySpreadConstraints[0].MaxSkew)
	assert.Equal(t, "foo", deployment.Spec.Template.Spec.TopologySpreadConstraints[0].TopologyKey)

	assert.Len(t, deployment.Spec.Template.Spec.Tolerations, 1)
	assert.Equal(t, "foo", deployment.Spec.Template.Spec.Tolerations[0].Key)

	managerImage := "ghcr.io/actions/gha-runner-scale-set-controller:dev"

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, managerImage, deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullAlways, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	expectArgs := []string{
		"--auto-scaling-runner-set-only",
		"--auto-scaler-image-pull-secrets=dockerhub",
		"--log-level=info",
		"--log-format=json",
		"--update-strategy=eventual",
		"--listener-metrics-addr=0",
		"--listener-metrics-endpoint=",
		"--metrics-addr=0",
		"--runner-max-concurrent-reconciles=2",
	}

	assert.ElementsMatch(t, expectArgs, deployment.Spec.Template.Spec.Containers[0].Args)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Env, 3)
	assert.Equal(t, "CONTROLLER_MANAGER_CONTAINER_IMAGE", deployment.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, managerImage, deployment.Spec.Template.Spec.Containers[0].Env[0].Value)

	assert.Equal(t, "CONTROLLER_MANAGER_POD_NAMESPACE", deployment.Spec.Template.Spec.Containers[0].Env[1].Name)
	assert.Equal(t, "metadata.namespace", deployment.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath)

	assert.Equal(t, "ENV_VAR_NAME_1", deployment.Spec.Template.Spec.Containers[0].Env[2].Name)
	assert.Equal(t, "ENV_VAR_VALUE_1", deployment.Spec.Template.Spec.Containers[0].Env[2].Value)

	assert.Equal(t, "500m", deployment.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String())
	assert.True(t, *deployment.Spec.Template.Spec.Containers[0].SecurityContext.RunAsNonRoot)
	assert.Equal(t, int64(1000), *deployment.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 2)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name)
	assert.Equal(t, "/tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
	assert.Equal(t, "customMount", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[1].Name)
	assert.Equal(t, "/my/mount/path", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath)
}

func TestTemplate_EnableLeaderElectionRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"replicaCount": "2",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/leader_election_role.yaml"})

	var leaderRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &leaderRole)

	assert.Equal(t, "test-arc-gha-rs-controller-leader-election", leaderRole.Name)
	assert.Equal(t, namespaceName, leaderRole.Namespace)
}

func TestTemplate_EnableLeaderElectionRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"replicaCount": "2",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/leader_election_role_binding.yaml"})

	var leaderRoleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &leaderRoleBinding)

	assert.Equal(t, "test-arc-gha-rs-controller-leader-election", leaderRoleBinding.Name)
	assert.Equal(t, namespaceName, leaderRoleBinding.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller-leader-election", leaderRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-rs-controller", leaderRoleBinding.Subjects[0].Name)
}

func TestTemplate_EnableLeaderElection(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"replicaCount": "2",
			"image.tag":    "dev",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Name)

	assert.Equal(t, int32(2), *deployment.Spec.Replicas)

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/gha-runner-scale-set-controller:dev", deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullIfNotPresent, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	expectedArgs := []string{
		"--auto-scaling-runner-set-only",
		"--enable-leader-election",
		"--leader-election-id=test-arc-gha-rs-controller",
		"--log-level=debug",
		"--log-format=text",
		"--update-strategy=immediate",
		"--listener-metrics-addr=0",
		"--listener-metrics-endpoint=",
		"--metrics-addr=0",
		"--runner-max-concurrent-reconciles=2",
	}

	assert.ElementsMatch(t, expectedArgs, deployment.Spec.Template.Spec.Containers[0].Args)
}

func TestTemplate_ControllerDeployment_ForwardImagePullSecrets(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"imagePullSecrets[0].name": "dockerhub",
			"imagePullSecrets[1].name": "ghcr",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)

	expectedArgs := []string{
		"--auto-scaling-runner-set-only",
		"--auto-scaler-image-pull-secrets=dockerhub,ghcr",
		"--log-level=debug",
		"--log-format=text",
		"--update-strategy=immediate",
		"--listener-metrics-addr=0",
		"--listener-metrics-endpoint=",
		"--metrics-addr=0",
		"--runner-max-concurrent-reconciles=2",
	}

	assert.ElementsMatch(t, expectedArgs, deployment.Spec.Template.Spec.Containers[0].Args)
}

func TestTemplate_ControllerDeployment_WatchSingleNamespace(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"image.tag":                  "dev",
			"flags.watchSingleNamespace": "demo",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Name)
	assert.Equal(t, "gha-rs-controller-"+chart.Version, deployment.Labels["helm.sh/chart"])
	assert.Equal(t, "gha-rs-controller", deployment.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, chart.AppVersion, deployment.Labels["app.kubernetes.io/version"])
	assert.Equal(t, "Helm", deployment.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, namespaceName, deployment.Labels["actions.github.com/controller-service-account-namespace"])
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Labels["actions.github.com/controller-service-account-name"])
	assert.Equal(t, "demo", deployment.Labels["actions.github.com/controller-watch-single-namespace"])

	assert.Equal(t, int32(1), *deployment.Spec.Replicas)

	assert.Equal(t, "gha-rs-controller", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/instance"])

	assert.Equal(t, "gha-rs-controller", deployment.Spec.Template.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Template.Labels["app.kubernetes.io/instance"])

	assert.Equal(t, "manager", deployment.Spec.Template.Annotations["kubectl.kubernetes.io/default-container"])

	assert.Len(t, deployment.Spec.Template.Spec.ImagePullSecrets, 0)
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Spec.Template.Spec.ServiceAccountName)
	assert.Nil(t, deployment.Spec.Template.Spec.SecurityContext)
	assert.Empty(t, deployment.Spec.Template.Spec.PriorityClassName)
	assert.Equal(t, int64(10), *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Len(t, deployment.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Volumes[0].Name)
	assert.NotNil(t, 10, deployment.Spec.Template.Spec.Volumes[0].EmptyDir)

	assert.Len(t, deployment.Spec.Template.Spec.NodeSelector, 0)
	assert.Nil(t, deployment.Spec.Template.Spec.Affinity)
	assert.Len(t, deployment.Spec.Template.Spec.TopologySpreadConstraints, 0)
	assert.Len(t, deployment.Spec.Template.Spec.Tolerations, 0)

	managerImage := "ghcr.io/actions/gha-runner-scale-set-controller:dev"

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/gha-runner-scale-set-controller:dev", deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullIfNotPresent, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	expectedArgs := []string{
		"--auto-scaling-runner-set-only",
		"--log-level=debug",
		"--log-format=text",
		"--watch-single-namespace=demo",
		"--update-strategy=immediate",
		"--listener-metrics-addr=0",
		"--listener-metrics-endpoint=",
		"--metrics-addr=0",
		"--runner-max-concurrent-reconciles=2",
	}

	assert.ElementsMatch(t, expectedArgs, deployment.Spec.Template.Spec.Containers[0].Args)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Env, 2)
	assert.Equal(t, "CONTROLLER_MANAGER_CONTAINER_IMAGE", deployment.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, managerImage, deployment.Spec.Template.Spec.Containers[0].Env[0].Value)

	assert.Equal(t, "CONTROLLER_MANAGER_POD_NAMESPACE", deployment.Spec.Template.Spec.Containers[0].Env[1].Name)
	assert.Equal(t, "metadata.namespace", deployment.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath)

	assert.Empty(t, deployment.Spec.Template.Spec.Containers[0].Resources)
	assert.Nil(t, deployment.Spec.Template.Spec.Containers[0].SecurityContext)
	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name)
	assert.Equal(t, "/tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
}

func TestTemplate_ControllerContainerEnvironmentVariables(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"env[0].Name":                            "ENV_VAR_NAME_1",
			"env[0].Value":                           "ENV_VAR_VALUE_1",
			"env[1].Name":                            "ENV_VAR_NAME_2",
			"env[1].ValueFrom.SecretKeyRef.Key":      "ENV_VAR_NAME_2",
			"env[1].ValueFrom.SecretKeyRef.Name":     "secret-name",
			"env[1].ValueFrom.SecretKeyRef.Optional": "true",
			"env[2].Name":                            "ENV_VAR_NAME_3",
			"env[2].Value":                           "",
			"env[3].Name":                            "ENV_VAR_NAME_4",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller", deployment.Name)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Env, 6)
	assert.Equal(t, "ENV_VAR_NAME_1", deployment.Spec.Template.Spec.Containers[0].Env[2].Name)
	assert.Equal(t, "ENV_VAR_VALUE_1", deployment.Spec.Template.Spec.Containers[0].Env[2].Value)
	assert.Equal(t, "ENV_VAR_NAME_2", deployment.Spec.Template.Spec.Containers[0].Env[3].Name)
	assert.Equal(t, "secret-name", deployment.Spec.Template.Spec.Containers[0].Env[3].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "ENV_VAR_NAME_2", deployment.Spec.Template.Spec.Containers[0].Env[3].ValueFrom.SecretKeyRef.Key)
	assert.True(t, *deployment.Spec.Template.Spec.Containers[0].Env[3].ValueFrom.SecretKeyRef.Optional)
	assert.Equal(t, "ENV_VAR_NAME_3", deployment.Spec.Template.Spec.Containers[0].Env[4].Name)
	assert.Empty(t, deployment.Spec.Template.Spec.Containers[0].Env[4].Value)
	assert.Equal(t, "ENV_VAR_NAME_4", deployment.Spec.Template.Spec.Containers[0].Env[5].Name)
	assert.Empty(t, deployment.Spec.Template.Spec.Containers[0].Env[5].ValueFrom)
}

func TestTemplate_WatchSingleNamespace_NotCreateManagerClusterRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"flags.watchSingleNamespace": "demo",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/manager_cluster_role.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/manager_cluster_role.yaml in chart", "We should get an error because the template should be skipped")
}

func TestTemplate_WatchSingleNamespace_NotManagerClusterRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"serviceAccount.create":      "true",
			"flags.watchSingleNamespace": "demo",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/manager_cluster_role_binding.yaml"})
	assert.ErrorContains(t, err, "could not find template templates/manager_cluster_role_binding.yaml in chart", "We should get an error because the template should be skipped")
}

func TestTemplate_CreateManagerSingleNamespaceRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"flags.watchSingleNamespace": "demo",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_controller_role.yaml"})

	var managerSingleNamespaceControllerRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &managerSingleNamespaceControllerRole)

	assert.Equal(t, "test-arc-gha-rs-controller-single-namespace", managerSingleNamespaceControllerRole.Name)
	assert.Equal(t, namespaceName, managerSingleNamespaceControllerRole.Namespace)
	assert.Equal(t, 10, len(managerSingleNamespaceControllerRole.Rules))

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_watch_role.yaml"})

	var managerSingleNamespaceWatchRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &managerSingleNamespaceWatchRole)

	assert.Equal(t, "test-arc-gha-rs-controller-single-namespace-watch", managerSingleNamespaceWatchRole.Name)
	assert.Equal(t, "demo", managerSingleNamespaceWatchRole.Namespace)
	assert.Equal(t, 14, len(managerSingleNamespaceWatchRole.Rules))
}

func TestTemplate_ManagerSingleNamespaceRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"flags.watchSingleNamespace": "demo",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_controller_role_binding.yaml"})

	var managerSingleNamespaceControllerRoleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &managerSingleNamespaceControllerRoleBinding)

	assert.Equal(t, "test-arc-gha-rs-controller-single-namespace", managerSingleNamespaceControllerRoleBinding.Name)
	assert.Equal(t, namespaceName, managerSingleNamespaceControllerRoleBinding.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller-single-namespace", managerSingleNamespaceControllerRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-rs-controller", managerSingleNamespaceControllerRoleBinding.Subjects[0].Name)
	assert.Equal(t, namespaceName, managerSingleNamespaceControllerRoleBinding.Subjects[0].Namespace)

	output = helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_single_namespace_watch_role_binding.yaml"})

	var managerSingleNamespaceWatchRoleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &managerSingleNamespaceWatchRoleBinding)

	assert.Equal(t, "test-arc-gha-rs-controller-single-namespace-watch", managerSingleNamespaceWatchRoleBinding.Name)
	assert.Equal(t, "demo", managerSingleNamespaceWatchRoleBinding.Namespace)
	assert.Equal(t, "test-arc-gha-rs-controller-single-namespace-watch", managerSingleNamespaceWatchRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-rs-controller", managerSingleNamespaceWatchRoleBinding.Subjects[0].Name)
	assert.Equal(t, namespaceName, managerSingleNamespaceWatchRoleBinding.Subjects[0].Namespace)
}

func TestControllerDeployment_MetricsPorts(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"image.tag":                     "dev",
			"metrics.controllerManagerAddr": ":8080",
			"metrics.listenerAddr":          ":8081",
			"metrics.listenerEndpoint":      "/metrics",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	require.Len(t, deployment.Spec.Template.Spec.Containers, 1, "Expected one container")
	container := deployment.Spec.Template.Spec.Containers[0]
	assert.Len(t, container.Ports, 1)
	port := container.Ports[0]
	assert.Equal(t, corev1.Protocol("TCP"), port.Protocol)
	assert.Equal(t, int32(8080), port.ContainerPort)

	metricsFlags := map[string]*struct {
		expect    string
		frequency int
	}{
		"--listener-metrics-addr": {
			expect: ":8081",
		},
		"--listener-metrics-endpoint": {
			expect: "/metrics",
		},
		"--metrics-addr": {
			expect: ":8080",
		},
	}
	for _, cmd := range container.Args {
		s := strings.Split(cmd, "=")
		if len(s) != 2 {
			continue
		}
		flag, ok := metricsFlags[s[0]]
		if !ok {
			continue
		}
		flag.frequency++
		assert.Equal(t, flag.expect, s[1])
	}

	for key, value := range metricsFlags {
		assert.Equal(t, value.frequency, 1, fmt.Sprintf("frequency of %q is not 1", key))
	}
}

func TestDeployment_excludeLabelPropagationPrefixes(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	chartContent, err := os.ReadFile(filepath.Join(helmChartPath, "Chart.yaml"))
	require.NoError(t, err)

	chart := new(Chart)
	err = yaml.Unmarshal(chartContent, chart)
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		Logger: logger.Discard,
		SetValues: map[string]string{
			"flags.excludeLabelPropagationPrefixes[0]": "prefix.com/",
			"flags.excludeLabelPropagationPrefixes[1]": "complete.io/label",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	require.Len(t, deployment.Spec.Template.Spec.Containers, 1, "Expected one container")
	container := deployment.Spec.Template.Spec.Containers[0]

	assert.Contains(t, container.Args, "--exclude-label-propagation-prefix=prefix.com/")
	assert.Contains(t, container.Args, "--exclude-label-propagation-prefix=complete.io/label")
}
func TestNamespaceOverride(t *testing.T) {
	t.Parallel()

	chartPath := "../../gha-runner-scale-set-controller"

	releaseName := "test"
	releaseNamespace := "test-" + strings.ToLower(random.UniqueId())
	namespaceOverride := "test-" + strings.ToLower(random.UniqueId())

	tt := map[string]struct {
		file          string
		options       *helm.Options
		wantNamespace string
	}{
		"deployment": {
			file: "deployment.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride": namespaceOverride,
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"leader_election_role_binding": {
			file: "leader_election_role_binding.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride": namespaceOverride,
					"replicaCount":      "2",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"leader_election_role": {
			file: "leader_election_role.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride": namespaceOverride,
					"replicaCount":      "2",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"manager_listener_role_binding": {
			file: "manager_listener_role_binding.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride": namespaceOverride,
					"replicaCount":      "2",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"manager_listener_role": {
			file: "manager_listener_role.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride": namespaceOverride,
					"replicaCount":      "2",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"manager_single_namespace_controller_role": {
			file: "manager_single_namespace_controller_role.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride":          namespaceOverride,
					"flags.watchSingleNamespace": "true",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"manager_single_namespace_controller_role_binding": {
			file: "manager_single_namespace_controller_role_binding.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride":          namespaceOverride,
					"flags.watchSingleNamespace": "true",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: namespaceOverride,
		},
		"manager_single_namespace_watch_role": {
			file: "manager_single_namespace_watch_role.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride":          namespaceOverride,
					"flags.watchSingleNamespace": "target-ns",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: "target-ns",
		},
		"manager_single_namespace_watch_role_binding": {
			file: "manager_single_namespace_watch_role_binding.yaml",
			options: &helm.Options{
				Logger: logger.Discard,
				SetValues: map[string]string{
					"namespaceOverride":          namespaceOverride,
					"flags.watchSingleNamespace": "target-ns",
				},
				KubectlOptions: k8s.NewKubectlOptions("", "", releaseNamespace),
			},
			wantNamespace: "target-ns",
		},
	}

	for name, tc := range tt {
		c := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			templateFile := filepath.Join("./templates", c.file)

			output, err := helm.RenderTemplateE(t, c.options, chartPath, releaseName, []string{templateFile})
			if err != nil {
				t.Errorf("Error rendering template %s from chart %s: %s", c.file, chartPath, err)
			}

			type object struct {
				Metadata metav1.ObjectMeta
			}
			var renderedObject object
			helm.UnmarshalK8SYaml(t, output, &renderedObject)
			assert.Equal(t, tc.wantNamespace, renderedObject.Metadata.Namespace)
		})
	}
}
