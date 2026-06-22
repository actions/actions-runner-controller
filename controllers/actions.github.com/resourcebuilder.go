package actionsgithubcom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net"
	"strconv"
	"strings"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/build"
	ghalistenerconfig "github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/object"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/hash"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
	"github.com/actions/scaleset"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// AnnotationKeyIntegrityHash is used as a hash of the important fields
// of each resource to determine if more drastic action should be taken.
//
// For example, annotations/labels are not something that should modify
// the behavior of a resource, while the change in spec is. Therefore,
// the spec hash should contain the spec fields in order to determine
// modifications.
const AnnotationKeyIntegrityHash = "actions.github.com/integrity-hash"

const labelValueKubernetesPartOf = "gha-runner-scale-set"

var (
	scaleSetListenerLogLevel   = DefaultScaleSetListenerLogLevel
	scaleSetListenerLogFormat  = DefaultScaleSetListenerLogFormat
	scaleSetListenerEntrypoint = "/ghalistener"
)

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

func SetListenerEntrypoint(entrypoint string) {
	if entrypoint != "" {
		scaleSetListenerEntrypoint = entrypoint
	}
}

type SecretResolver interface {
	GetAppConfig(ctx context.Context, obj object.ActionsGitHubObject) (*appconfig.AppConfig, error)
	GetActionsService(ctx context.Context, obj object.ActionsGitHubObject) (multiclient.Client, error)
}

type ResourceBuilder struct {
	ExcludeLabelPropagationPrefixes []string
	SecretResolver
	Scheme        *runtime.Scheme
	ResourceCache ResourceCache
}

func (b *ResourceBuilder) setSchemeIfUnset(scheme *runtime.Scheme) {
	if b.Scheme == nil {
		b.Scheme = scheme
	}
}

func (b *ResourceBuilder) setControllerReference(owner client.Object, object client.Object) error {
	if b.Scheme == nil {
		b.Scheme = runtime.NewScheme()
		if err := v1alpha1.AddToScheme(b.Scheme); err != nil {
			return err
		}
	}

	return ctrl.SetControllerReference(owner, object, b.Scheme)
}

func autoscalingRunnerSetIntegrityHash(ars *v1alpha1.AutoscalingRunnerSet) string {
	return hash.ComputeTemplateHash(&ars.Spec)
}

func (b *ResourceBuilder) newAutoscalingListener(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, namespace, image string, imagePullSecrets []corev1.LocalObjectReference) (*v1alpha1.AutoscalingListener, error) {
	runnerScaleSetID, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey])
	if err != nil {
		return nil, err
	}

	cacheKeyObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerName(autoscalingRunnerSet),
			Namespace: namespace,
		},
	}
	inputDependency := resourceCacheInputObject("autoscaling-listener-inputs", struct {
		Namespace        string
		Image            string
		ImagePullSecrets []corev1.LocalObjectReference
	}{
		Namespace:        namespace,
		Image:            image,
		ImagePullSecrets: imagePullSecrets,
	})
	if cached, ok := b.cachedDesiredResource(resourceCacheShardAutoscalingListener, autoscalingRunnerSet, cacheKeyObject, ephemeralRunnerSet, inputDependency); ok {
		return cached.(*v1alpha1.AutoscalingListener), nil
	}

	effectiveMinRunners := 0
	if autoscalingRunnerSet.Spec.MinRunners != nil {
		effectiveMinRunners = *autoscalingRunnerSet.Spec.MinRunners
	}

	effectiveMaxRunners := math.MaxInt32
	if autoscalingRunnerSet.Spec.MaxRunners != nil {
		effectiveMaxRunners = *autoscalingRunnerSet.Spec.MaxRunners
	}

	spec := v1alpha1.AutoscalingListenerSpec{
		GitHubConfigURL:               autoscalingRunnerSet.Spec.GitHubConfigUrl,
		GitHubConfigSecret:            autoscalingRunnerSet.Spec.GitHubConfigSecret,
		VaultConfig:                   autoscalingRunnerSet.VaultConfig(),
		RunnerScaleSetID:              runnerScaleSetID,
		AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
		AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
		EphemeralRunnerSetName:        ephemeralRunnerSet.Name,
		MinRunners:                    effectiveMinRunners,
		MaxRunners:                    effectiveMaxRunners,
		Image:                         image,
		ImagePullSecrets:              imagePullSecrets,
		Proxy:                         autoscalingRunnerSet.Spec.Proxy,
		GitHubServerTLS:               autoscalingRunnerSet.Spec.GitHubServerTLS,
		Metrics:                       autoscalingRunnerSet.Spec.ListenerMetrics,
		Template:                      autoscalingRunnerSet.Spec.ListenerTemplate,
		ServiceAccountMetadata:        autoscalingRunnerSet.Spec.ListenerServiceAccountMetadata,
		RoleMetadata:                  autoscalingRunnerSet.Spec.ListenerRoleMetadata,
		RoleBindingMetadata:           autoscalingRunnerSet.Spec.ListenerRoleBindingMetadata,
		ConfigSecretMetadata:          autoscalingRunnerSet.Spec.ListenerConfigSecretMetadata,
	}

	labels := b.filterAndMergeLabels(autoscalingRunnerSet.Labels, map[string]string{
		LabelKeyGitHubScaleSetNamespace: autoscalingRunnerSet.Namespace,
		LabelKeyGitHubScaleSetName:      autoscalingRunnerSet.Name,
		LabelKeyKubernetesPartOf:        labelValueKubernetesPartOf,
		LabelKeyKubernetesComponent:     "runner-scale-set-listener",
		LabelKeyKubernetesVersion:       autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
	})

	if err := applyGitHubURLLabels(autoscalingRunnerSet.Spec.GitHubConfigUrl, labels); err != nil {
		return nil, fmt.Errorf("failed to apply GitHub URL labels: %v", err)
	}

	annotations := map[string]string{
		AnnotationKeyIntegrityHash: spec.Hash(),
	}

	if autoscalingRunnerSet.Spec.AutoscalingListenerMetadata != nil {
		labels = b.filterAndMergeLabels(autoscalingRunnerSet.Spec.AutoscalingListenerMetadata.Labels, labels)
		annotations = b.filterAndMergeAnnotations(autoscalingRunnerSet.Spec.AutoscalingListenerMetadata.Annotations, annotations)
	}

	autoscalingListener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:        scaleSetListenerName(autoscalingRunnerSet),
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
	}
	b.cacheDesiredResource(resourceCacheShardAutoscalingListener, autoscalingRunnerSet, autoscalingListener, ephemeralRunnerSet, inputDependency)

	return autoscalingListener, nil
}

