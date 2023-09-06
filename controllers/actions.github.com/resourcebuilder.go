package actionsgithubcom

import (
	"context"
	"fmt"
	"math"
	"net"
	"strconv"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/hash"
	"github.com/actions/actions-runner-controller/logging"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// secret constants
const (
	jitTokenKey = "jitToken"
)

var commonLabelKeys = [...]string{
	LabelKeyKubernetesPartOf,
	LabelKeyKubernetesComponent,
	LabelKeyKubernetesVersion,
	LabelKeyGitHubScaleSetName,
	LabelKeyGitHubScaleSetNamespace,
	LabelKeyGitHubEnterprise,
	LabelKeyGitHubOrganization,
	LabelKeyGitHubRepository,
}

var reservedListenerContainerEnvKeys = map[string]struct{}{
	"GITHUB_CONFIGURE_URL":                  {},
	"GITHUB_EPHEMERAL_RUNNER_SET_NAMESPACE": {},
	"GITHUB_EPHEMERAL_RUNNER_SET_NAME":      {},
	"GITHUB_MAX_RUNNERS":                    {},
	"GITHUB_MIN_RUNNERS":                    {},
	"GITHUB_RUNNER_SCALE_SET_ID":            {},
	"GITHUB_RUNNER_LOG_LEVEL":               {},
	"GITHUB_RUNNER_LOG_FORMAT":              {},
	"GITHUB_TOKEN":                          {},
	"GITHUB_APP_ID":                         {},
	"GITHUB_APP_INSTALLATION_ID":            {},
	"GITHUB_APP_PRIVATE_KEY":                {},
}

const labelValueKubernetesPartOf = "gha-runner-scale-set"

var scaleSetListenerLogLevel = DefaultScaleSetListenerLogLevel
var scaleSetListenerLogFormat = DefaultScaleSetListenerLogFormat

var scaleSetListenerImagePullPolicy = DefaultScaleSetListenerImagePullPolicy

func SetListenerImagePullPolicy(pullPolicy string) bool {
	switch p := corev1.PullPolicy(pullPolicy); p {
	case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
		scaleSetListenerImagePullPolicy = p
		return true
	default:
		return false
	}
}

func SetListenerLoggingParameters(level string, format string) bool {
	switch level {
	case logging.LogLevelDebug, logging.LogLevelInfo, logging.LogLevelWarn, logging.LogLevelError:
	default:
		return false
	}

	switch format {
	case logging.LogFormatJSON, logging.LogFormatText:
	default:
		return false
	}

	scaleSetListenerLogLevel = level
	scaleSetListenerLogFormat = format
	return true
}

type resourceBuilder struct{}

func (b *resourceBuilder) newAutoScalingListener(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, namespace, image string, imagePullSecrets []corev1.LocalObjectReference) (*v1alpha1.AutoscalingListener, error) {
	runnerScaleSetId, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIdAnnotationKey])
	if err != nil {
		return nil, err
	}

	effectiveMinRunners := 0
	effectiveMaxRunners := math.MaxInt32
	if autoscalingRunnerSet.Spec.MaxRunners != nil {
		effectiveMaxRunners = *autoscalingRunnerSet.Spec.MaxRunners
	}
	if autoscalingRunnerSet.Spec.MinRunners != nil {
		effectiveMinRunners = *autoscalingRunnerSet.Spec.MinRunners
	}

	labels := map[string]string{
		LabelKeyGitHubScaleSetNamespace: autoscalingRunnerSet.Namespace,
		LabelKeyGitHubScaleSetName:      autoscalingRunnerSet.Name,
		LabelKeyKubernetesPartOf:        labelValueKubernetesPartOf,
		LabelKeyKubernetesComponent:     "runner-scale-set-listener",
		LabelKeyKubernetesVersion:       autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
		labelKeyRunnerSpecHash:          autoscalingRunnerSet.ListenerSpecHash(),
	}

	if err := applyGitHubURLLabels(autoscalingRunnerSet.Spec.GitHubConfigUrl, labels); err != nil {
		return nil, fmt.Errorf("failed to apply GitHub URL labels: %v", err)
	}

	autoscalingListener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerName(autoscalingRunnerSet),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: v1alpha1.AutoscalingListenerSpec{
			GitHubConfigUrl:               autoscalingRunnerSet.Spec.GitHubConfigUrl,
			GitHubConfigSecret:            autoscalingRunnerSet.Spec.GitHubConfigSecret,
			RunnerScaleSetId:              runnerScaleSetId,
			AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
			AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
			EphemeralRunnerSetName:        ephemeralRunnerSet.Name,
			MinRunners:                    effectiveMinRunners,
			MaxRunners:                    effectiveMaxRunners,
			Image:                         image,
			ImagePullSecrets:              imagePullSecrets,
			Proxy:                         autoscalingRunnerSet.Spec.Proxy,
			GitHubServerTLS:               autoscalingRunnerSet.Spec.GitHubServerTLS,
			Template:                      autoscalingRunnerSet.Spec.ListenerTemplate,
		},
	}

	return autoscalingListener, nil
}

