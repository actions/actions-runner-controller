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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newTestResourceCache() *ResourceCache {
	cache := NewResourceCache()
	return &cache
}

func resourceCacheHasMainObjectEntries(cache *ResourceCache, mainObject client.Object) bool {
	return resourceCacheStateHasMainObjectEntries(cache.autoscalingListener, mainObject) ||
		resourceCacheStateHasMainObjectEntries(cache.ephemeralRunnerSet, mainObject) ||
		resourceCacheStateHasMainObjectEntries(cache.listenerPod, mainObject) ||
		resourceCacheStateHasMainObjectEntries(cache.listenerServiceAccount, mainObject) ||
		resourceCacheStateHasMainObjectEntries(cache.listenerRole, mainObject) ||
		resourceCacheStateHasMainObjectEntries(cache.listenerRoleBinding, mainObject)
}

func resourceCacheStateHasMainObjectEntries[T client.Object](state *resourceCacheState[T], mainObject client.Object) bool {
	uid := mainObject.GetUID()
	if uid == "" {
		return false
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	return len(state.entriesByMainUID[uid]) > 0
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
	staleConfigSecret := configSecret.DeepCopy()
	staleConfigSecret.ResourceVersion = "1"
	_, ok = cache.listenerPod.Get(mainObject, desiredPod, staleConfigSecret, serviceAccount, role)
	assert.False(t, ok)
	_, ok = cache.listenerPod.Get(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, ok)

	assert.Same(t, desiredPod, value.Object)
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

func TestResourceCacheDeletePanicsWithNilCache(t *testing.T) {
	var cache *ResourceCache
	assert.Panics(t, func() {
		cache.Delete(&v1alpha1.AutoscalingListener{})
	})
}

func TestResourceCacheIgnoresInvalidInputs(t *testing.T) {
	cache := NewResourceCache()
	desiredPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "controller-ns"}}
	mainObjectWithoutUID := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "controller-ns"}}
	mainObject := mainObjectWithoutUID.DeepCopy()
	mainObject.UID = "listener-uid"

	_, replaced := cache.listenerPod.Upsert(mainObjectWithoutUID, desiredPod)
	assert.False(t, replaced)
	_, ok := cache.listenerPod.Get(mainObjectWithoutUID, desiredPod)
	assert.False(t, ok)

	var nilDependency *corev1.Secret
	assert.NotPanics(t, func() {
		_, replaced = cache.listenerPod.Upsert(mainObject, desiredPod, nilDependency)
		assert.False(t, replaced)
		_, ok = cache.listenerPod.Get(mainObject, desiredPod, nilDependency)
		assert.False(t, ok)
	})

	tooManyDependencies := make([]client.Object, resourceCacheMaxDependencyRefs+1)
	for i := range tooManyDependencies {
		tooManyDependencies[i] = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("dependency-%d", i), Namespace: "controller-ns"}}
	}
	assert.NotPanics(t, func() {
		_, replaced = cache.listenerPod.Upsert(mainObject, desiredPod, tooManyDependencies...)
		assert.False(t, replaced)
		_, ok = cache.listenerPod.Get(mainObject, desiredPod, tooManyDependencies...)
		assert.False(t, ok)
	})
}

func TestResourceBuilderCachesListenerPodDependencies(t *testing.T) {
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "listener",
			Namespace:   "controller-ns",
			UID:         "listener-uid",
			Annotations: map[string]string{"example.com/listener-hash": "listener-hash"},
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
			Annotations:     map[string]string{"example.com/config-hash": "config-hash"},
		},
	}
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			UID:             "service-account-uid",
			ResourceVersion: "12",
			Annotations:     map[string]string{"example.com/service-account-hash": "service-account-hash"},
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-uid",
			ResourceVersion: "13",
			Annotations:     map[string]string{"example.com/role-hash": "role-hash"},
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-binding-uid",
			ResourceVersion: "14",
			Annotations:     map[string]string{"example.com/role-binding-hash": "role-binding-hash"},
		},
	}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)

	metadataDependency := resourceCacheObjectMetadataInputObject(listener)
	cachedPod, ok := b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, serviceAccount, role, roleBinding, metadataDependency)
	require.True(t, ok)
	assert.IsType(t, &corev1.Pod{}, cachedPod)

	lookupPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerPod.Name,
			Namespace: listenerPod.Namespace,
		},
	}
	cachedPod, ok = b.ResourceCache.listenerPod.Get(listener, lookupPod, podConfig, serviceAccount, role, roleBinding, metadataDependency)
	require.True(t, ok, "name-only lookup object should hit the cached desired pod")
	assert.Same(t, listenerPod, cachedPod)

	role.ResourceVersion = "changed"
	_, ok = b.ResourceCache.listenerPod.Get(listener, lookupPod, podConfig, serviceAccount, role, roleBinding, metadataDependency)
	assert.False(t, ok)
}

