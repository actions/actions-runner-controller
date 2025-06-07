package actionsgithubcom

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLabelPropagation(t *testing.T) {
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
				runnerScaleSetIdAnnotationKey:         "1",
				AnnotationKeyGitHubRunnerGroupName:    "test-group",
				AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/org/repo",
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

	assert.NotContains(t, listener.Labels, "example.com/label")
	assert.NotContains(t, listener.Labels, "example.com/example")
	assert.NotContains(t, listener.Labels, "directly.excluded.org/label")
	assert.Equal(t, "not-excluded-value", listener.Labels["directly.excluded.org/arbitrary"])

	listenerServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
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

	runnerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	pod := b.newEphemeralRunnerPod(context.TODO(), ephemeralRunner, runnerSecret)
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
				runnerScaleSetIdAnnotationKey:         "1",
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
				runnerScaleSetIdAnnotationKey:         "1",
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
	pod := b.newEphemeralRunnerPod(context.TODO(), ephemeralRunner, runnerSecret)

	// Test EphemeralRunnerPod ownership
	require.Len(t, pod.OwnerReferences, 1, "EphemeralRunnerPod should have exactly one owner reference")
	ownerRef = pod.OwnerReferences[0]
	assert.Equal(t, ephemeralRunner.GetName(), ownerRef.Name, "Owner reference name should match EphemeralRunner name")
	assert.Equal(t, ephemeralRunner.GetUID(), ownerRef.UID, "Owner reference UID should match EphemeralRunner UID")
	assert.Equal(t, true, *ownerRef.Controller, "Controller flag should be true")
	assert.Equal(t, true, *ownerRef.BlockOwnerDeletion, "BlockOwnerDeletion flag should be true")
}