type listenerMetricsServerConfig struct {
	addr     string
	endpoint string
}

func (b *resourceBuilder) newScaleSetListenerPod(autoscalingListener *v1alpha1.AutoscalingListener, serviceAccount *corev1.ServiceAccount, secret *corev1.Secret, metricsConfig *listenerMetricsServerConfig, envs ...corev1.EnvVar) (*corev1.Pod, error) {
	listenerEnv := []corev1.EnvVar{
		{
			Name:  "GITHUB_CONFIGURE_URL",
			Value: autoscalingListener.Spec.GitHubConfigUrl,
		},
		{
			Name:  "GITHUB_EPHEMERAL_RUNNER_SET_NAMESPACE",
			Value: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
		},
		{
			Name:  "GITHUB_EPHEMERAL_RUNNER_SET_NAME",
			Value: autoscalingListener.Spec.EphemeralRunnerSetName,
		},
		{
			Name:  "GITHUB_MAX_RUNNERS",
			Value: strconv.Itoa(autoscalingListener.Spec.MaxRunners),
		},
		{
			Name:  "GITHUB_MIN_RUNNERS",
			Value: strconv.Itoa(autoscalingListener.Spec.MinRunners),
		},
		{
			Name:  "GITHUB_RUNNER_SCALE_SET_ID",
			Value: strconv.Itoa(autoscalingListener.Spec.RunnerScaleSetId),
		},
		{
			Name:  "GITHUB_RUNNER_SCALE_SET_NAME",
			Value: autoscalingListener.Spec.AutoscalingRunnerSetName,
		},
		{
			Name:  "GITHUB_RUNNER_LOG_LEVEL",
			Value: scaleSetListenerLogLevel,
		},
		{
			Name:  "GITHUB_RUNNER_LOG_FORMAT",
			Value: scaleSetListenerLogFormat,
		},
	}
	listenerEnv = append(listenerEnv, envs...)

	if _, ok := secret.Data["github_token"]; ok {
		listenerEnv = append(listenerEnv, corev1.EnvVar{
			Name: "GITHUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
					Key: "github_token",
				},
			},
		})
	}

	if _, ok := secret.Data["github_app_id"]; ok {
		listenerEnv = append(listenerEnv, corev1.EnvVar{
			Name: "GITHUB_APP_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
					Key: "github_app_id",
				},
			},
		})
	}

	if _, ok := secret.Data["github_app_installation_id"]; ok {
		listenerEnv = append(listenerEnv, corev1.EnvVar{
			Name: "GITHUB_APP_INSTALLATION_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
					Key: "github_app_installation_id",
				},
			},
		})
	}

	if _, ok := secret.Data["github_app_private_key"]; ok {
		listenerEnv = append(listenerEnv, corev1.EnvVar{
			Name: "GITHUB_APP_PRIVATE_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
					Key: "github_app_private_key",
				},
			},
		})
	}

	var ports []corev1.ContainerPort
	if metricsConfig != nil && len(metricsConfig.addr) != 0 {
		listenerEnv = append(
			listenerEnv,
			corev1.EnvVar{
				Name:  "GITHUB_METRICS_ADDR",
				Value: metricsConfig.addr,
			},
			corev1.EnvVar{
				Name:  "GITHUB_METRICS_ENDPOINT",
				Value: metricsConfig.endpoint,
			},
		)

		_, portStr, err := net.SplitHostPort(metricsConfig.addr)
		if err != nil {
			return nil, fmt.Errorf("failed to split host:port for metrics address: %v", err)
		}
		port, err := strconv.ParseInt(portStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to convert port %q to int32: %v", portStr, err)
		}
		ports = append(
			ports,
			corev1.ContainerPort{
				ContainerPort: int32(port),
				Protocol:      corev1.ProtocolTCP,
				Name:          "metrics",
			},
		)
	}

	podSpec := corev1.PodSpec{
		ServiceAccountName: serviceAccount.Name,
		Containers: []corev1.Container{
			{
				Name:            autoscalingListenerContainerName,
				Image:           autoscalingListener.Spec.Image,
				Env:             listenerEnv,
				ImagePullPolicy: scaleSetListenerImagePullPolicy,
				Command: []string{
					"/github-runnerscaleset-listener",
				},
				Ports: ports,
			},
		},
		ImagePullSecrets: autoscalingListener.Spec.ImagePullSecrets,
		RestartPolicy:    corev1.RestartPolicyNever,
	}

	labels := make(map[string]string, len(autoscalingListener.Labels))
	for key, val := range autoscalingListener.Labels {
		labels[key] = val
	}

	newRunnerScaleSetListenerPod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalingListener.Name,
			Namespace: autoscalingListener.Namespace,
			Labels:    labels,
		},
		Spec: podSpec,
	}

	if autoscalingListener.Spec.Template != nil {
		mergeListenerPodWithTemplate(newRunnerScaleSetListenerPod, autoscalingListener.Spec.Template)
	}

	return newRunnerScaleSetListenerPod, nil
}

