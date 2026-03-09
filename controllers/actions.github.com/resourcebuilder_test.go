package actionsgithubcom

import (
	"fmt"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMetadataPropagation(t *testing.T) {
	autoscalingRunnerSet := v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-scale-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelKeyKubernetesPartOf:          labelValueKubernetesPartOf,
				LabelKeyKubernetesVersion:         "0.2.0",
				"arbitrary-label":                 "random-value",
				"example.com/label":               "example-value",
				"example.com/example":             "example-value",
				"directly.excluded.org/label":     "excluded-value",
				"directly.excluded.org/arbitrary": "not-excluded-value",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "1",
				AnnotationKeyGitHubRunnerGroupName:    "test-group",
				AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/org/repo",
			AutoscalingListenerMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/autoscaling-listener-label": "autoscaling-listener-label",
				},
				Annotations: map[string]string{
					"test.com/autoscaling-listener-annotation": "autoscaling-listener-annotation",
				},
			},
			ListenerServiceAccountMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/listener-service-account-label": "listener-service-account-label",
				},
				Annotations: map[string]string{
					"test.com/listener-service-account-annotation": "listener-service-account-annotation",
				},
			},
			ListenerRoleMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/listener-role-label": "listener-role-label",
				},
				Annotations: map[string]string{
					"test.com/listener-role-annotation": "listener-role-annotation",
				},
			},
			ListenerRoleBindingMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/listener-role-binding-label": "listener-role-binding-label",
				},
				Annotations: map[string]string{
					"test.com/listener-role-binding-annotation": "listener-role-binding-annotation",
				},
			},
			ListenerConfigSecretMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/listener-config-secret-label": "listener-config-secret-label",
				},
				Annotations: map[string]string{
					"test.com/listener-config-secret-annotation": "listener-config-secret-annotation",
				},
			},
			EphemeralRunnerSetMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/ephemeral-runner-set-label": "ephemeral-runner-set-label",
				},
				Annotations: map[string]string{
					"test.com/ephemeral-runner-set-annotation": "ephemeral-runner-set-annotation",
				},
			},
			EphemeralRunnerMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/ephemeral-runner-label": "ephemeral-runner-label",
				},
				Annotations: map[string]string{
					"test.com/ephemeral-runner-annotation": "ephemeral-runner-annotation",
				},
			},
			EphemeralRunnerConfigSecretMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"test.com/ephemeral-runner-config-secret-label": "ephemeral-runner-config-secret-label",
				},
				Annotations: map[string]string{
					"test.com/ephemeral-runner-config-secret-annotation": "ephemeral-runner-config-secret-annotation",
				},
			},
		},
	}

	b := ResourceBuilder{
		ExcludeLabelPropagationPrefixes: []string{
			"example.com/",
			"directly.excluded.org/label",
		},
	}
	ephemeralRunnerSet, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)
	assert.Equal(t, labelValueKubernetesPartOf, ephemeralRunnerSet.Labels[LabelKeyKubernetesPartOf])
	assert.Equal(t, "runner-set", ephemeralRunnerSet.Labels[LabelKeyKubernetesComponent])
	assert.Equal(t, autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion], ephemeralRunnerSet.Labels[LabelKeyKubernetesVersion])
	assert.NotEmpty(t, ephemeralRunnerSet.Annotations[annotationKeyRunnerSpecHash])
	assert.Equal(t, autoscalingRunnerSet.Name, ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetName])
	assert.Equal(t, autoscalingRunnerSet.Namespace, ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetNamespace])
	assert.Equal(t, "", ephemeralRunnerSet.Labels[LabelKeyGitHubEnterprise])
	assert.Equal(t, "org", ephemeralRunnerSet.Labels[LabelKeyGitHubOrganization])
	assert.Equal(t, "repo", ephemeralRunnerSet.Labels[LabelKeyGitHubRepository])
	assert.Equal(t, autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName], ephemeralRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName])
	assert.Equal(t, autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName], ephemeralRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName])
	assert.Equal(t, autoscalingRunnerSet.Labels["arbitrary-label"], ephemeralRunnerSet.Labels["arbitrary-label"])
	assert.Equal(t, "ephemeral-runner-set-label", ephemeralRunnerSet.Labels["test.com/ephemeral-runner-set-label"])
	assert.Equal(t, "ephemeral-runner-set-annotation", ephemeralRunnerSet.Annotations["test.com/ephemeral-runner-set-annotation"])

	listener, err := b.newAutoScalingListener(&autoscalingRunnerSet, ephemeralRunnerSet, autoscalingRunnerSet.Namespace, "test:latest", nil)
	require.NoError(t, err)
	assert.Equal(t, labelValueKubernetesPartOf, listener.Labels[LabelKeyKubernetesPartOf])
	assert.Equal(t, "runner-scale-set-listener", listener.Labels[LabelKeyKubernetesComponent])
	assert.Equal(t, autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion], listener.Labels[LabelKeyKubernetesVersion])
	assert.NotEmpty(t, ephemeralRunnerSet.Annotations[annotationKeyRunnerSpecHash])
	assert.Equal(t, autoscalingRunnerSet.Name, listener.Labels[LabelKeyGitHubScaleSetName])
	assert.Equal(t, autoscalingRunnerSet.Namespace, listener.Labels[LabelKeyGitHubScaleSetNamespace])
	assert.Equal(t, "", listener.Labels[LabelKeyGitHubEnterprise])
	assert.Equal(t, "org", listener.Labels[LabelKeyGitHubOrganization])
	assert.Equal(t, "repo", listener.Labels[LabelKeyGitHubRepository])
	assert.Equal(t, autoscalingRunnerSet.Labels["arbitrary-label"], listener.Labels["arbitrary-label"])
	assert.Equal(t, "autoscaling-listener-label", listener.Labels["test.com/autoscaling-listener-label"])
	assert.Equal(t, "autoscaling-listener-annotation", listener.Annotations["test.com/autoscaling-listener-annotation"])

	assert.NotContains(t, listener.Labels, "example.com/label")
	assert.NotContains(t, listener.Labels, "example.com/example")
	assert.NotContains(t, listener.Labels, "directly.excluded.org/label")
	assert.Equal(t, "not-excluded-value", listener.Labels["directly.excluded.org/arbitrary"])

	listenerServiceAccount := b.newScaleSetListenerServiceAccount(listener)
	assert.Equal(t, "listener-service-account-label", listenerServiceAccount.Labels["test.com/listener-service-account-label"])
	assert.Equal(t, "listener-service-account-annotation", listenerServiceAccount.Annotations["test.com/listener-service-account-annotation"])

	listenerRole := b.newScaleSetListenerRole(listener)
	assert.Equal(t, "listener-role-label", listenerRole.Labels["test.com/listener-role-label"])
	assert.Equal(t, "listener-role-annotation", listenerRole.Annotations["test.com/listener-role-annotation"])

	listenerRoleBinding := b.newScaleSetListenerRoleBinding(listener, listenerRole, listenerServiceAccount)
	assert.Equal(t, "listener-role-binding-label", listenerRoleBinding.Labels["test.com/listener-role-binding-label"])
	assert.Equal(t, "listener-role-binding-annotation", listenerRoleBinding.Annotations["test.com/listener-role-binding-annotation"])

	listenerPod, err := b.newScaleSetListenerPod(listener, &corev1.Secret{}, listenerServiceAccount, nil)
	require.NoError(t, err)
	assert.Equal(t, listenerPod.Labels, listener.Labels)

	ephemeralRunner := b.newEphemeralRunner(ephemeralRunnerSet)
	require.NoError(t, err)

	for _, key := range commonLabelKeys {
		if key == LabelKeyKubernetesComponent {
			continue
		}
		assert.Equal(t, ephemeralRunnerSet.Labels[key], ephemeralRunner.Labels[key])
	}
	assert.Equal(t, "runner", ephemeralRunner.Labels[LabelKeyKubernetesComponent])
	assert.Equal(t, autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName], ephemeralRunner.Annotations[AnnotationKeyGitHubRunnerGroupName])
	assert.Equal(t, autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName], ephemeralRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName])
	assert.Equal(t, "ephemeral-runner-label", ephemeralRunner.Labels["test.com/ephemeral-runner-label"])
	assert.Equal(t, "ephemeral-runner-annotation", ephemeralRunner.Annotations["test.com/ephemeral-runner-annotation"])

	runnerSecret := b.newEphemeralRunnerJitSecret(ephemeralRunner, &actions.RunnerScaleSetJitRunnerConfig{
		Runner: &actions.RunnerReference{
			Id:               1,
			Name:             "test",
			RunnerScaleSetId: 1,
		},
		EncodedJITConfig: "",
	})
	assert.Equal(t, "ephemeral-runner-config-secret-label", runnerSecret.Labels["test.com/ephemeral-runner-config-secret-label"])
	assert.Equal(t, "ephemeral-runner-config-secret-annotation", runnerSecret.Annotations["test.com/ephemeral-runner-config-secret-annotation"])

	pod := b.newEphemeralRunnerPod(ephemeralRunner, runnerSecret)
	for key := range ephemeralRunner.Labels {
		assert.Equal(t, ephemeralRunner.Labels[key], pod.Labels[key])
	}
}

