package actionsgithubcom

import (
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
	_, ok := cache.listenerPod.Value(mainObject, desiredPod)
	assert.True(t, ok)
	assert.Equal(t, "1", value.ResourceVersion)

	_, replaced = cache.listenerPod.Upsert(mainObject, desiredPod, role, configSecret, serviceAccount)
	assert.False(t, replaced, "dependency ordering should not affect the cache value")
	_, ok = cache.listenerPod.Value(mainObject, desiredPod)
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

func TestResourceCacheDeleteByMainUID(t *testing.T) {
	listenerA := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener-a", Namespace: "ns", UID: "listener-a-uid"}}
	listenerB := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener-b", Namespace: "ns", UID: "listener-b-uid"}}
	podA := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "listener-a", Namespace: "ns"}}
	podB := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "listener-b", Namespace: "ns"}}

	cache := NewResourceCache()
	cache.listenerPod.Upsert(listenerA, podA)
	cache.listenerConfigSecret.Upsert(listenerA, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-a-config", Namespace: "ns"}})
	cache.listenerPod.Upsert(listenerB, podB)

	assert.Equal(t, 2, cache.DeleteByMainUID(listenerA.UID))
	_, ok := cache.listenerPod.Value(listenerA, podA)
	assert.False(t, ok)
	_, ok = cache.listenerConfigSecret.Value(listenerA, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-a-config", Namespace: "ns"}})
	assert.False(t, ok)
	_, ok = cache.listenerPod.Value(listenerB, podB)
	assert.True(t, ok)
	assert.Equal(t, 0, cache.DeleteByMainUID(listenerA.UID))
}

func TestResourceCacheSeparatesResourceTypes(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "ns", UID: "listener-uid"}}
	listenerSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "ns", ResourceVersion: "1"}}
	proxySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "ns", ResourceVersion: "2"}}

	cache := NewResourceCache()
	cache.listenerConfigSecret.Upsert(mainObject, listenerSecret)
	cache.autoscalingListenerProxySecret.Upsert(mainObject, proxySecret)

	value, ok := cache.listenerConfigSecret.Value(mainObject, listenerSecret)
	require.True(t, ok)
	assert.Equal(t, "1", value.ResourceVersion)
	value, ok = cache.autoscalingListenerProxySecret.Value(mainObject, proxySecret)
	require.True(t, ok)
	assert.Equal(t, "2", value.ResourceVersion)
}

func TestResourceBuilderCachesListenerPodDependencies(t *testing.T) {
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "listener",
			Namespace: "controller-ns",
			UID:       "listener-uid",
			Annotations: map[string]string{
				AnnotationKeyIntegrityHash: "listener-hash",
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
				AnnotationKeyIntegrityHash: "config-hash",
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
				AnnotationKeyIntegrityHash: "service-account-hash",
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
				AnnotationKeyIntegrityHash: "role-hash",
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
				AnnotationKeyIntegrityHash: "role-binding-hash",
			},
		},
	}

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)

	value, ok := b.ResourceCache.listenerPod.Value(listener, listenerPod)
	require.True(t, ok)
	assert.IsType(t, &corev1.Pod{}, value.Object)
	assert.ElementsMatch(t, []ResourceCacheObjectRef{
		newResourceCacheObjectRef(podConfig),
		newResourceCacheObjectRef(serviceAccount),
		newResourceCacheObjectRef(role),
		newResourceCacheObjectRef(roleBinding),
	}, value.Dependencies)
}

func BenchmarkResourceCacheGetHit(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	cache := NewResourceCache()
	cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := cache.listenerPod.Get(mainObject, desiredPod, configSecret, serviceAccount, role); !ok {
			b.Fatal("expected cache hit")
		}
	}
}

func BenchmarkResourceCacheUpsertNoChange(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	cache := NewResourceCache()
	cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, replaced := cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role); replaced {
			b.Fatal("expected unchanged cache entry")
		}
	}
}