func mergeListenerPodWithTemplate(pod *corev1.Pod, tmpl *corev1.PodTemplateSpec) {
	// apply metadata
	for k, v := range tmpl.Annotations {
		if _, ok := pod.Annotations[k]; !ok {
			pod.Annotations[k] = v
		}
	}

	for k, v := range tmpl.Labels {
		if _, ok := pod.Labels[k]; !ok {
			pod.Labels[k] = v
		}
	}

	// apply spec

	// apply container
	listenerContainer := &pod.Spec.Containers[0] // if this panics, we have bigger problems
	for i := range tmpl.Spec.Containers {
		c := &tmpl.Spec.Containers[i]

		switch c.Name {
		case "listener":
			mergeListenerContainer(listenerContainer, c)
		default:
			pod.Spec.Containers = append(pod.Spec.Containers, *c)
		}
	}

	// apply pod related spec
	// NOTE: fields that should be gnored
	// - service account based fields

	if tmpl.Spec.RestartPolicy != "" {
		pod.Spec.RestartPolicy = tmpl.Spec.RestartPolicy
	}

	if tmpl.Spec.ImagePullSecrets != nil {
		pod.Spec.ImagePullSecrets = tmpl.Spec.ImagePullSecrets
	}

	pod.Spec.Volumes = tmpl.Spec.Volumes
	pod.Spec.InitContainers = tmpl.Spec.InitContainers
	pod.Spec.EphemeralContainers = tmpl.Spec.EphemeralContainers
	pod.Spec.TerminationGracePeriodSeconds = tmpl.Spec.TerminationGracePeriodSeconds
	pod.Spec.ActiveDeadlineSeconds = tmpl.Spec.ActiveDeadlineSeconds
	pod.Spec.DNSPolicy = tmpl.Spec.DNSPolicy
	pod.Spec.NodeSelector = tmpl.Spec.NodeSelector
	pod.Spec.NodeName = tmpl.Spec.NodeName
	pod.Spec.HostNetwork = tmpl.Spec.HostNetwork
	pod.Spec.HostPID = tmpl.Spec.HostPID
	pod.Spec.HostIPC = tmpl.Spec.HostIPC
	pod.Spec.ShareProcessNamespace = tmpl.Spec.ShareProcessNamespace
	pod.Spec.SecurityContext = tmpl.Spec.SecurityContext
	pod.Spec.Hostname = tmpl.Spec.Hostname
	pod.Spec.Subdomain = tmpl.Spec.Subdomain
	pod.Spec.Affinity = tmpl.Spec.Affinity
	pod.Spec.SchedulerName = tmpl.Spec.SchedulerName
	pod.Spec.Tolerations = tmpl.Spec.Tolerations
	pod.Spec.HostAliases = tmpl.Spec.HostAliases
	pod.Spec.PriorityClassName = tmpl.Spec.PriorityClassName
	pod.Spec.Priority = tmpl.Spec.Priority
	pod.Spec.DNSConfig = tmpl.Spec.DNSConfig
	pod.Spec.ReadinessGates = tmpl.Spec.ReadinessGates
	pod.Spec.RuntimeClassName = tmpl.Spec.RuntimeClassName
	pod.Spec.EnableServiceLinks = tmpl.Spec.EnableServiceLinks
	pod.Spec.PreemptionPolicy = tmpl.Spec.PreemptionPolicy
	pod.Spec.Overhead = tmpl.Spec.Overhead
	pod.Spec.TopologySpreadConstraints = tmpl.Spec.TopologySpreadConstraints
	pod.Spec.SetHostnameAsFQDN = tmpl.Spec.SetHostnameAsFQDN
	pod.Spec.OS = tmpl.Spec.OS
	pod.Spec.HostUsers = tmpl.Spec.HostUsers
	pod.Spec.SchedulingGates = tmpl.Spec.SchedulingGates
	pod.Spec.ResourceClaims = tmpl.Spec.ResourceClaims
}