func resourceCacheInputObject(name string, value any) client.Object {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			ResourceVersion: hash.ComputeTemplateHashJSON(value),
		},
	}
}

type listenerMetricsServerConfig struct {
	addr     string
	endpoint string
}

func (lm *listenerMetricsServerConfig) containerPort() (corev1.ContainerPort, error) {
	_, portStr, err := net.SplitHostPort(lm.addr)
	if err != nil {
		return corev1.ContainerPort{}, err
	}
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return corev1.ContainerPort{}, err
	}
	return corev1.ContainerPort{
		ContainerPort: int32(port),
		Protocol:      corev1.ProtocolTCP,
		Name:          "metrics",
	}, nil
}

func (b *ResourceBuilder) newScaleSetListenerConfig(autoscalingListener *v1alpha1.AutoscalingListener, appConfig *appconfig.AppConfig, metricsConfig *listenerMetricsServerConfig, cert string) (*corev1.Secret, error) {
	var (
		metricsAddr     = ""
		metricsEndpoint = ""
	)
	if metricsConfig != nil {
		metricsAddr = metricsConfig.addr
		metricsEndpoint = metricsConfig.endpoint
	}

	config := ghalistenerconfig.Config{
		ConfigureURL:                autoscalingListener.Spec.GitHubConfigURL,
		EphemeralRunnerSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
		EphemeralRunnerSetName:      autoscalingListener.Spec.EphemeralRunnerSetName,
		MaxRunners:                  autoscalingListener.Spec.MaxRunners,
		MinRunners:                  autoscalingListener.Spec.MinRunners,
		RunnerScaleSetID:            autoscalingListener.Spec.RunnerScaleSetID,
		RunnerScaleSetName:          autoscalingListener.Spec.AutoscalingRunnerSetName,
		ServerRootCA:                cert,
		LogLevel:                    scaleSetListenerLogLevel,
		LogFormat:                   scaleSetListenerLogFormat,
		MetricsAddr:                 metricsAddr,
		MetricsEndpoint:             metricsEndpoint,
		Metrics:                     autoscalingListener.Spec.Metrics,
	}

	vault := autoscalingListener.Spec.VaultConfig
	if vault == nil {
		config.AppConfig = appConfig
	} else {
		config.VaultType = vault.Type
		config.VaultLookupKey = autoscalingListener.Spec.GitHubConfigSecret
		config.AzureKeyVaultConfig = &azurekeyvault.Config{
			TenantID:        vault.AzureKeyVault.TenantID,
			ClientID:        vault.AzureKeyVault.ClientID,
			URL:             vault.AzureKeyVault.URL,
			CertificatePath: vault.AzureKeyVault.CertificatePath,
		}
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid listener config: %w", err)
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(config); err != nil {
		return nil, fmt.Errorf("failed to encode config: %w", err)
	}

	var labels map[string]string
	if autoscalingListener.Spec.ConfigSecretMetadata != nil && len(autoscalingListener.Spec.ConfigSecretMetadata.Labels) > 0 {
		labels = b.filterAndMergeLabels(autoscalingListener.Spec.ConfigSecretMetadata.Labels, nil)
	}

	annotations := make(map[string]string)
	if autoscalingListener.Spec.ConfigSecretMetadata != nil && len(autoscalingListener.Spec.ConfigSecretMetadata.Annotations) > 0 {
		annotations = autoscalingListener.Spec.ConfigSecretMetadata.Annotations
	}

	desiredSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        scaleSetListenerConfigName(autoscalingListener),
			Namespace:   autoscalingListener.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Data: map[string][]byte{
			"config.json": buf.Bytes(),
		},
	}

	desiredSecret.Annotations[AnnotationKeyIntegrityHash] = scaleSetListenerConfigIntegrityHash(desiredSecret)

	if err := b.setControllerReference(autoscalingListener, desiredSecret); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for listener config secret: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardListenerConfigSecret, autoscalingListener, desiredSecret)

	return desiredSecret, nil
}