func TestGitHubURLTrimLabelValues(t *testing.T) {
	enterprise := strings.Repeat("a", 64)
	organization := strings.Repeat("b", 64)
	repository := strings.Repeat("c", 64)

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
	}

	t.Run("org/repo", func(t *testing.T) {
		autoscalingRunnerSet := autoscalingRunnerSet.DeepCopy()
		autoscalingRunnerSet.Spec = v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: fmt.Sprintf("https://github.com/%s/%s", organization, repository),
		}

		var b ResourceBuilder
		ephemeralRunnerSet, err := b.newEphemeralRunnerSet(autoscalingRunnerSet)
		require.NoError(t, err)
		assert.Len(t, ephemeralRunnerSet.Labels[LabelKeyGitHubEnterprise], 0)
		assert.Len(t, ephemeralRunnerSet.Labels[LabelKeyGitHubOrganization], 63)
		assert.Len(t, ephemeralRunnerSet.Labels[LabelKeyGitHubRepository], 63)
		assert.True(t, strings.HasSuffix(ephemeralRunnerSet.Labels[LabelKeyGitHubOrganization], trimLabelVauleSuffix))
		assert.True(t, strings.HasSuffix(ephemeralRunnerSet.Labels[LabelKeyGitHubRepository], trimLabelVauleSuffix))

		listener, err := b.newAutoScalingListener(autoscalingRunnerSet, ephemeralRunnerSet, autoscalingRunnerSet.Namespace, "test:latest", nil)
		require.NoError(t, err)
		assert.Len(t, listener.Labels[LabelKeyGitHubEnterprise], 0)
		assert.Len(t, listener.Labels[LabelKeyGitHubOrganization], 63)
		assert.Len(t, listener.Labels[LabelKeyGitHubRepository], 63)
		assert.True(t, strings.HasSuffix(ephemeralRunnerSet.Labels[LabelKeyGitHubOrganization], trimLabelVauleSuffix))
		assert.True(t, strings.HasSuffix(ephemeralRunnerSet.Labels[LabelKeyGitHubRepository], trimLabelVauleSuffix))
	})

	t.Run("enterprise", func(t *testing.T) {
		autoscalingRunnerSet := autoscalingRunnerSet.DeepCopy()
		autoscalingRunnerSet.Spec = v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: fmt.Sprintf("https://github.com/enterprises/%s", enterprise),
		}

		var b ResourceBuilder
		ephemeralRunnerSet, err := b.newEphemeralRunnerSet(autoscalingRunnerSet)
		require.NoError(t, err)
		assert.Len(t, ephemeralRunnerSet.Labels[LabelKeyGitHubEnterprise], 63)
		assert.True(t, strings.HasSuffix(ephemeralRunnerSet.Labels[LabelKeyGitHubEnterprise], trimLabelVauleSuffix))
		assert.Len(t, ephemeralRunnerSet.Labels[LabelKeyGitHubOrganization], 0)
		assert.Len(t, ephemeralRunnerSet.Labels[LabelKeyGitHubRepository], 0)

		listener, err := b.newAutoScalingListener(autoscalingRunnerSet, ephemeralRunnerSet, autoscalingRunnerSet.Namespace, "test:latest", nil)
		require.NoError(t, err)
		assert.Len(t, listener.Labels[LabelKeyGitHubEnterprise], 63)
		assert.True(t, strings.HasSuffix(ephemeralRunnerSet.Labels[LabelKeyGitHubEnterprise], trimLabelVauleSuffix))
		assert.Len(t, listener.Labels[LabelKeyGitHubOrganization], 0)
		assert.Len(t, listener.Labels[LabelKeyGitHubRepository], 0)
	})
}