func mergeListenerContainer(base, from *corev1.Container) {
	// name should not be modified

	if from.Image != "" {
		base.Image = from.Image
	}

	if len(from.Command) > 0 {
		base.Command = from.Command
	}

	for _, v := range from.Env {
		if _, ok := reservedListenerContainerEnvKeys[v.Name]; !ok {
			base.Env = append(base.Env, v)
		}
	}

	if from.ImagePullPolicy != "" {
		base.ImagePullPolicy = from.ImagePullPolicy
	}

	base.Args = append(base.Args, from.Args...)
	base.WorkingDir = from.WorkingDir
	base.Ports = append(base.Ports, from.Ports...)
	base.EnvFrom = append(base.EnvFrom, from.EnvFrom...)
	base.Resources = from.Resources
	base.VolumeMounts = append(base.VolumeMounts, from.VolumeMounts...)
	base.VolumeDevices = append(base.VolumeDevices, from.VolumeDevices...)
	base.LivenessProbe = from.LivenessProbe
	base.ReadinessProbe = from.ReadinessProbe
	base.StartupProbe = from.StartupProbe
	base.Lifecycle = from.Lifecycle
	base.TerminationMessagePath = from.TerminationMessagePath
	base.TerminationMessagePolicy = from.TerminationMessagePolicy
	base.ImagePullPolicy = from.ImagePullPolicy
	base.SecurityContext = from.SecurityContext
	base.Stdin = from.Stdin
	base.StdinOnce = from.StdinOnce
	base.TTY = from.TTY
}

func (b *resourceBuilder) newScaleSetListenerServiceAccount(autoscalingListener *v1alpha1.AutoscalingListener) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerServiceAccountName(autoscalingListener),
			Namespace: autoscalingListener.Namespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
			},
		},
	}
}

func (b *resourceBuilder) newScaleSetListenerRole(autoscalingListener *v1alpha1.AutoscalingListener) *rbacv1.Role {
	rules := rulesForListenerRole([]string{autoscalingListener.Spec.EphemeralRunnerSetName})
	rulesHash := hash.ComputeTemplateHash(&rules)
	newRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerRoleName(autoscalingListener),
			Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
				labelKeyListenerNamespace:       autoscalingListener.Namespace,
				labelKeyListenerName:            autoscalingListener.Name,
				"role-policy-rules-hash":        rulesHash,
			},
		},
		Rules: rules,
	}

	return newRole
}

func (b *resourceBuilder) newScaleSetListenerRoleBinding(autoscalingListener *v1alpha1.AutoscalingListener, listenerRole *rbacv1.Role, serviceAccount *corev1.ServiceAccount) *rbacv1.RoleBinding {
	roleRef := rbacv1.RoleRef{
		Kind: "Role",
		Name: listenerRole.Name,
	}
	roleRefHash := hash.ComputeTemplateHash(&roleRef)

	subjects := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Namespace: serviceAccount.Namespace,
			Name:      serviceAccount.Name,
		},
	}
	subjectHash := hash.ComputeTemplateHash(&subjects)

	newRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerRoleName(autoscalingListener),
			Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
				labelKeyListenerNamespace:       autoscalingListener.Namespace,
				labelKeyListenerName:            autoscalingListener.Name,
				"role-binding-role-ref-hash":    roleRefHash,
				"role-binding-subject-hash":     subjectHash,
			},
		},
		RoleRef:  roleRef,
		Subjects: subjects,
	}

	return newRoleBinding
}

func (b *resourceBuilder) newScaleSetListenerSecretMirror(autoscalingListener *v1alpha1.AutoscalingListener, secret *corev1.Secret) *corev1.Secret {
	dataHash := hash.ComputeTemplateHash(&secret.Data)

	newListenerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerSecretMirrorName(autoscalingListener),
			Namespace: autoscalingListener.Namespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
				"secret-data-hash":              dataHash,
			},
		},
		Data: secret.DeepCopy().Data,
	}

	return newListenerSecret
}