func TestResourceBuilderCachesListenerPodMetadataDependency(t *testing.T) {
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
			Labels: map[string]string{
				"arc.test/listener-label": "initial",
			},
		},
		Spec: v1alpha1.AutoscalingListenerSpec{
			Image:                         "listener:latest",
			AutoscalingRunnerSetName:      "scale-set",
			AutoscalingRunnerSetNamespace: "scale-set-ns",
			EphemeralRunnerSetName:        "scale-set",
		},
	}
	podConfig := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-config", Namespace: "controller-ns", UID: "config-secret-uid", ResourceVersion: "11"}}
	serviceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "controller-ns", UID: "service-account-uid", ResourceVersion: "12"}}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "scale-set-ns", UID: "role-uid", ResourceVersion: "13"}}
	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "scale-set-ns", UID: "role-binding-uid", ResourceVersion: "14"}}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)

	lookupPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: listenerPod.Name, Namespace: listenerPod.Namespace}}
	_, ok := b.ResourceCache.listenerPod.Get(listener, lookupPod, podConfig, serviceAccount, role, roleBinding, resourceCacheObjectMetadataInputObject(listener))
	assert.True(t, ok)

	listener.Labels["arc.test/listener-label"] = "updated"
	_, ok = b.ResourceCache.listenerPod.Get(listener, lookupPod, podConfig, serviceAccount, role, roleBinding, resourceCacheObjectMetadataInputObject(listener))
	assert.False(t, ok, "cache miss when listener metadata used by the pod changes")
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

	metadataDependency := resourceCacheObjectMetadataInputObject(&autoscalingRunnerSet)
	cachedRunnerSet, ok := b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, runnerSet, metadataDependency)
	require.True(t, ok, "direct cache Get with returned object should hit")
	assert.Equal(t, runnerSet.Spec, cachedRunnerSet.Spec)
	assert.Same(t, runnerSet, cachedRunnerSet)

	lookupRunnerSet := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: runnerSet.Name, Namespace: runnerSet.Namespace}}
	cachedRunnerSet, ok = b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, lookupRunnerSet, metadataDependency)
	require.True(t, ok, "name-only lookup object should hit the cached desired runner set")
	assert.Same(t, runnerSet, cachedRunnerSet)

	autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey] = "2"
	_, ok = b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, lookupRunnerSet, metadataDependency)
	assert.True(t, ok, "cache should be valid when main object generation unchanged")
}

func TestResourceBuilderCachesEphemeralRunnerSetMetadataDependency(t *testing.T) {
	autoscalingRunnerSet := v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scale-set",
			Namespace: "default",
			UID:       "scale-set-uid",
			Labels: map[string]string{
				"arc.test/scale-set-label": "initial",
			},
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

	lookupRunnerSet := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: runnerSet.Name, Namespace: runnerSet.Namespace}}
	_, ok := b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, lookupRunnerSet, resourceCacheObjectMetadataInputObject(&autoscalingRunnerSet))
	assert.True(t, ok)

	autoscalingRunnerSet.Labels["arc.test/scale-set-label"] = "updated"
	_, ok = b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, lookupRunnerSet, resourceCacheObjectMetadataInputObject(&autoscalingRunnerSet))
	assert.False(t, ok, "cache miss when autoscaling runner set metadata used by the runner set changes")
}

func TestResourceCacheOwnerGenerationDoesNotAffectCacheEntry(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "listener",
			Namespace:  "controller-ns",
			UID:        "listener-uid",
			Generation: 5,
		},
	}
	desiredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
		},
	}

	cache := NewResourceCache()
	_, replaced := cache.listenerPod.Upsert(mainObject, desiredPod)
	assert.True(t, replaced)
	_, ok := cache.listenerPod.Get(mainObject, desiredPod)
	assert.True(t, ok)

	mainObjectCopy := mainObject.DeepCopy()
	_, ok = cache.listenerPod.Get(mainObjectCopy, desiredPod)
	assert.True(t, ok, "cache hit when owner generation unchanged")
}

func TestResourceCacheOwnerGenerationChangeInvalidatesCacheEntry(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "listener",
			Namespace:  "controller-ns",
			UID:        "listener-uid",
			Generation: 5,
		},
	}
	desiredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
		},
	}

	cache := NewResourceCache()
	_, replaced := cache.listenerPod.Upsert(mainObject, desiredPod)
	assert.True(t, replaced)
	_, ok := cache.listenerPod.Get(mainObject, desiredPod)
	assert.True(t, ok)

	mainObjectWithNewGeneration := mainObject.DeepCopy()
	mainObjectWithNewGeneration.Generation = 6
	_, ok = cache.listenerPod.Get(mainObjectWithNewGeneration, desiredPod)
	assert.False(t, ok, "cache miss when owner generation changes")
}

func TestResourceCacheDependencyResourceVersionChangeInvalidates(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
		},
	}
	desiredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
		},
	}
	dependency := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "config",
			Namespace:       "controller-ns",
			UID:             "config-uid",
			ResourceVersion: "1",
		},
	}

	cache := NewResourceCache()
	_, replaced := cache.listenerPod.Upsert(mainObject, desiredPod, dependency)
	assert.True(t, replaced)
	_, ok := cache.listenerPod.Get(mainObject, desiredPod, dependency)
	assert.True(t, ok)

	dependencyWithNewResourceVersion := dependency.DeepCopy()
	dependencyWithNewResourceVersion.ResourceVersion = "2"
	_, ok = cache.listenerPod.Get(mainObject, desiredPod, dependencyWithNewResourceVersion)
	assert.False(t, ok, "cache miss when dependency resourceVersion changes")
}

func TestResourceCacheNoAnnotationFallback(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
		},
	}
	desiredPodWithoutResourceVersion := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
		},
	}
	desiredPodWithDifferentAnnotation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			Annotations: map[string]string{
				"unrelated-key": "unrelated-value",
			},
		},
	}

	cache := NewResourceCache()
	_, replaced := cache.listenerPod.Upsert(mainObject, desiredPodWithoutResourceVersion)
	assert.True(t, replaced)

	_, ok := cache.listenerPod.Get(mainObject, desiredPodWithoutResourceVersion)
	assert.True(t, ok, "cache hit with same pod object")

	_, ok = cache.listenerPod.Get(mainObject, desiredPodWithDifferentAnnotation)
	assert.False(t, ok, "cache miss when pod changed - uses hash not annotation fallback")

	desiredPodIdentical := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
		},
	}
	_, ok = cache.listenerPod.Get(mainObject, desiredPodIdentical)
	assert.True(t, ok, "cache hit when pod structure identical even if different instance")
}