func TestOwnershipRelationships(t *testing.T) {
	// Create an AutoscalingRunnerSet
	autoscalingRunnerSet := v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-scale-set",
			Namespace: "test-ns",
			UID:       "test-autoscaling-runner-set-uid",
			Labels: map[string]string{
				LabelKeyKubernetesPartOf:  labelValueKubernetesPartOf,
				LabelKeyKubernetesVersion: "0.2.0",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "1",
				AnnotationKeyGitHubRunnerGroupName:    "test-group",
				AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
				annotationKeyValuesHash:               "test-hash",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/org/repo",
		},
	}

	// Initialize ResourceBuilder
	b := ResourceBuilder{}

	// Create EphemeralRunnerSet
	ephemeralRunnerSet, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)

	// Test EphemeralRunnerSet ownership
	require.Len(t, ephemeralRunnerSet.OwnerReferences, 1, "EphemeralRunnerSet should have exactly one owner reference")
	ownerRef := ephemeralRunnerSet.OwnerReferences[0]
	assert.Equal(t, autoscalingRunnerSet.GetName(), ownerRef.Name, "Owner reference name should match AutoscalingRunnerSet name")
	assert.Equal(t, autoscalingRunnerSet.GetUID(), ownerRef.UID, "Owner reference UID should match AutoscalingRunnerSet UID")
	assert.Equal(t, true, *ownerRef.Controller, "Controller flag should be true")
	assert.Equal(t, true, *ownerRef.BlockOwnerDeletion, "BlockOwnerDeletion flag should be true")

	// Create EphemeralRunner
	ephemeralRunner := b.newEphemeralRunner(ephemeralRunnerSet)

	// Test EphemeralRunner ownership
	require.Len(t, ephemeralRunner.OwnerReferences, 1, "EphemeralRunner should have exactly one owner reference")
	ownerRef = ephemeralRunner.OwnerReferences[0]
	assert.Equal(t, ephemeralRunnerSet.GetName(), ownerRef.Name, "Owner reference name should match EphemeralRunnerSet name")
	assert.Equal(t, ephemeralRunnerSet.GetUID(), ownerRef.UID, "Owner reference UID should match EphemeralRunnerSet UID")
	assert.Equal(t, true, *ownerRef.Controller, "Controller flag should be true")
	assert.Equal(t, true, *ownerRef.BlockOwnerDeletion, "BlockOwnerDeletion flag should be true")

	// Create EphemeralRunnerPod
	runnerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-secret",
		},
	}
	pod := b.newEphemeralRunnerPod(ephemeralRunner, runnerSecret)

	// Test EphemeralRunnerPod ownership
	require.Len(t, pod.OwnerReferences, 1, "EphemeralRunnerPod should have exactly one owner reference")
	ownerRef = pod.OwnerReferences[0]
	assert.Equal(t, ephemeralRunner.GetName(), ownerRef.Name, "Owner reference name should match EphemeralRunner name")
	assert.Equal(t, ephemeralRunner.GetUID(), ownerRef.UID, "Owner reference UID should match EphemeralRunner UID")
	assert.Equal(t, true, *ownerRef.Controller, "Controller flag should be true")
	assert.Equal(t, true, *ownerRef.BlockOwnerDeletion, "BlockOwnerDeletion flag should be true")
}