func (b *resourceBuilder) newEphemeralRunnerSet(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) (*v1alpha1.EphemeralRunnerSet, error) {
	runnerScaleSetId, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIdAnnotationKey])
	if err != nil {
		return nil, err
	}
	runnerSpecHash := autoscalingRunnerSet.RunnerSetSpecHash()

	labels := map[string]string{
		labelKeyRunnerSpecHash:          runnerSpecHash,
		LabelKeyKubernetesPartOf:        labelValueKubernetesPartOf,
		LabelKeyKubernetesComponent:     "runner-set",
		LabelKeyKubernetesVersion:       autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
		LabelKeyGitHubScaleSetName:      autoscalingRunnerSet.Name,
		LabelKeyGitHubScaleSetNamespace: autoscalingRunnerSet.Namespace,
	}

	if err := applyGitHubURLLabels(autoscalingRunnerSet.Spec.GitHubConfigUrl, labels); err != nil {
		return nil, fmt.Errorf("failed to apply GitHub URL labels: %v", err)
	}

	newAnnotations := map[string]string{
		AnnotationKeyGitHubRunnerGroupName: autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName],
	}

	newEphemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: autoscalingRunnerSet.ObjectMeta.Name + "-",
			Namespace:    autoscalingRunnerSet.ObjectMeta.Namespace,
			Labels:       labels,
			Annotations:  newAnnotations,
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: 0,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				RunnerScaleSetId:   runnerScaleSetId,
				GitHubConfigUrl:    autoscalingRunnerSet.Spec.GitHubConfigUrl,
				GitHubConfigSecret: autoscalingRunnerSet.Spec.GitHubConfigSecret,
				Proxy:              autoscalingRunnerSet.Spec.Proxy,
				GitHubServerTLS:    autoscalingRunnerSet.Spec.GitHubServerTLS,
				PodTemplateSpec:    autoscalingRunnerSet.Spec.Template,
			},
		},
	}

	return newEphemeralRunnerSet, nil
}

func (b *resourceBuilder) newEphemeralRunner(ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet) *v1alpha1.EphemeralRunner {
	labels := make(map[string]string)
	for _, key := range commonLabelKeys {
		switch key {
		case LabelKeyKubernetesComponent:
			labels[key] = "runner"
		default:
			v, ok := ephemeralRunnerSet.Labels[key]
			if !ok {
				continue
			}
			labels[key] = v
		}
	}
	annotations := make(map[string]string)
	for key, val := range ephemeralRunnerSet.Annotations {
		annotations[key] = val
	}
	return &v1alpha1.EphemeralRunner{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: ephemeralRunnerSet.Name + "-runner-",
			Namespace:    ephemeralRunnerSet.Namespace,
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: ephemeralRunnerSet.Spec.EphemeralRunnerSpec,
	}
}

func (b *resourceBuilder) newEphemeralRunnerPod(ctx context.Context, runner *v1alpha1.EphemeralRunner, secret *corev1.Secret, envs ...corev1.EnvVar) *corev1.Pod {
	var newPod corev1.Pod

	labels := map[string]string{}
	annotations := map[string]string{}

	for k, v := range runner.ObjectMeta.Labels {
		labels[k] = v
	}
	for k, v := range runner.Spec.PodTemplateSpec.Labels {
		labels[k] = v
	}
	labels["actions-ephemeral-runner"] = string(corev1.ConditionTrue)

	for k, v := range runner.ObjectMeta.Annotations {
		annotations[k] = v
	}
	for k, v := range runner.Spec.PodTemplateSpec.Annotations {
		annotations[k] = v
	}

	labels[LabelKeyPodTemplateHash] = hash.FNVHashStringObjects(
		FilterLabels(labels, LabelKeyRunnerTemplateHash),
		annotations,
		runner.Spec,
		runner.Status.RunnerJITConfig,
	)

	objectMeta := metav1.ObjectMeta{
		Name:        runner.ObjectMeta.Name,
		Namespace:   runner.ObjectMeta.Namespace,
		Labels:      labels,
		Annotations: annotations,
	}

	newPod.ObjectMeta = objectMeta
	newPod.Spec = runner.Spec.PodTemplateSpec.Spec
	newPod.Spec.Containers = make([]corev1.Container, 0, len(runner.Spec.PodTemplateSpec.Spec.Containers))

	for _, c := range runner.Spec.PodTemplateSpec.Spec.Containers {
		if c.Name == EphemeralRunnerContainerName {
			c.Env = append(
				c.Env,
				corev1.EnvVar{
					Name: EnvVarRunnerJITConfig,
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: secret.Name,
							},
							Key: jitTokenKey,
						},
					},
				},
				corev1.EnvVar{
					Name:  EnvVarRunnerExtraUserAgent,
					Value: fmt.Sprintf("actions-runner-controller/%s", build.Version),
				},
			)
			c.Env = append(c.Env, envs...)
		}

		newPod.Spec.Containers = append(newPod.Spec.Containers, c)
	}

	return &newPod
}