func scaleSetListenerConfigIntegrityHash(secret *corev1.Secret) string {
	type data struct {
		Data map[string][]byte `json:"data,omitempty"`
	}

	d := data{
		Data: secret.Data,
	}

	return hash.ComputeTemplateHash(&d)
}

func (b *ResourceBuilder) newScaleSetListenerPod(
	autoscalingListener *v1alpha1.AutoscalingListener,
	podConfig *corev1.Secret,
	serviceAccount *corev1.ServiceAccount,
	role *rbacv1.Role,
	roleBinding *rbacv1.RoleBinding,
	metricsConfig *listenerMetricsServerConfig,
) (*corev1.Pod, error) {
	cacheKeyObject := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalingListener.Name,
			Namespace: autoscalingListener.Namespace,
		},
	}
	if cached, ok := b.cachedDesiredResource(resourceCacheShardListenerPod, autoscalingListener, cacheKeyObject, podConfig, serviceAccount, role, roleBinding); ok {
		return cached.(*corev1.Pod), nil
	}

	envs := []corev1.EnvVar{
		{
			Name:  "LISTENER_CONFIG_PATH",
			Value: "/etc/gha-listener/config.json",
		},
	}

	if autoscalingListener.Spec.Proxy != nil {
		httpURL := corev1.EnvVar{
			Name: "http_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: proxyListenerSecretName(autoscalingListener),
					},
					Key: "http_proxy",
				},
			},
		}
		if autoscalingListener.Spec.Proxy.HTTP != nil {
			envs = append(envs, httpURL)
		}

		httpsURL := corev1.EnvVar{
			Name: "https_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: proxyListenerSecretName(autoscalingListener),
					},
					Key: "https_proxy",
				},
			},
		}
		if autoscalingListener.Spec.Proxy.HTTPS != nil {
			envs = append(envs, httpsURL)
		}

		noProxy := corev1.EnvVar{
			Name: "no_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: proxyListenerSecretName(autoscalingListener),
					},
					Key: "no_proxy",
				},
			},
		}
		if len(autoscalingListener.Spec.Proxy.NoProxy) > 0 {
			envs = append(envs, noProxy)
		}
	}

	var ports []corev1.ContainerPort
	if metricsConfig != nil && len(metricsConfig.addr) != 0 {
		port, err := metricsConfig.containerPort()
		if err != nil {
			return nil, fmt.Errorf("failed to convert metrics server address to container port: %v", err)
		}
		ports = append(ports, port)
	}

	terminationGracePeriodSeconds := int64(60)
	podSpec := corev1.PodSpec{
		ServiceAccountName: serviceAccount.Name,
		NodeSelector: map[string]string{
			LabelKeyKubernetesOS: "linux",
		},
		Containers: []corev1.Container{
			{
				Name:  autoscalingListenerContainerName,
				Image: autoscalingListener.Spec.Image,
				Env:   envs,
				Command: []string{
					scaleSetListenerEntrypoint,
				},
				Ports: ports,
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "listener-config",
						MountPath: "/etc/gha-listener",
						ReadOnly:  true,
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "listener-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: podConfig.Name,
					},
				},
			},
		},
		ImagePullSecrets:              autoscalingListener.Spec.ImagePullSecrets,
		RestartPolicy:                 corev1.RestartPolicyNever,
		TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
	}

	labels := make(map[string]string, len(autoscalingListener.Labels))
	maps.Copy(labels, autoscalingListener.Labels)

	newRunnerScaleSetListenerPod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        autoscalingListener.Name,
			Namespace:   autoscalingListener.Namespace,
			Labels:      labels,
			Annotations: make(map[string]string),
		},
		Spec: podSpec,
	}

	newRunnerScaleSetListenerPod.Annotations[AnnotationKeyIntegrityHash] = scaleSetListenerPodIntegrity(
		newRunnerScaleSetListenerPod,
		autoscalingListener,
		podConfig,
		serviceAccount,
		role,
		roleBinding,
		metricsConfig,
	)

	if err := b.setControllerReference(autoscalingListener, newRunnerScaleSetListenerPod); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for listener pod: %w", err)
	}

	if autoscalingListener.Spec.Template != nil {
		mergeListenerPodWithTemplate(newRunnerScaleSetListenerPod, autoscalingListener.Spec.Template)
	}
	b.cacheDesiredResource(resourceCacheShardListenerPod, autoscalingListener, newRunnerScaleSetListenerPod, podConfig, serviceAccount, role, roleBinding)

	return newRunnerScaleSetListenerPod, nil
}

