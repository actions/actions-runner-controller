package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// desiredListenerPod mirrors the shape produced by newScaleSetListenerPod: only
// the handful of fields the builder actually sets.
func desiredListenerPod() *corev1.Pod {
	grace := int64(60)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "arc-systems"},
		Spec: corev1.PodSpec{
			ServiceAccountName: "listener",
			NodeSelector:       map[string]string{"kubernetes.io/os": "linux"},
			Containers: []corev1.Container{{
				Name:    autoscalingListenerContainerName,
				Image:   "ghcr.io/actions/arc:0.1.0",
				Command: []string{"/ghalistener"},
				Env: []corev1.EnvVar{
					{Name: "LISTENER_CONFIG_PATH", Value: "/etc/gha-listener/config.json"},
				},
				Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "listener-config", MountPath: "/etc/gha-listener", ReadOnly: true},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "listener-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: "listener-config"},
				},
			}},
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: &grace,
		},
	}
}

// livePodFromDesired approximates what the API server and admission return after
// the desired pod is created: every field the controller set, plus the defaults
// and injections it did not.
func livePodFromDesired(desired *corev1.Pod) *corev1.Pod {
	live := desired.DeepCopy()
	defaultMode := int32(420)
	enableServiceLinks := true
	tolerationSeconds := int64(300)

	live.Spec.NodeName = "node-1"
	live.Spec.DNSPolicy = corev1.DNSClusterFirst
	live.Spec.SchedulerName = "default-scheduler"
	live.Spec.SecurityContext = &corev1.PodSecurityContext{}
	live.Spec.DeprecatedServiceAccount = desired.Spec.ServiceAccountName
	live.Spec.EnableServiceLinks = &enableServiceLinks
	live.Spec.PreemptionPolicy = ptr.To(corev1.PreemptLowerPriority)
	live.Spec.Priority = ptr.To(int32(0))
	live.Spec.Tolerations = []corev1.Toleration{
		{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists,
			Effect: corev1.TaintEffectNoExecute, TolerationSeconds: &tolerationSeconds},
		{Key: "node.kubernetes.io/unreachable", Operator: corev1.TolerationOpExists,
			Effect: corev1.TaintEffectNoExecute, TolerationSeconds: &tolerationSeconds},
	}
	live.Spec.Volumes[0].Secret.DefaultMode = &defaultMode
	live.Spec.Volumes = append(live.Spec.Volumes, corev1.Volume{
		Name: "kube-api-access-x7f2k",
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{DefaultMode: &defaultMode},
		},
	})
	live.Spec.Containers[0].TerminationMessagePath = corev1.TerminationMessagePathDefault
	live.Spec.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	live.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
	live.Spec.Containers[0].Ports[0].Protocol = corev1.ProtocolTCP
	live.Spec.Containers[0].VolumeMounts = append(live.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name: "kube-api-access-x7f2k", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true,
	})
	return live
}

// TestListenerPodSpecRequiresRecreation_SteadyState is the guard rail against
// swapping DeepDerivative for DeepEqual.
//
// A healthy listener pod carries many API-server defaults and admission
// injections that the desired pod never sets. If this comparison ever becomes
// strict, the controller deletes and recreates the pod on every reconcile, the
// listener never lives long enough to poll jobs, and every scale set in the
// cluster stops functioning. This test must keep passing.
func TestListenerPodSpecRequiresRecreation_SteadyState(t *testing.T) {
	desired := desiredListenerPod()
	live := livePodFromDesired(desired)

	assert.False(t, listenerPodSpecRequiresRecreation(live, desired),
		"a healthy pod carrying only API-server defaults must never be recreated; "+
			"if this fails, the comparison became too strict and will spin in a delete/create loop")
}

