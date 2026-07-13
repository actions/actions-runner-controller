package actionsgithubcom

import (
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var benchmarkEphemeralRunnerSetSink *v1alpha1.EphemeralRunnerSet

func newTestResourceCache() *ResourceCache {
	cache := NewResourceCache()
	return &cache
}

func TestResourceCacheUpsertReplacesByDependencyResourceVersion(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			UID:             "listener-uid",
			ResourceVersion: "10",
		},
	}
	desiredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			ResourceVersion: "1",
			Labels: map[string]string{
				"app": "listener",
			},
		},
	}
	configSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener-config",
			Namespace:       "controller-ns",
			UID:             "config-secret-uid",
			ResourceVersion: "1",
		},
	}
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			UID:             "service-account-uid",
			ResourceVersion: "1",
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-uid",
			ResourceVersion: "1",
		},
	}

	cache := NewResourceCache()
	value, replaced := cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, replaced)
	_, ok := cache.listenerPod.Get(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, ok)
	assert.Equal(t, "1", value.ResourceVersion)

	_, replaced = cache.listenerPod.Upsert(mainObject, desiredPod, role, configSecret, serviceAccount)
	assert.False(t, replaced, "dependency ordering should not affect the cache value")
	_, ok = cache.listenerPod.Get(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, ok)

	configSecret.ResourceVersion = "2"
	value, replaced = cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, replaced)
	assert.Contains(t, value.Dependencies, ResourceCacheObjectRef{
		ObjectType:      resourceCacheObjectType(configSecret),
		Namespace:       "controller-ns",
		Name:            "listener-config",
		UID:             "config-secret-uid",
		ResourceVersion: "2",
	})

	desiredPod.Labels["mutated"] = "after-cache"
	cachedPod := value.Object
	assert.NotContains(t, cachedPod.Labels, "mutated")
}

func TestResourceCacheDeleteRemovesMainObjectEntries(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
		},
	}
	otherMainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-listener",
			Namespace: "controller-ns",
			UID:       "other-listener-uid",
		},
	}
	listenerPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "controller-ns"}}
	listenerServiceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "controller-ns"}}
	otherListenerPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other-listener", Namespace: "controller-ns"}}

	cache := NewResourceCache()
	cache.listenerPod.Upsert(mainObject, listenerPod)
	cache.listenerServiceAccount.Upsert(mainObject, listenerServiceAccount)
	cache.listenerPod.Upsert(otherMainObject, otherListenerPod)

	cache.Delete(mainObject)

	_, ok := cache.listenerPod.Get(mainObject, listenerPod)
	assert.False(t, ok)
	_, ok = cache.listenerServiceAccount.Get(mainObject, listenerServiceAccount)
	assert.False(t, ok)
	_, ok = cache.listenerPod.Get(otherMainObject, otherListenerPod)
	assert.True(t, ok)
}

func TestResourceBuilderCachesListenerPodDependencies(t *testing.T) {
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
			Annotations: map[string]string{
				annotationKeyIntegrityHash: "listener-hash",
			},
		},
		Spec: v1alpha1.AutoscalingListenerSpec{
			Image:                         "listener:latest",
			AutoscalingRunnerSetName:      "scale-set",
			AutoscalingRunnerSetNamespace: "scale-set-ns",
			EphemeralRunnerSetName:        "scale-set",
		},
	}
	podConfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener-config",
			Namespace:       "controller-ns",
			UID:             "config-secret-uid",
			ResourceVersion: "11",
			Annotations: map[string]string{
				annotationKeyIntegrityHash: "config-hash",
			},
		},
	}
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			UID:             "service-account-uid",
			ResourceVersion: "12",
			Annotations: map[string]string{
				annotationKeyIntegrityHash: "service-account-hash",
			},
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-uid",
			ResourceVersion: "13",
			Annotations: map[string]string{
				annotationKeyIntegrityHash: "role-hash",
			},
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-binding-uid",
			ResourceVersion: "14",
			Annotations: map[string]string{
				annotationKeyIntegrityHash: "role-binding-hash",
			},
		},
	}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)

	cachedPod, ok := b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, serviceAccount, role, roleBinding)
	require.True(t, ok)
	assert.IsType(t, &corev1.Pod{}, cachedPod)

	role.ResourceVersion = "changed"
	_, ok = b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, serviceAccount, role, roleBinding)
	assert.False(t, ok)
}