func scaleSetListenerPodIntegrity(
	pod *corev1.Pod,
	autoscalingListener *v1alpha1.AutoscalingListener,
	podConfig *corev1.Secret,
	serviceAccount *corev1.ServiceAccount,
	role *rbacv1.Role,
	roleBinding *rbacv1.RoleBinding,
	metricsConfig *listenerMetricsServerConfig,
) string {
	type data struct {
		ListenerPodSpec                  *corev1.PodSpec              `json:"listenerPodSpec,omitempty"`
		AutoscalingListenerIntegrityHash string                       `json:"autoscalingListenerIntegrityHash"`
		ConfigSecretIntegrityHash        string                       `json:"configSecretIntegrityHash"`
		ServiceAccountIntegrityHash      string                       `json:"serviceAccountIntegrityHash"`
		RoleIntegrityHash                string                       `json:"roleIntegrityHash"`
		RoleBindingIntegrityHash         string                       `json:"roleBindingIntegrityHash"`
		MetricsConfig                    *listenerMetricsServerConfig `json:"metricsConfig,omitempty"`
	}

	d := data{
		ListenerPodSpec:                  &pod.Spec,
		AutoscalingListenerIntegrityHash: autoscalingListener.Annotations[AnnotationKeyIntegrityHash],
		ConfigSecretIntegrityHash:        podConfig.Annotations[AnnotationKeyIntegrityHash],
		ServiceAccountIntegrityHash:      serviceAccount.Annotations[AnnotationKeyIntegrityHash],
		RoleIntegrityHash:                role.Annotations[AnnotationKeyIntegrityHash],
		RoleBindingIntegrityHash:         roleBinding.Annotations[AnnotationKeyIntegrityHash],
		MetricsConfig:                    metricsConfig,
	}

	return hash.ComputeTemplateHash(&d)
}

func mergeListenerPodWithTemplate(pod *corev1.Pod, tmpl *corev1.PodTemplateSpec) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
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
		case autoscalingListenerContainerName:
			mergeListenerContainer(listenerContainer, c)
		default:
			pod.Spec.Containers = append(pod.Spec.Containers, *c)
		}
	}

	// apply pod related spec
	// NOTE: fields that should be ignored
	// - service account based fields

	if tmpl.Spec.RestartPolicy != "" {
		pod.Spec.RestartPolicy = tmpl.Spec.RestartPolicy
	}

	if tmpl.Spec.ImagePullSecrets != nil {
		pod.Spec.ImagePullSecrets = tmpl.Spec.ImagePullSecrets
	}

	if tmpl.Spec.NodeSelector != nil {
		pod.Spec.NodeSelector = tmpl.Spec.NodeSelector
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, tmpl.Spec.Volumes...)
	pod.Spec.InitContainers = tmpl.Spec.InitContainers
	pod.Spec.EphemeralContainers = tmpl.Spec.EphemeralContainers
	pod.Spec.TerminationGracePeriodSeconds = tmpl.Spec.TerminationGracePeriodSeconds
	pod.Spec.ActiveDeadlineSeconds = tmpl.Spec.ActiveDeadlineSeconds
	pod.Spec.DNSPolicy = tmpl.Spec.DNSPolicy
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

	base.Env = append(base.Env, from.Env...)

	base.ImagePullPolicy = from.ImagePullPolicy
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
	base.ResizePolicy = from.ResizePolicy
	base.RestartPolicy = from.RestartPolicy
	base.Stdin = from.Stdin
	base.StdinOnce = from.StdinOnce
	base.TTY = from.TTY
}

