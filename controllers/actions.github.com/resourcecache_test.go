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
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
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
		},
	}
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			UID:             "service-account-uid",
			ResourceVersion: "12",
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-uid",
			ResourceVersion: "13",
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "scale-set-ns",
			UID:             "role-binding-uid",
			ResourceVersion: "14",
		},
	}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, nil, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)

	cachedPod, ok := b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, serviceAccount, role, roleBinding)
	require.True(t, ok)
	assert.IsType(t, &corev1.Pod{}, cachedPod)

	role.ResourceVersion = "changed"
	_, ok = b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, serviceAccount, role, roleBinding)
	assert.False(t, ok)
}

// The proxy secret is a dependency only when the listener has a proxy. A nil
// dependency disables the cache, so a listener without a proxy must not pass one.
func TestResourceBuilderCachesListenerPodProxySecretDependency(t *testing.T) {
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
		},
		Spec: v1alpha1.AutoscalingListenerSpec{
			Image:                         "listener:latest",
			AutoscalingRunnerSetName:      "scale-set",
			AutoscalingRunnerSetNamespace: "scale-set-ns",
			EphemeralRunnerSetName:        "scale-set",
			Proxy: &v1alpha1.ProxyConfig{
				HTTP: &v1alpha1.ProxyServerConfig{Url: "http://proxy.example.com"},
			},
		},
	}
	podConfig := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-config", Namespace: "controller-ns", UID: "config-secret-uid", ResourceVersion: "11"}}
	proxySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-proxy", Namespace: "controller-ns", UID: "proxy-secret-uid", ResourceVersion: "15"}}
	serviceAccount := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "controller-ns", UID: "service-account-uid", ResourceVersion: "12"}}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "scale-set-ns", UID: "role-uid", ResourceVersion: "13"}}
	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "scale-set-ns", UID: "role-binding-uid", ResourceVersion: "14"}}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, proxySecret, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)
	assert.Equal(t, "15", listenerPod.Annotations[AnnotationKeyListenerProxySecretResourceVersion])

	_, ok := b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, proxySecret, serviceAccount, role, roleBinding)
	require.True(t, ok, "the pod must be cached against the proxy secret")

	rotated := proxySecret.DeepCopy()
	rotated.ResourceVersion = "16"
	_, ok = b.ResourceCache.listenerPod.Get(listener, listenerPod, podConfig, rotated, serviceAccount, role, roleBinding)
	assert.False(t, ok, "a rotated proxy secret must miss the cache")

	rebuilt, err := b.newScaleSetListenerPod(listener, podConfig, rotated, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)
	assert.Equal(t, "16", rebuilt.Annotations[AnnotationKeyListenerProxySecretResourceVersion],
		"the rebuilt pod must carry the rotated proxy secret's resource version")

	withoutProxy := listener.DeepCopy()
	withoutProxy.Spec.Proxy = nil
	plainPod, err := b.newScaleSetListenerPod(withoutProxy, podConfig, nil, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)
	assert.NotContains(t, plainPod.Annotations, AnnotationKeyListenerProxySecretResourceVersion)
	_, ok = b.ResourceCache.listenerPod.Get(withoutProxy, plainPod, podConfig, serviceAccount, role, roleBinding)
	assert.True(t, ok, "a listener without a proxy must still be cached")
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
	assert.Same(t, runnerSet, cachedRunnerSet)

	fromBuilder, err := b.newEphemeralRunnerSet(&autoscalingRunnerSet)
	require.NoError(t, err)
	assert.Same(t, runnerSet, fromBuilder)

	autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey] = "2"
	_, ok = b.ResourceCache.ephemeralRunnerSet.Get(&autoscalingRunnerSet, runnerSet)
	assert.False(t, ok)
}