func TestListenerPodNodeSelector(t *testing.T) {
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
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/org/repo",
		},
	}

	b := ResourceBuilder{}
	ephemeralRunnerSet, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)

	listener, err := b.newAutoScalingListener(&autoscalingRunnerSet, ephemeralRunnerSet, autoscalingRunnerSet.Namespace, "test:latest", nil)
	require.NoError(t, err)

	listenerServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}

	t.Run("default listener pod has linux nodeSelector", func(t *testing.T) {
		pod, err := b.newScaleSetListenerPod(listener, &corev1.Secret{}, listenerServiceAccount, nil)
		require.NoError(t, err)
		require.NotNil(t, pod.Spec.NodeSelector)
		assert.Equal(t, "linux", pod.Spec.NodeSelector[LabelKeyKubernetesOS],
			"listener pod should default to linux nodeSelector")
	})

	t.Run("nil listenerTemplate preserves linux nodeSelector", func(t *testing.T) {
		listenerNoTemplate := listener.DeepCopy()
		listenerNoTemplate.Spec.Template = nil

		pod, err := b.newScaleSetListenerPod(listenerNoTemplate, &corev1.Secret{}, listenerServiceAccount, nil)
		require.NoError(t, err)
		require.NotNil(t, pod.Spec.NodeSelector)
		assert.Equal(t, "linux", pod.Spec.NodeSelector[LabelKeyKubernetesOS],
			"listener pod should keep linux nodeSelector when no template is provided")
	})

	t.Run("listenerTemplate with nil nodeSelector preserves linux default", func(t *testing.T) {
		listenerWithTemplate := listener.DeepCopy()
		listenerWithTemplate.Spec.Template = &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				// NodeSelector intentionally nil
				Tolerations: []corev1.Toleration{
					{Key: "example.com/test", Operator: corev1.TolerationOpExists},
				},
			},
		}

		pod, err := b.newScaleSetListenerPod(listenerWithTemplate, &corev1.Secret{}, listenerServiceAccount, nil)
		require.NoError(t, err)
		require.NotNil(t, pod.Spec.NodeSelector,
			"linux nodeSelector should not be cleared by template with nil nodeSelector")
		assert.Equal(t, "linux", pod.Spec.NodeSelector[LabelKeyKubernetesOS])
		assert.Len(t, pod.Spec.Tolerations, 1, "other template fields should still be applied")
	})

	t.Run("listenerTemplate with explicit nodeSelector overrides default", func(t *testing.T) {
		listenerWithTemplate := listener.DeepCopy()
		listenerWithTemplate.Spec.Template = &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					LabelKeyKubernetesOS: "linux",
					"custom-label/pool":  "listeners",
				},
			},
		}

		pod, err := b.newScaleSetListenerPod(listenerWithTemplate, &corev1.Secret{}, listenerServiceAccount, nil)
		require.NoError(t, err)
		require.NotNil(t, pod.Spec.NodeSelector)
		assert.Equal(t, "linux", pod.Spec.NodeSelector[LabelKeyKubernetesOS])
		assert.Equal(t, "listeners", pod.Spec.NodeSelector["custom-label/pool"],
			"explicit template nodeSelector should be applied")
	})

	t.Run("listenerTemplate with empty nodeSelector overrides default", func(t *testing.T) {
		listenerWithTemplate := listener.DeepCopy()
		listenerWithTemplate.Spec.Template = &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{},
			},
		}

		pod, err := b.newScaleSetListenerPod(listenerWithTemplate, &corev1.Secret{}, listenerServiceAccount, nil)
		require.NoError(t, err)
		// An explicitly set empty map is non-nil, so it overrides the default.
		// This is intentional: the user explicitly opted out of nodeSelector constraints.
		assert.NotNil(t, pod.Spec.NodeSelector)
		assert.Empty(t, pod.Spec.NodeSelector,
			"explicitly empty nodeSelector should override the linux default")
	})
}