func (b *resourceBuilder) newEphemeralRunnerJitSecret(ephemeralRunner *v1alpha1.EphemeralRunner) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ephemeralRunner.Name,
			Namespace: ephemeralRunner.Namespace,
		},
		Data: map[string][]byte{
			jitTokenKey: []byte(ephemeralRunner.Status.RunnerJITConfig),
		},
	}
}

func scaleSetListenerName(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) string {
	namespaceHash := hash.FNVHashString(autoscalingRunnerSet.Namespace)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return fmt.Sprintf("%v-%v-listener", autoscalingRunnerSet.Name, namespaceHash)
}

func scaleSetListenerServiceAccountName(autoscalingListener *v1alpha1.AutoscalingListener) string {
	namespaceHash := hash.FNVHashString(autoscalingListener.Spec.AutoscalingRunnerSetNamespace)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return fmt.Sprintf("%v-%v-listener", autoscalingListener.Spec.AutoscalingRunnerSetName, namespaceHash)
}

func scaleSetListenerRoleName(autoscalingListener *v1alpha1.AutoscalingListener) string {
	namespaceHash := hash.FNVHashString(autoscalingListener.Spec.AutoscalingRunnerSetNamespace)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return fmt.Sprintf("%v-%v-listener", autoscalingListener.Spec.AutoscalingRunnerSetName, namespaceHash)
}

func scaleSetListenerSecretMirrorName(autoscalingListener *v1alpha1.AutoscalingListener) string {
	namespaceHash := hash.FNVHashString(autoscalingListener.Spec.AutoscalingRunnerSetNamespace)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return fmt.Sprintf("%v-%v-listener", autoscalingListener.Spec.AutoscalingRunnerSetName, namespaceHash)
}

func proxyListenerSecretName(autoscalingListener *v1alpha1.AutoscalingListener) string {
	namespaceHash := hash.FNVHashString(autoscalingListener.Spec.AutoscalingRunnerSetNamespace)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return fmt.Sprintf("%v-%v-listener-proxy", autoscalingListener.Spec.AutoscalingRunnerSetName, namespaceHash)
}

func proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet) string {
	namespaceHash := hash.FNVHashString(ephemeralRunnerSet.Namespace)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return fmt.Sprintf("%v-%v-runner-proxy", ephemeralRunnerSet.Name, namespaceHash)
}

func rulesForListenerRole(resourceNames []string) []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"actions.github.com"},
			Resources:     []string{"ephemeralrunnersets"},
			ResourceNames: resourceNames,
			Verbs:         []string{"patch"},
		},
		{
			APIGroups: []string{"actions.github.com"},
			Resources: []string{"ephemeralrunners", "ephemeralrunners/status"},
			Verbs:     []string{"patch"},
		},
	}
}

func applyGitHubURLLabels(url string, labels map[string]string) error {
	githubConfig, err := actions.ParseGitHubConfigFromURL(url)
	if err != nil {
		return fmt.Errorf("failed to parse github config from url: %v", err)
	}

	if len(githubConfig.Enterprise) > 0 {
		labels[LabelKeyGitHubEnterprise] = trimLabelValue(githubConfig.Enterprise)
	}
	if len(githubConfig.Organization) > 0 {
		labels[LabelKeyGitHubOrganization] = trimLabelValue(githubConfig.Organization)
	}
	if len(githubConfig.Repository) > 0 {
		labels[LabelKeyGitHubRepository] = trimLabelValue(githubConfig.Repository)
	}

	return nil
}

const trimLabelVauleSuffix = "-trim"

func trimLabelValue(val string) string {
	if len(val) > 63 {
		return val[:63-len(trimLabelVauleSuffix)] + trimLabelVauleSuffix
	}
	return val
}