func TestListenerPodSpecRequiresRecreation(t *testing.T) {
	tests := map[string]struct {
		mutateDesired func(*corev1.Pod)
		want          bool
		why           string
	}{
		"identical": {
			mutateDesired: func(*corev1.Pod) {},
			want:          false,
			why:           "no drift",
		},
		"image changed": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.Containers[0].Image = "ghcr.io/actions/arc:0.2.0" },
			want:          true,
			why:           "an upgraded listener image must roll the pod",
		},
		"metrics port removed": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.Containers[0].Ports = nil },
			want:          true,
			why:           "disabling --listener-metrics-addr must remove the port from the running pod",
		},
		"metrics port value changed": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.Containers[0].Ports[0].ContainerPort = 9090 },
			want:          true,
			why:           "changing the metrics port must roll the pod",
		},
		"metrics port added": {
			mutateDesired: func(p *corev1.Pod) {
				p.Spec.Containers[0].Ports = append(p.Spec.Containers[0].Ports,
					corev1.ContainerPort{ContainerPort: 9090})
			},
			want: true,
			why:  "an added port must roll the pod",
		},
		"env var added": {
			mutateDesired: func(p *corev1.Pod) {
				p.Spec.Containers[0].Env = append(p.Spec.Containers[0].Env,
					corev1.EnvVar{Name: "HTTP_PROXY", Value: "http://proxy:8080"})
			},
			want: true,
			why:  "proxy configuration must roll the pod",
		},
		"env value changed": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.Containers[0].Env[0].Value = "/etc/other/config.json" },
			want:          true,
			why:           "a changed env value must roll the pod",
		},
		"config secret name changed": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.Volumes[0].Secret.SecretName = "other-config" },
			want:          true,
			why:           "pointing at a different config secret must roll the pod",
		},
		"service account changed": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.ServiceAccountName = "other-sa" },
			want:          true,
			why:           "a changed service account must roll the pod",
		},
		"nodeSelector key added": {
			mutateDesired: func(p *corev1.Pod) { p.Spec.NodeSelector["pool"] = "listeners" },
			want:          true,
			why:           "an added nodeSelector key must roll the pod",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			desired := desiredListenerPod()
			live := livePodFromDesired(desired)
			tc.mutateDesired(desired)

			assert.Equal(t, tc.want, listenerPodSpecRequiresRecreation(live, desired), tc.why)
		})
	}

	t.Run("nil handling", func(t *testing.T) {
		desired := desiredListenerPod()
		assert.False(t, listenerPodSpecRequiresRecreation(nil, nil))
		assert.True(t, listenerPodSpecRequiresRecreation(nil, desired))
		assert.True(t, listenerPodSpecRequiresRecreation(desired, nil))
	})
}

// TestListenerPodSpecRequiresRecreation_KnownDeepDerivativeLimits documents,
// rather than asserts away, the removals DeepDerivative cannot see. These are
// all sourced from the user-facing listener template, so they are handled
// upstream: the AutoscalingRunnerSet controller compares the whole
// AutoscalingListener spec with cmp.Equal and deletes the listener, which
// deletes the pod. If that upstream behaviour ever changes to a derivative
// comparison, these become real bugs.
func TestListenerPodSpecRequiresRecreation_KnownDeepDerivativeLimits(t *testing.T) {
	tests := map[string]func(*corev1.Pod){
		"all tolerations removed": func(p *corev1.Pod) { p.Spec.Tolerations = nil },
		"trailing volume removed": func(p *corev1.Pod) {
			p.Spec.Volumes = p.Spec.Volumes[:len(p.Spec.Volumes)-1]
		},
		"nodeSelector emptied": func(p *corev1.Pod) { p.Spec.NodeSelector = nil },
		"grace period removed": func(p *corev1.Pod) { p.Spec.TerminationGracePeriodSeconds = nil },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			desired := desiredListenerPod()
			live := livePodFromDesired(desired)
			mutate(desired)

			assert.False(t, listenerPodSpecRequiresRecreation(live, desired),
				"documented DeepDerivative limitation: removal is invisible here and is "+
					"instead handled by AutoscalingListener re-creation upstream")
		})
	}
}