func BenchmarkResourceCacheUpsertReplace(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	cache := NewResourceCache()
	cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		desiredPod.ResourceVersion = strconv.Itoa(i)
		if _, replaced := cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role); !replaced {
			b.Fatal("expected cache replacement")
		}
	}
}

func BenchmarkResourceCacheUpsertNoChangeParallel(b *testing.B) {
	podCases := newResourceCacheParallelPodBenchmarkCases()
	operations := newResourceCacheParallelBenchmarkOperations()

	b.Run("SameResourceType", func(b *testing.B) {
		cache := NewResourceCache()
		for _, tc := range podCases {
			cache.listenerPod.Upsert(tc.mainObject, tc.desiredPod, tc.dependencies...)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				tc := podCases[index.Add(1)%uint64(len(podCases))]
				if _, replaced := cache.listenerPod.Upsert(tc.mainObject, tc.desiredPod, tc.dependencies...); replaced {
					b.Fatal("expected unchanged cache entry")
				}
			}
		})
	})

	b.Run("DifferentResourceTypes", func(b *testing.B) {
		cache := NewResourceCache()
		for _, op := range operations {
			op.seed(&cache)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				op := operations[index.Add(1)%uint64(len(operations))]
				if op.upsertNoChange(&cache) {
					b.Fatal("expected unchanged cache entry")
				}
			}
		})
	})
}

func BenchmarkResourceCacheUpsertReplaceParallel(b *testing.B) {
	podCases := newResourceCacheParallelPodBenchmarkCases()
	operations := newResourceCacheParallelBenchmarkOperations()

	b.Run("SameResourceType", func(b *testing.B) {
		cache := NewResourceCache()
		for _, tc := range podCases {
			cache.listenerPod.Upsert(tc.mainObject, tc.desiredPod, tc.dependencies...)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				iteration := index.Add(1)
				tc := podCases[iteration%uint64(len(podCases))]
				desiredPod := tc.desiredPod.DeepCopy()
				desiredPod.SetResourceVersion(strconv.FormatUint(iteration, 10))
				if _, replaced := cache.listenerPod.Upsert(tc.mainObject, desiredPod, tc.dependencies...); !replaced {
					b.Fatal("expected cache replacement")
				}
			}
		})
	})

	b.Run("DifferentResourceTypes", func(b *testing.B) {
		cache := NewResourceCache()
		for _, op := range operations {
			op.seed(&cache)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				iteration := index.Add(1)
				op := operations[iteration%uint64(len(operations))]
				if !op.upsertReplace(&cache, iteration) {
					b.Fatal("expected cache replacement")
				}
			}
		})
	})
}

func newResourceCacheBenchmarkObjects() (*v1alpha1.AutoscalingListener, *corev1.Pod, *corev1.Secret, *corev1.ServiceAccount, *rbacv1.Role) {
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

	return mainObject, desiredPod, configSecret, serviceAccount, role
}

type resourceCachePodBenchmarkCase struct {
	mainObject   *v1alpha1.AutoscalingListener
	desiredPod   *corev1.Pod
	dependencies []client.Object
}

type resourceCacheBenchmarkOperation struct {
	seed           func(*ResourceCache)
	upsertNoChange func(*ResourceCache) bool
	upsertReplace  func(*ResourceCache, uint64) bool
}

func newResourceCacheParallelPodBenchmarkCases() []resourceCachePodBenchmarkCase {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()

	listenerB := mainObject.DeepCopy()
	listenerB.Name = "listener-pod-b"
	listenerB.UID = "listener-pod-b-uid"
	desiredPodB := desiredPod.DeepCopy()
	desiredPodB.Name = listenerB.Name

	listenerC := mainObject.DeepCopy()
	listenerC.Name = "listener-pod-c"
	listenerC.UID = "listener-pod-c-uid"
	desiredPodC := desiredPod.DeepCopy()
	desiredPodC.Name = listenerC.Name

	listenerD := mainObject.DeepCopy()
	listenerD.Name = "listener-pod-d"
	listenerD.UID = "listener-pod-d-uid"
	desiredPodD := desiredPod.DeepCopy()
	desiredPodD.Name = listenerD.Name

	return []resourceCachePodBenchmarkCase{
		{
			mainObject:   mainObject,
			desiredPod:   desiredPod,
			dependencies: []client.Object{configSecret, serviceAccount, role},
		},
		{
			mainObject:   listenerB,
			desiredPod:   desiredPodB,
			dependencies: []client.Object{configSecret, serviceAccount, role},
		},
		{
			mainObject:   listenerC,
			desiredPod:   desiredPodC,
			dependencies: []client.Object{configSecret, serviceAccount, role},
		},
		{
			mainObject:   listenerD,
			desiredPod:   desiredPodD,
			dependencies: []client.Object{configSecret, serviceAccount, role},
		},
	}
}