func (b *ResourceBuilder) newScaleSetListenerServiceAccount(autoscalingListener *v1alpha1.AutoscalingListener) (*corev1.ServiceAccount, error) {
	cacheKeyObject := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalingListener.Name,
			Namespace: autoscalingListener.Namespace,
		},
	}
	if cached, ok := b.cachedDesiredResource(resourceCacheShardListenerServiceAccount, autoscalingListener, cacheKeyObject); ok {
		return cached.(*corev1.ServiceAccount), nil
	}

	base := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalingListener.Name,
			Namespace: autoscalingListener.Namespace,
			Labels: b.filterAndMergeLabels(autoscalingListener.Labels, map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
			}),
			Annotations: make(map[string]string),
		},
	}

	if autoscalingListener.Spec.ServiceAccountMetadata != nil {
		base.Labels = b.filterAndMergeLabels(autoscalingListener.Spec.ServiceAccountMetadata.Labels, base.Labels)
		base.Annotations = b.filterAndMergeAnnotations(autoscalingListener.Spec.ServiceAccountMetadata.Annotations, base.Annotations)
	}

	base.Annotations[AnnotationKeyIntegrityHash] = scaleSetListenerServiceAccountIntegrityHash(base)

	if err := b.setControllerReference(autoscalingListener, base); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for listener service account: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardListenerServiceAccount, autoscalingListener, base)

	return base, nil
}

func scaleSetListenerServiceAccountIntegrityHash(sa *corev1.ServiceAccount) string {
	type data struct {
		Secrets                      []corev1.ObjectReference      `json:"secrets"`
		ImagePullSecrets             []corev1.LocalObjectReference `json:"imagePullSecrets"`
		AutomountServiceAccountToken *bool                         `json:"automountServiceAccountToken"`
	}

	d := data{
		Secrets:                      sa.Secrets,
		ImagePullSecrets:             sa.ImagePullSecrets,
		AutomountServiceAccountToken: sa.AutomountServiceAccountToken,
	}

	return hash.ComputeTemplateHash(&d)
}

func (b *ResourceBuilder) newScaleSetListenerRole(autoscalingListener *v1alpha1.AutoscalingListener) *rbacv1.Role {
	cacheKeyObject := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalingListener.Name,
			Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
		},
	}
	if cached, ok := b.cachedDesiredResource(resourceCacheShardListenerRole, autoscalingListener, cacheKeyObject); ok {
		return cached.(*rbacv1.Role)
	}

	labels := b.filterAndMergeLabels(autoscalingListener.Labels, map[string]string{
		LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
		LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
		labelKeyListenerNamespace:       autoscalingListener.Namespace,
		labelKeyListenerName:            autoscalingListener.Name,
	})

	annotations := make(map[string]string)
	if autoscalingListener.Spec.RoleMetadata != nil {
		labels = b.filterAndMergeLabels(autoscalingListener.Spec.RoleMetadata.Labels, labels)
		annotations = b.filterAndMergeAnnotations(autoscalingListener.Spec.RoleMetadata.Annotations, nil)
	}

	newRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:        autoscalingListener.Name,
			Namespace:   autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Rules: rulesForListenerRole([]string{autoscalingListener.Spec.EphemeralRunnerSetName}),
	}

	newRole.Annotations[AnnotationKeyIntegrityHash] = scaleSetRoleIntegrityHash(newRole)
	b.cacheDesiredResource(resourceCacheShardListenerRole, autoscalingListener, newRole)

	return newRole
}

func scaleSetRoleIntegrityHash(role *rbacv1.Role) string {
	type data struct {
		Rules []rbacv1.PolicyRule `json:"rules"`
	}

	d := data{
		Rules: role.Rules,
	}

	return hash.ComputeTemplateHash(&d)
}

func (b *ResourceBuilder) newScaleSetListenerRoleBinding(autoscalingListener *v1alpha1.AutoscalingListener, listenerRole *rbacv1.Role, serviceAccount *corev1.ServiceAccount) *rbacv1.RoleBinding {
	cacheKeyObject := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      autoscalingListener.Name,
			Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
		},
	}
	if cached, ok := b.cachedDesiredResource(resourceCacheShardListenerRoleBinding, autoscalingListener, cacheKeyObject, listenerRole, serviceAccount); ok {
		return cached.(*rbacv1.RoleBinding)
	}

	roleRef := rbacv1.RoleRef{
		Kind: "Role",
		Name: listenerRole.Name,
	}

	subjects := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Namespace: serviceAccount.Namespace,
			Name:      serviceAccount.Name,
		},
	}

	labels := b.filterAndMergeLabels(autoscalingListener.Labels, map[string]string{
		LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
		LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
		labelKeyListenerNamespace:       autoscalingListener.Namespace,
		labelKeyListenerName:            autoscalingListener.Name,
	})

	annotations := make(map[string]string)
	if autoscalingListener.Spec.RoleBindingMetadata != nil {
		labels = b.filterAndMergeLabels(autoscalingListener.Spec.RoleBindingMetadata.Labels, labels)
		annotations = autoscalingListener.Spec.RoleBindingMetadata.Annotations
	}

	newRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        autoscalingListener.Name,
			Namespace:   autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			Labels:      labels,
			Annotations: annotations,
		},
		RoleRef:  roleRef,
		Subjects: subjects,
	}

	newRoleBinding.Annotations[AnnotationKeyIntegrityHash] = scaleSetListenerRoleBindingIntegrityHash(newRoleBinding)
	b.cacheDesiredResource(resourceCacheShardListenerRoleBinding, autoscalingListener, newRoleBinding, listenerRole, serviceAccount)

	return newRoleBinding
}

