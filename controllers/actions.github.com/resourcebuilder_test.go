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
				LabelKeyKubernetesPartOf:  labelValueKubernetesPartOf,
				LabelKeyKubernetesVersion: "0.2.0",
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

	var b resourceBuilder
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

	listenerServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	listenerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	listenerPod, err := b.newScaleSetListenerPod(listener, &corev1.Secret{}, listenerServiceAccount, listenerSecret, nil)
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

		var b resourceBuilder
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

		var b resourceBuilder
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