func newResourceCacheParallelBenchmarkOperations() []resourceCacheBenchmarkOperation {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()

	listener := mainObject.DeepCopy()
	listener.Name = "listener-service-account"
	listener.UID = "listener-service-account-uid"
	serviceAccountDesired := serviceAccount.DeepCopy()

	listenerRole := mainObject.DeepCopy()
	listenerRole.Name = "listener-role"
	listenerRole.UID = "listener-role-uid"
	roleDesired := role.DeepCopy()

	listenerConfig := mainObject.DeepCopy()
	listenerConfig.Name = "listener-config"
	listenerConfig.UID = "listener-config-uid"
	configSecretDesired := configSecret.DeepCopy()

	return []resourceCacheBenchmarkOperation{
		{
			seed: func(cache *ResourceCache) {
				cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)
			},
			upsertNoChange: func(cache *ResourceCache) bool {
				_, replaced := cache.listenerPod.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)
				return replaced
			},
			upsertReplace: func(cache *ResourceCache, iteration uint64) bool {
				pod := desiredPod.DeepCopy()
				pod.SetResourceVersion(strconv.FormatUint(iteration, 10))
				_, replaced := cache.listenerPod.Upsert(mainObject, pod, configSecret, serviceAccount, role)
				return replaced
			},
		},
		{
			seed: func(cache *ResourceCache) {
				cache.listenerServiceAccount.Upsert(listener, serviceAccountDesired)
			},
			upsertNoChange: func(cache *ResourceCache) bool {
				_, replaced := cache.listenerServiceAccount.Upsert(listener, serviceAccountDesired)
				return replaced
			},
			upsertReplace: func(cache *ResourceCache, iteration uint64) bool {
				serviceAccount := serviceAccountDesired.DeepCopy()
				serviceAccount.SetResourceVersion(strconv.FormatUint(iteration, 10))
				_, replaced := cache.listenerServiceAccount.Upsert(listener, serviceAccount)
				return replaced
			},
		},
		{
			seed: func(cache *ResourceCache) {
				cache.listenerRole.Upsert(listenerRole, roleDesired)
			},
			upsertNoChange: func(cache *ResourceCache) bool {
				_, replaced := cache.listenerRole.Upsert(listenerRole, roleDesired)
				return replaced
			},
			upsertReplace: func(cache *ResourceCache, iteration uint64) bool {
				role := roleDesired.DeepCopy()
				role.SetResourceVersion(strconv.FormatUint(iteration, 10))
				_, replaced := cache.listenerRole.Upsert(listenerRole, role)
				return replaced
			},
		},
		{
			seed: func(cache *ResourceCache) {
				cache.listenerConfigSecret.Upsert(listenerConfig, configSecretDesired)
			},
			upsertNoChange: func(cache *ResourceCache) bool {
				_, replaced := cache.listenerConfigSecret.Upsert(listenerConfig, configSecretDesired)
				return replaced
			},
			upsertReplace: func(cache *ResourceCache, iteration uint64) bool {
				secret := configSecretDesired.DeepCopy()
				secret.SetResourceVersion(strconv.FormatUint(iteration, 10))
				_, replaced := cache.listenerConfigSecret.Upsert(listenerConfig, secret)
				return replaced
			},
		},
	}
}