func scaleSetListenerRoleBindingIntegrityHash(rb *rbacv1.RoleBinding) string {
	type data struct {
		RoleRef  rbacv1.RoleRef   `json:"roleRef"`
		Subjects []rbacv1.Subject `json:"subjects"`
	}

	d := data{
		RoleRef:  rb.RoleRef,
		Subjects: rb.Subjects,
	}

	return hash.ComputeTemplateHash(&d)
}

func (b *ResourceBuilder) newEphemeralRunnerSet(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) (*v1alpha1.EphemeralRunnerSet, error) {
	runnerScaleSetID, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey])
	if err != nil {
		return nil, err
	}

	spec := v1alpha1.EphemeralRunnerSetSpec{
		Replicas: 0,
		EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
			RunnerScaleSetID:                    runnerScaleSetID,
			GitHubConfigURL:                     autoscalingRunnerSet.Spec.GitHubConfigUrl,
			GitHubConfigSecret:                  autoscalingRunnerSet.Spec.GitHubConfigSecret,
			Proxy:                               autoscalingRunnerSet.Spec.Proxy,
			GitHubServerTLS:                     autoscalingRunnerSet.Spec.GitHubServerTLS,
			PodTemplateSpec:                     autoscalingRunnerSet.Spec.Template,
			VaultConfig:                         autoscalingRunnerSet.VaultConfig(),
			EphemeralRunnerConfigSecretMetadata: autoscalingRunnerSet.Spec.EphemeralRunnerConfigSecretMetadata,
		},
		EphemeralRunnerMetadata: autoscalingRunnerSet.Spec.EphemeralRunnerMetadata,
	}

	labels := b.filterAndMergeLabels(autoscalingRunnerSet.Labels, map[string]string{
		LabelKeyKubernetesPartOf:        labelValueKubernetesPartOf,
		LabelKeyKubernetesComponent:     "runner-set",
		LabelKeyKubernetesVersion:       autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
		LabelKeyGitHubScaleSetName:      autoscalingRunnerSet.Name,
		LabelKeyGitHubScaleSetNamespace: autoscalingRunnerSet.Namespace,
	})

	if err := applyGitHubURLLabels(autoscalingRunnerSet.Spec.GitHubConfigUrl, labels); err != nil {
		return nil, fmt.Errorf("failed to apply GitHub URL labels: %v", err)
	}

	annotations := map[string]string{
		AnnotationKeyGitHubRunnerGroupName:    autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName],
		AnnotationKeyGitHubRunnerScaleSetName: autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName],
	}

	if autoscalingRunnerSet.Spec.EphemeralRunnerSetMetadata != nil {
		labels = b.filterAndMergeLabels(autoscalingRunnerSet.Spec.EphemeralRunnerSetMetadata.Labels, labels)
		annotations = b.filterAndMergeAnnotations(autoscalingRunnerSet.Spec.EphemeralRunnerSetMetadata.Annotations, annotations)
	}

	newEphemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        autoscalingRunnerSet.Name,
			Namespace:   autoscalingRunnerSet.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
	}

	newEphemeralRunnerSet.Annotations[AnnotationKeyIntegrityHash] = ephemeralRunnerSetIntegrityHash(newEphemeralRunnerSet)

	if err := b.setControllerReference(autoscalingRunnerSet, newEphemeralRunnerSet); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for ephemeral runner set: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardEphemeralRunnerSet, autoscalingRunnerSet, newEphemeralRunnerSet)

	return newEphemeralRunnerSet, nil
}

func ephemeralRunnerSetIntegrityHash(ers *v1alpha1.EphemeralRunnerSet) string {
	type data struct {
		EphemeralRunnerSpec v1alpha1.EphemeralRunnerSpec `json:"ephemeralRunnerSpec"`
	}

	d := data{
		EphemeralRunnerSpec: ers.Spec.EphemeralRunnerSpec,
	}
	return hash.ComputeTemplateHashJSON(&d)
}

func (b *ResourceBuilder) newAutoscalingListenerProxySecret(autoscalingListener *v1alpha1.AutoscalingListener, data map[string][]byte) (*corev1.Secret, error) {
	newProxySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyListenerSecretName(autoscalingListener),
			Namespace: autoscalingListener.Namespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
			},
			Annotations: make(map[string]string, 1),
		},
		Data: data,
	}

	newProxySecret.Annotations[AnnotationKeyIntegrityHash] = autoscalingListenerProxySecretIntegrityHash(newProxySecret)

	if err := b.setControllerReference(autoscalingListener, newProxySecret); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for listener proxy secret: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardAutoscalingListenerProxySecret, autoscalingListener, newProxySecret)

	return newProxySecret, nil
}

