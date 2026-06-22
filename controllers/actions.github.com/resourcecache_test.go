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

	var cache ResourceCache
	value, replaced := cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, replaced)
	assert.Equal(t, 1, cache.Len())
	_, ok := cache.Value(resourceCacheShardListenerPod, mainObject, desiredPod)
	assert.True(t, ok)
	assert.Equal(t, "1", value.ResourceVersion)

	_, replaced = cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, role, configSecret, serviceAccount)
	assert.False(t, replaced, "dependency ordering should not affect the cache value")
	assert.Equal(t, 1, cache.Len())

	configSecret.ResourceVersion = "2"
	value, replaced = cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, replaced)
	assert.Equal(t, 1, cache.Len())
	assert.Contains(t, value.Dependencies, ResourceCacheObjectRef{
		ObjectType:      resourceCacheObjectType(configSecret),
		Namespace:       "controller-ns",
		Name:            "listener-config",
		UID:             "config-secret-uid",
		ResourceVersion: "2",
	})

	desiredPod.Labels["mutated"] = "after-cache"
	cachedPod := value.Object.(*corev1.Pod)
	assert.NotContains(t, cachedPod.Labels, "mutated")
}

func TestResourceCacheDeleteByMainUID(t *testing.T) {
	listenerA := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener-a", Namespace: "ns", UID: "listener-a-uid"}}
	listenerB := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener-b", Namespace: "ns", UID: "listener-b-uid"}}
	podA := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "listener-a", Namespace: "ns"}}
	podB := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "listener-b", Namespace: "ns"}}

	var cache ResourceCache
	cache.Upsert(resourceCacheShardListenerPod, listenerA, podA)
	cache.Upsert(resourceCacheShardListenerConfigSecret, listenerA, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-a-config", Namespace: "ns"}})
	cache.Upsert(resourceCacheShardListenerPod, listenerB, podB)

	assert.Equal(t, 2, cache.DeleteByMainUID(listenerA.UID))
	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, 0, cache.DeleteByMainUID(listenerA.UID))
}

func TestResourceCacheSeparatesShards(t *testing.T) {
	mainObject := &v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "ns", UID: "listener-uid"}}
	listenerSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "ns", ResourceVersion: "1"}}
	proxySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener", Namespace: "ns", ResourceVersion: "2"}}

	var cache ResourceCache
	cache.Upsert(resourceCacheShardListenerConfigSecret, mainObject, listenerSecret)
	cache.Upsert(resourceCacheShardAutoscalingListenerProxySecret, mainObject, proxySecret)

	assert.Equal(t, 2, cache.Len())
	value, ok := cache.Value(resourceCacheShardListenerConfigSecret, mainObject, listenerSecret)
	require.True(t, ok)
	assert.Equal(t, "1", value.ResourceVersion)
	value, ok = cache.Value(resourceCacheShardAutoscalingListenerProxySecret, mainObject, proxySecret)
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

	b := ResourceBuilder{}
	listenerPod, err := b.newScaleSetListenerPod(listener, podConfig, serviceAccount, role, roleBinding, nil)
	require.NoError(t, err)

	value, ok := b.ResourceCache.Value(resourceCacheShardListenerPod, listener, listenerPod)
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
	var cache ResourceCache
	cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := cache.Get(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role); !ok {
			b.Fatal("expected cache hit")
		}
	}
}

func BenchmarkResourceCacheUpsertNoChange(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	var cache ResourceCache
	cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, replaced := cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role); replaced {
			b.Fatal("expected unchanged cache entry")
		}
	}
}

func BenchmarkResourceCacheUpsertReplace(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	var cache ResourceCache
	cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		desiredPod.ResourceVersion = strconv.Itoa(i)
		if _, replaced := cache.Upsert(resourceCacheShardListenerPod, mainObject, desiredPod, configSecret, serviceAccount, role); !replaced {
			b.Fatal("expected cache replacement")
		}
	}
}

func BenchmarkResourceCacheUpsertNoChangeParallel(b *testing.B) {
	cases := newResourceCacheParallelBenchmarkCases()

	b.Run("SameShard", func(b *testing.B) {
		var cache ResourceCache
		for _, tc := range cases {
			cache.Upsert(resourceCacheShardListenerPod, tc.mainObject, tc.desiredObject, tc.dependencies...)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				tc := cases[index.Add(1)%uint64(len(cases))]
				if _, replaced := cache.Upsert(resourceCacheShardListenerPod, tc.mainObject, tc.desiredObject, tc.dependencies...); replaced {
					b.Fatal("expected unchanged cache entry")
				}
			}
		})
	})

	b.Run("Sharded", func(b *testing.B) {
		var cache ResourceCache
		for _, tc := range cases {
			cache.Upsert(tc.shard, tc.mainObject, tc.desiredObject, tc.dependencies...)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				tc := cases[index.Add(1)%uint64(len(cases))]
				if _, replaced := cache.Upsert(tc.shard, tc.mainObject, tc.desiredObject, tc.dependencies...); replaced {
					b.Fatal("expected unchanged cache entry")
				}
			}
		})
	})
}

func BenchmarkResourceCacheUpsertReplaceParallel(b *testing.B) {
	cases := newResourceCacheParallelBenchmarkCases()

	b.Run("SameShard", func(b *testing.B) {
		var cache ResourceCache
		for _, tc := range cases {
			cache.Upsert(resourceCacheShardListenerPod, tc.mainObject, tc.desiredObject, tc.dependencies...)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				iteration := index.Add(1)
				tc := cases[iteration%uint64(len(cases))]
				desiredObject := tc.desiredObject.DeepCopyObject().(client.Object)
				desiredObject.SetResourceVersion(strconv.FormatUint(iteration, 10))
				if _, replaced := cache.Upsert(resourceCacheShardListenerPod, tc.mainObject, desiredObject, tc.dependencies...); !replaced {
					b.Fatal("expected cache replacement")
				}
			}
		})
	})

	b.Run("Sharded", func(b *testing.B) {
		var cache ResourceCache
		for _, tc := range cases {
			cache.Upsert(tc.shard, tc.mainObject, tc.desiredObject, tc.dependencies...)
		}

		var index atomic.Uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				iteration := index.Add(1)
				tc := cases[iteration%uint64(len(cases))]
				desiredObject := tc.desiredObject.DeepCopyObject().(client.Object)
				desiredObject.SetResourceVersion(strconv.FormatUint(iteration, 10))
				if _, replaced := cache.Upsert(tc.shard, tc.mainObject, desiredObject, tc.dependencies...); !replaced {
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

type resourceCacheBenchmarkCase struct {
	shard         resourceCacheShard
	mainObject    client.Object
	desiredObject client.Object
	dependencies  []client.Object
}

func newResourceCacheParallelBenchmarkCases() []resourceCacheBenchmarkCase {
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

	return []resourceCacheBenchmarkCase{
		{
			shard:         resourceCacheShardListenerPod,
			mainObject:    mainObject,
			desiredObject: desiredPod,
			dependencies:  []client.Object{configSecret, serviceAccount, role},
		},
		{
			shard:         resourceCacheShardListenerServiceAccount,
			mainObject:    listener,
			desiredObject: serviceAccountDesired,
		},
		{
			shard:         resourceCacheShardListenerRole,
			mainObject:    listenerRole,
			desiredObject: roleDesired,
		},
		{
			shard:         resourceCacheShardListenerConfigSecret,
			mainObject:    listenerConfig,
			desiredObject: configSecretDesired,
		},
	}
}
