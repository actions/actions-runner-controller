package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller", serviceAccount.Name)
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
		SetValues: map[string]string{
			"serviceAccount.create":          "false",
			"serviceAccount.annotations.foo": "bar",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	_, err = helm.RenderTemplateE(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})
	assert.ErrorContains(t, err, "serviceAccount.name must be set if serviceAccount.create is false", "We should get an error because the default service account cannot be used")
}

func TestTemplate_CreateManagerRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		SetValues:      map[string]string{},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_role.yaml"})

	var managerRole rbacv1.ClusterRole
	helm.UnmarshalK8SYaml(t, output, &managerRole)

	assert.Empty(t, managerRole.Namespace, "ClusterRole should not have a namespace")
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller-manager-role", managerRole.Name)
	assert.Equal(t, 18, len(managerRole.Rules))
}

func TestTemplate_ManagerRoleBinding(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		SetValues: map[string]string{
			"serviceAccount.create": "true",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/manager_role_binding.yaml"})

	var managerRoleBinding rbacv1.ClusterRoleBinding
	helm.UnmarshalK8SYaml(t, output, &managerRoleBinding)

	assert.Empty(t, managerRoleBinding.Namespace, "ClusterRoleBinding should not have a namespace")
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller-manager-rolebinding", managerRoleBinding.Name)
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller-manager-role", managerRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller", managerRoleBinding.Subjects[0].Name)
	assert.Equal(t, namespaceName, managerRoleBinding.Subjects[0].Namespace)
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
		SetValues: map[string]string{
			"image.tag": "dev",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller", deployment.Name)
	assert.Equal(t, "gha-runner-scale-set-controller-"+chart.Version, deployment.Labels["helm.sh/chart"])
	assert.Equal(t, "gha-runner-scale-set-controller", deployment.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, chart.AppVersion, deployment.Labels["app.kubernetes.io/version"])
	assert.Equal(t, "Helm", deployment.Labels["app.kubernetes.io/managed-by"])

	assert.Equal(t, int32(1), *deployment.Spec.Replicas)

	assert.Equal(t, "gha-runner-scale-set-controller", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/instance"])

	assert.Equal(t, "gha-runner-scale-set-controller", deployment.Spec.Template.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Template.Labels["app.kubernetes.io/instance"])

	assert.Equal(t, "manager", deployment.Spec.Template.Annotations["kubectl.kubernetes.io/default-container"])

	assert.Len(t, deployment.Spec.Template.Spec.ImagePullSecrets, 0)
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller", deployment.Spec.Template.Spec.ServiceAccountName)
	assert.Nil(t, deployment.Spec.Template.Spec.SecurityContext)
	assert.Empty(t, deployment.Spec.Template.Spec.PriorityClassName)
	assert.Equal(t, int64(10), *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Len(t, deployment.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Volumes[0].Name)
	assert.NotNil(t, 10, deployment.Spec.Template.Spec.Volumes[0].EmptyDir)

	assert.Len(t, deployment.Spec.Template.Spec.NodeSelector, 0)
	assert.Nil(t, deployment.Spec.Template.Spec.Affinity)
	assert.Len(t, deployment.Spec.Template.Spec.Tolerations, 0)

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/gha-runner-scale-set-controller:dev", deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullIfNotPresent, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Args, 2)
	assert.Equal(t, "--auto-scaling-runner-set-only", deployment.Spec.Template.Spec.Containers[0].Args[0])
	assert.Equal(t, "--log-level=debug", deployment.Spec.Template.Spec.Containers[0].Args[1])

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Env, 2)
	assert.Equal(t, "CONTROLLER_MANAGER_POD_NAME", deployment.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, "metadata.name", deployment.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.FieldRef.FieldPath)

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
		SetValues: map[string]string{
			"labels.foo":                   "bar",
			"labels.github":                "actions",
			"replicaCount":                 "1",
			"image.pullPolicy":             "Always",
			"image.tag":                    "dev",
			"imagePullSecrets[0].name":     "dockerhub",
			"nameOverride":                 "gha-runner-scale-set-controller-override",
			"fullnameOverride":             "gha-runner-scale-set-controller-fullname-override",
			"serviceAccount.name":          "gha-runner-scale-set-controller-sa",
			"podAnnotations.foo":           "bar",
			"podSecurityContext.fsGroup":   "1000",
			"securityContext.runAsUser":    "1000",
			"securityContext.runAsNonRoot": "true",
			"resources.limits.cpu":         "500m",
			"nodeSelector.foo":             "bar",
			"tolerations[0].key":           "foo",
			"affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key":      "foo",
			"affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator": "bar",
			"priorityClassName": "test-priority-class",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/deployment.yaml"})

	var deployment appsv1.Deployment
	helm.UnmarshalK8SYaml(t, output, &deployment)

	assert.Equal(t, namespaceName, deployment.Namespace)
	assert.Equal(t, "gha-runner-scale-set-controller-fullname-override", deployment.Name)
	assert.Equal(t, "gha-runner-scale-set-controller-"+chart.Version, deployment.Labels["helm.sh/chart"])
	assert.Equal(t, "gha-runner-scale-set-controller-override", deployment.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, chart.AppVersion, deployment.Labels["app.kubernetes.io/version"])
	assert.Equal(t, "Helm", deployment.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "bar", deployment.Labels["foo"])
	assert.Equal(t, "actions", deployment.Labels["github"])

	assert.Equal(t, int32(1), *deployment.Spec.Replicas)

	assert.Equal(t, "gha-runner-scale-set-controller-override", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Selector.MatchLabels["app.kubernetes.io/instance"])

	assert.Equal(t, "gha-runner-scale-set-controller-override", deployment.Spec.Template.Labels["app.kubernetes.io/name"])
	assert.Equal(t, "test-arc", deployment.Spec.Template.Labels["app.kubernetes.io/instance"])

	assert.Equal(t, "bar", deployment.Spec.Template.Annotations["foo"])
	assert.Equal(t, "manager", deployment.Spec.Template.Annotations["kubectl.kubernetes.io/default-container"])

	assert.Len(t, deployment.Spec.Template.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "dockerhub", deployment.Spec.Template.Spec.ImagePullSecrets[0].Name)
	assert.Equal(t, "gha-runner-scale-set-controller-sa", deployment.Spec.Template.Spec.ServiceAccountName)
	assert.Equal(t, int64(1000), *deployment.Spec.Template.Spec.SecurityContext.FSGroup)
	assert.Equal(t, "test-priority-class", deployment.Spec.Template.Spec.PriorityClassName)
	assert.Equal(t, int64(10), *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Len(t, deployment.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Volumes[0].Name)
	assert.NotNil(t, 10, deployment.Spec.Template.Spec.Volumes[0].EmptyDir)

	assert.Len(t, deployment.Spec.Template.Spec.NodeSelector, 1)
	assert.Equal(t, "bar", deployment.Spec.Template.Spec.NodeSelector["foo"])

	assert.NotNil(t, deployment.Spec.Template.Spec.Affinity.NodeAffinity)
	assert.Equal(t, "foo", deployment.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Key)
	assert.Equal(t, "bar", string(deployment.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Operator))

	assert.Len(t, deployment.Spec.Template.Spec.Tolerations, 1)
	assert.Equal(t, "foo", deployment.Spec.Template.Spec.Tolerations[0].Key)

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/gha-runner-scale-set-controller:dev", deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullAlways, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Args, 3)
	assert.Equal(t, "--auto-scaling-runner-set-only", deployment.Spec.Template.Spec.Containers[0].Args[0])
	assert.Equal(t, "--auto-scaler-image-pull-secrets=dockerhub", deployment.Spec.Template.Spec.Containers[0].Args[1])
	assert.Equal(t, "--log-level=debug", deployment.Spec.Template.Spec.Containers[0].Args[2])

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Env, 2)
	assert.Equal(t, "CONTROLLER_MANAGER_POD_NAME", deployment.Spec.Template.Spec.Containers[0].Env[0].Name)
	assert.Equal(t, "metadata.name", deployment.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.FieldRef.FieldPath)

	assert.Equal(t, "CONTROLLER_MANAGER_POD_NAMESPACE", deployment.Spec.Template.Spec.Containers[0].Env[1].Name)
	assert.Equal(t, "metadata.namespace", deployment.Spec.Template.Spec.Containers[0].Env[1].ValueFrom.FieldRef.FieldPath)

	assert.Equal(t, "500m", deployment.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String())
	assert.True(t, *deployment.Spec.Template.Spec.Containers[0].SecurityContext.RunAsNonRoot)
	assert.Equal(t, int64(1000), *deployment.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 1)
	assert.Equal(t, "tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name)
	assert.Equal(t, "/tmp", deployment.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
}

func TestTemplate_EnableLeaderElectionRole(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
		SetValues: map[string]string{
			"replicaCount": "2",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/leader_election_role.yaml"})

	var leaderRole rbacv1.Role
	helm.UnmarshalK8SYaml(t, output, &leaderRole)

	assert.Equal(t, "test-arc-gha-runner-scale-set-controller-leader-election-role", leaderRole.Name)
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
		SetValues: map[string]string{
			"replicaCount": "2",
		},
		KubectlOptions: k8s.NewKubectlOptions("", "", namespaceName),
	}

	output := helm.RenderTemplate(t, options, helmChartPath, releaseName, []string{"templates/leader_election_role_binding.yaml"})

	var leaderRoleBinding rbacv1.RoleBinding
	helm.UnmarshalK8SYaml(t, output, &leaderRoleBinding)

	assert.Equal(t, "test-arc-gha-runner-scale-set-controller-leader-election-rolebinding", leaderRoleBinding.Name)
	assert.Equal(t, namespaceName, leaderRoleBinding.Namespace)
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller-leader-election-role", leaderRoleBinding.RoleRef.Name)
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller", leaderRoleBinding.Subjects[0].Name)
}

func TestTemplate_EnableLeaderElection(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
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
	assert.Equal(t, "test-arc-gha-runner-scale-set-controller", deployment.Name)

	assert.Equal(t, int32(2), *deployment.Spec.Replicas)

	assert.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "manager", deployment.Spec.Template.Spec.Containers[0].Name)
	assert.Equal(t, "ghcr.io/actions/gha-runner-scale-set-controller:dev", deployment.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, corev1.PullIfNotPresent, deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy)

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Command, 1)
	assert.Equal(t, "/manager", deployment.Spec.Template.Spec.Containers[0].Command[0])

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Args, 4)
	assert.Equal(t, "--auto-scaling-runner-set-only", deployment.Spec.Template.Spec.Containers[0].Args[0])
	assert.Equal(t, "--enable-leader-election", deployment.Spec.Template.Spec.Containers[0].Args[1])
	assert.Equal(t, "--leader-election-id=test-arc-gha-runner-scale-set-controller", deployment.Spec.Template.Spec.Containers[0].Args[2])
	assert.Equal(t, "--log-level=debug", deployment.Spec.Template.Spec.Containers[0].Args[3])
}

func TestTemplate_ControllerDeployment_ForwardImagePullSecrets(t *testing.T) {
	t.Parallel()

	// Path to the helm chart we will test
	helmChartPath, err := filepath.Abs("../../gha-runner-scale-set-controller")
	require.NoError(t, err)

	releaseName := "test-arc"
	namespaceName := "test-" + strings.ToLower(random.UniqueId())

	options := &helm.Options{
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

	assert.Len(t, deployment.Spec.Template.Spec.Containers[0].Args, 3)
	assert.Equal(t, "--auto-scaling-runner-set-only", deployment.Spec.Template.Spec.Containers[0].Args[0])
	assert.Equal(t, "--auto-scaler-image-pull-secrets=dockerhub,ghcr", deployment.Spec.Template.Spec.Containers[0].Args[1])
	assert.Equal(t, "--log-level=debug", deployment.Spec.Template.Spec.Containers[0].Args[2])
}