func autoscalingListenerProxySecretIntegrityHash(secret *corev1.Secret) string {
	type data struct {
		Data map[string][]byte `json:"data"`
	}

	d := data{
		Data: secret.Data,
	}

	return hash.ComputeTemplateHash(&d)
}

func (b *ResourceBuilder) newEphemeralRunner(ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet) (*v1alpha1.EphemeralRunner, error) {
	labels := make(map[string]string, len(ephemeralRunnerSet.Labels))
	maps.Copy(labels, ephemeralRunnerSet.Labels)
	labels[LabelKeyKubernetesComponent] = "runner"

	annotations := make(map[string]string, len(ephemeralRunnerSet.Annotations)+1)
	maps.Copy(annotations, ephemeralRunnerSet.Annotations)
	annotations[AnnotationKeyPatchID] = strconv.Itoa(ephemeralRunnerSet.Spec.PatchID)

	if ephemeralRunnerSet.Spec.EphemeralRunnerMetadata != nil {
		labels = b.filterAndMergeLabels(ephemeralRunnerSet.Spec.EphemeralRunnerMetadata.Labels, labels)
		annotations = b.filterAndMergeAnnotations(ephemeralRunnerSet.Spec.EphemeralRunnerMetadata.Annotations, annotations)
	}

	ephemeralRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: ephemeralRunnerSet.Name + "-runner-",
			Namespace:    ephemeralRunnerSet.Namespace,
			Labels:       labels,
			Annotations:  annotations,
			Finalizers: []string{
				ephemeralRunnerFinalizerName,
				ephemeralRunnerActionsFinalizerName,
			},
		},
		Spec: ephemeralRunnerSet.Spec.EphemeralRunnerSpec,
	}
	if err := b.setControllerReference(ephemeralRunnerSet, ephemeralRunner); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for ephemeral runner: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardEphemeralRunner, ephemeralRunnerSet, ephemeralRunner)

	return ephemeralRunner, nil
}

func (b *ResourceBuilder) newEphemeralRunnerPod(runner *v1alpha1.EphemeralRunner, secret *corev1.Secret, envs ...corev1.EnvVar) (*corev1.Pod, error) {
	var newPod corev1.Pod

	annotations := make(map[string]string, len(runner.Annotations)+len(runner.Spec.Annotations))
	maps.Copy(annotations, runner.Annotations)
	maps.Copy(annotations, runner.Spec.Annotations)

	labels := make(map[string]string, len(runner.Labels)+len(runner.Spec.Labels)+2)
	maps.Copy(labels, runner.Labels)
	maps.Copy(labels, runner.Spec.Labels)
	labels["actions-ephemeral-runner"] = string(corev1.ConditionTrue)
	labels[LabelKeyPodTemplateHash] = hash.FNVHashStringObjects(
		FilterLabels(labels, LabelKeyRunnerTemplateHash),
		annotations,
		runner.Spec,
		secret.Data,
	)

	objectMeta := metav1.ObjectMeta{
		Name:        runner.Name,
		Namespace:   runner.Namespace,
		Labels:      labels,
		Annotations: annotations,
	}

	newPod.ObjectMeta = objectMeta
	newPod.Spec = runner.Spec.Spec
	newPod.Spec.Containers = make([]corev1.Container, 0, len(runner.Spec.Spec.Containers))

	for _, c := range runner.Spec.Spec.Containers {
		if c.Name == v1alpha1.EphemeralRunnerContainerName {
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
				corev1.EnvVar{
					Name:  EnvVarRunnerDeprecatedExitCode,
					Value: "1",
				},
			)
			c.Env = append(c.Env, envs...)
		}

		newPod.Spec.Containers = append(newPod.Spec.Containers, c)
	}

	if err := b.setControllerReference(runner, &newPod); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for ephemeral runner pod: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardEphemeralRunnerPod, runner, &newPod, secret)

	return &newPod, nil
}

func (b *ResourceBuilder) newEphemeralRunnerJitSecret(ephemeralRunner *v1alpha1.EphemeralRunner, jitConfig *scaleset.RunnerScaleSetJitRunnerConfig) (*corev1.Secret, error) {
	var (
		labels      map[string]string
		annotations map[string]string
	)

	if ephemeralRunner.Spec.EphemeralRunnerConfigSecretMetadata != nil {
		labels = b.filterAndMergeLabels(ephemeralRunner.Spec.EphemeralRunnerConfigSecretMetadata.Labels, nil)
		annotations = ephemeralRunner.Spec.EphemeralRunnerConfigSecretMetadata.Annotations
	}

	jitSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ephemeralRunner.Name,
			Namespace:   ephemeralRunner.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Data: map[string][]byte{
			jitTokenKey:  []byte(jitConfig.EncodedJITConfig),
			"runnerName": []byte(jitConfig.Runner.Name),
			"runnerId":   []byte(strconv.Itoa(jitConfig.Runner.ID)),
			"scaleSetId": []byte(strconv.Itoa(jitConfig.Runner.RunnerScaleSetID)),
		},
	}
	if err := b.setControllerReference(ephemeralRunner, jitSecret); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for ephemeral runner jit secret: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardEphemeralRunnerJitSecret, ephemeralRunner, jitSecret)

	return jitSecret, nil
}