func TestResourceBuilderCachesEphemeralRunnerSet(t *testing.T) {
	autoscalingRunnerSet := v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scale-set",
			Namespace: "default",
			UID:       "scale-set-uid",
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey: "1",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/actions/actions-runner-controller",
		},
	}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	runnerSet, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)

	cachedRunnerSet, ok := b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, runnerSet)
	require.True(t, ok)
	assert.Equal(t, runnerSet.Spec, cachedRunnerSet.Spec)

	runnerSet.Labels["mutated"] = "after-cache"
	assert.NotContains(t, cachedRunnerSet.Labels, "mutated")

	fromBuilder, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)
	assert.NotContains(t, fromBuilder.Labels, "mutated")

	autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey] = "2"
	_, ok = b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, runnerSet)
	assert.False(t, ok)
}

func BenchmarkNewEphemeralRunnerSetResourceCache(b *testing.B) {
	autoscalingRunnerSet := newBenchmarkAutoscalingRunnerSet()

	b.Run("no_cache", func(b *testing.B) {
		builder := ResourceBuilder{}
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			runnerSet, err := builder.newEphemeralRunnerSet(autoscalingRunnerSet)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEphemeralRunnerSetSink = runnerSet
		}
	})

	b.Run("cache_hit", func(b *testing.B) {
		cache := NewResourceCache()
		builder := ResourceBuilder{ResourceCache: &cache}
		if _, err := builder.newEphemeralRunnerSet(autoscalingRunnerSet); err != nil {
			b.Fatal(err)
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			runnerSet, err := builder.newEphemeralRunnerSet(autoscalingRunnerSet)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEphemeralRunnerSetSink = runnerSet
		}
	})

	b.Run("cache_miss", func(b *testing.B) {
		cache := NewResourceCache()
		builder := ResourceBuilder{ResourceCache: &cache}
		autoscalingRunnerSet := autoscalingRunnerSet.DeepCopy()

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			autoscalingRunnerSet.ResourceVersion = fmt.Sprint(i)
			runnerSet, err := builder.newEphemeralRunnerSet(autoscalingRunnerSet)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEphemeralRunnerSetSink = runnerSet
		}
	})
}

func newBenchmarkAutoscalingRunnerSet() *v1alpha1.AutoscalingRunnerSet {
	return &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "benchmark-scale-set",
			Namespace:       "benchmark-namespace",
			UID:             "benchmark-scale-set-uid",
			ResourceVersion: "1",
			Labels: map[string]string{
				LabelKeyKubernetesVersion: "0.12.0",
				"example.com/label-1":     "value-1",
				"example.com/label-2":     "value-2",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "123",
				AnnotationKeyGitHubRunnerGroupName:    "benchmark-runner-group",
				AnnotationKeyGitHubRunnerScaleSetName: "benchmark-scale-set",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/actions/actions-runner-controller",
			EphemeralRunnerSetMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"example.com/runner-set-label": "runner-set-value",
				},
				Annotations: map[string]string{
					"example.com/runner-set-annotation": "runner-set-value",
				},
			},
			EphemeralRunnerMetadata: &v1alpha1.ResourceMeta{
				Labels: map[string]string{
					"example.com/runner-label": "runner-value",
				},
				Annotations: map[string]string{
					"example.com/runner-annotation": "runner-value",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"example.com/template-label": "template-value",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  v1alpha1.EphemeralRunnerContainerName,
							Image: "ghcr.io/actions/actions-runner:latest",
							Env: []corev1.EnvVar{
								{Name: "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER", Value: "false"},
							},
						},
					},
				},
			},
		},
	}
}