// TestListenerPodSpecRequiresRecreation_MetricsToggleUsesRealBuilder exercises
// the drift check against genuine newScaleSetListenerPod output rather than a
// hand-written fixture, closing the gap between the fixture above and reality.
//
// metricsConfig is the one pod-spec input that does not come from any resource:
// it is derived from the --listener-metrics-addr controller flag. Because
// nothing in the AutoscalingListener spec changes when an operator disables
// metrics, the AutoscalingListener is not re-created and this comparison is the
// only thing that can remove the stale port from the running pod.
func TestListenerPodSpecRequiresRecreation_MetricsToggleUsesRealBuilder(t *testing.T) {
	autoscalingRunnerSet := v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-scale-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelKeyKubernetesPartOf:  labelValueKubernetesPartOf,
				LabelKeyKubernetesVersion: "0.2.0",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "1",
				AnnotationKeyGitHubRunnerGroupName:    "test-group",
				AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{GitHubConfigUrl: "https://github.com/org/repo"},
	}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	ephemeralRunnerSet, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)
	listener, err := b.newAutoscalingListener(&autoscalingRunnerSet, ephemeralRunnerSet, autoscalingRunnerSet.Namespace, "test:latest", nil)
	require.NoError(t, err)
	sa, err := b.newScaleSetListenerServiceAccount(listener)
	require.NoError(t, err)
	role := b.newScaleSetListenerRole(listener)
	roleBinding := b.newScaleSetListenerRoleBinding(listener, role, sa)

	build := func(metrics *listenerMetricsServerConfig) *corev1.Pod {
		// The cache keys on the listener object, not on metricsConfig, so it must
		// be bypassed to build both variants.
		b.ResourceCache.listenerPod.Delete(listener)
		pod, err := b.newScaleSetListenerPod(listener, &corev1.Secret{}, sa, role, roleBinding, metrics)
		require.NoError(t, err)
		return pod
	}

	withMetrics := build(&listenerMetricsServerConfig{addr: ":8080", endpoint: "/metrics"})
	withoutMetrics := build(nil)

	require.NotEmpty(t, withMetrics.Spec.Containers[0].Ports,
		"builder should expose a container port when metrics are enabled")
	require.Empty(t, withoutMetrics.Spec.Containers[0].Ports,
		"builder should expose no container port when metrics are disabled")

	assert.True(t, listenerPodSpecRequiresRecreation(withMetrics, withoutMetrics),
		"disabling --listener-metrics-addr must recreate a pod that still has the metrics port")
	assert.True(t, listenerPodSpecRequiresRecreation(withoutMetrics, withMetrics),
		"enabling --listener-metrics-addr must recreate a pod that has no metrics port")
	assert.False(t, listenerPodSpecRequiresRecreation(withMetrics, withMetrics),
		"an unchanged metrics configuration must not recreate the pod")
}

// The listener pod mounts its config as a secret volume and parses it once at
// startup, so a change to the secret contents is invisible in the pod spec but
// still requires a restart to take effect.
func TestListenerPodSpecRequiresRecreation_ConfigSecretChanged(t *testing.T) {
	tests := map[string]struct {
		liveVersion    string
		desiredVersion string
		want           bool
		why            string
	}{
		"unchanged": {
			liveVersion:    "100",
			desiredVersion: "100",
			want:           false,
			why:            "the config the listener is running is still the desired one",
		},
		"changed": {
			liveVersion:    "100",
			desiredVersion: "101",
			want:           true,
			why:            "the listener only reads its config at startup, so it must be restarted",
		},
		"missing on live pod": {
			liveVersion:    "",
			desiredVersion: "101",
			want:           false,
			why:            "pods predating the annotation must not all be recreated on controller upgrade",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			desired := desiredListenerPod()
			live := livePodFromDesired(desired)

			setListenerConfigVersion(live, tt.liveVersion)
			setListenerConfigVersion(desired, tt.desiredVersion)

			assert.Equal(t, tt.want, listenerPodSpecRequiresRecreation(live, desired), tt.why)
		})
	}
}

func setListenerConfigVersion(pod *corev1.Pod, version string) {
	if version == "" {
		delete(pod.Annotations, AnnotationKeyListenerConfigResourceVersion)
		return
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[AnnotationKeyListenerConfigResourceVersion] = version
}