func (b *ResourceBuilder) newEphemeralRunnerSetProxySecret(ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, data map[string][]byte) (*corev1.Secret, error) {
	runnerPodProxySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
			Namespace: ephemeralRunnerSet.Namespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetName:      ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetName],
				LabelKeyGitHubScaleSetNamespace: ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetNamespace],
			},
			Annotations: make(map[string]string, 1),
		},
		Data: data,
	}

	runnerPodProxySecret.Annotations[AnnotationKeyIntegrityHash] = ephemeralRunnerSetProxySecretZIdentityHash(runnerPodProxySecret)

	if err := b.setControllerReference(ephemeralRunnerSet, runnerPodProxySecret); err != nil {
		return nil, fmt.Errorf("failed to set controller reference for ephemeral runner set proxy secret: %w", err)
	}
	b.cacheDesiredResource(resourceCacheShardEphemeralRunnerSetProxySecret, ephemeralRunnerSet, runnerPodProxySecret)

	return runnerPodProxySecret, nil
}

func ephemeralRunnerSetProxySecretZIdentityHash(secret *corev1.Secret) string {
	type data struct {
		Data map[string][]byte `json:"data"`
	}

	d := data{
		Data: secret.Data,
	}

	return hash.ComputeTemplateHashJSON(&d)
}

func scaleSetListenerConfigName(autoscalingListener *v1alpha1.AutoscalingListener) string {
	return autoscalingListener.Name + "-config"
}

func hashSuffix(namespace, runnerGroup, configURL string) string {
	namespaceHash := hash.FNVHashString(namespace + "@" + runnerGroup + "@" + configURL)
	if len(namespaceHash) > 8 {
		namespaceHash = namespaceHash[:8]
	}
	return namespaceHash
}

func scaleSetListenerName(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) string {
	return fmt.Sprintf(
		"%v-%v-listener",
		autoscalingRunnerSet.Name,
		hashSuffix(
			autoscalingRunnerSet.Namespace,
			autoscalingRunnerSet.Spec.RunnerGroup,
			autoscalingRunnerSet.Spec.GitHubConfigUrl,
		),
	)
}

func proxyListenerSecretName(autoscalingListener *v1alpha1.AutoscalingListener) string {
	return autoscalingListener.Name + "-proxy"
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
	return strings.Trim(val, "-_.")
}

func (b *ResourceBuilder) filterLabels(k, v string) bool {
	for _, prefix := range b.ExcludeLabelPropagationPrefixes {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func (b *ResourceBuilder) filterAndMergeLabels(base, overwrite map[string]string) map[string]string {
	return filterAndMergeMaps(base, overwrite, b.filterLabels)
}

func filterAndMergeMaps(base, overwrite map[string]string, filter func(k, v string) bool) map[string]string {
	if base == nil && overwrite == nil {
		return nil
	}
	var result map[string]string
	if len(base) == 0 {
		result = make(map[string]string)
	} else {
		result = maps.Clone(base)
	}
	if len(overwrite) > 0 {
		maps.Copy(result, overwrite)
	}
	maps.DeleteFunc(result, filter)
	return result
}

func (b *ResourceBuilder) filterAndMergeAnnotations(base, overwrite map[string]string) map[string]string {
	if base == nil && overwrite == nil {
		return nil
	}
	var result map[string]string
	if len(base) == 0 {
		result = make(map[string]string)
	} else {
		result = maps.Clone(base)
	}

	for k, v := range overwrite {
		if k == AnnotationKeyIntegrityHash {
			continue
		}
		result[k] = v
	}

	return result
}

// compareAnnotations compares two maps of annotations, ignoring the integrity hash annotation.
func (b *ResourceBuilder) annotationsEqual(m1, m2 map[string]string) bool {
	l1 := len(m1)
	if _, ok := m1[AnnotationKeyIntegrityHash]; !ok {
		l1++
	}
	l2 := len(m2)
	if _, ok := m2[AnnotationKeyIntegrityHash]; !ok {
		l2++
	}
	if l1 != l2 {
		return false
	}

	for k, v1 := range m1 {
		if k == AnnotationKeyIntegrityHash {
			continue
		}
		if v2, ok := m2[k]; !ok || v1 != v2 {
			return false
		}
	}
	return true
}
