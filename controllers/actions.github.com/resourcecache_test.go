package actionsgithubcom

import (
	"strconv"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	value, replaced := cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, replaced)
	assert.Len(t, cache, 1)
	assert.Contains(t, cache, newResourceCacheKey(mainObject, desiredPod))
	assert.Equal(t, "1", value.ResourceVersion)

	_, replaced = cache.Upsert(mainObject, desiredPod, role, configSecret, serviceAccount)
	assert.False(t, replaced, "dependency ordering should not affect the cache value")
	assert.Len(t, cache, 1)

	configSecret.ResourceVersion = "2"
	value, replaced = cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)
	assert.True(t, replaced)
	assert.Len(t, cache, 1)
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

	cache := ResourceCache{}
	cache.Upsert(listenerA, podA)
	cache.Upsert(listenerA, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "listener-a-config", Namespace: "ns"}})
	cache.Upsert(listenerB, podB)

	assert.Equal(t, 2, cache.DeleteByMainUID(listenerA.UID))
	assert.Len(t, cache, 1)
	assert.Equal(t, 0, cache.DeleteByMainUID(listenerA.UID))
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

	value, ok := b.ResourceCache[newResourceCacheKey(listener, listenerPod)]
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
	cache := ResourceCache{}
	cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := cache.Get(mainObject, desiredPod, configSecret, serviceAccount, role); !ok {
			b.Fatal("expected cache hit")
		}
	}
}

func BenchmarkResourceCacheUpsertNoChange(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	cache := ResourceCache{}
	cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, replaced := cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role); replaced {
			b.Fatal("expected unchanged cache entry")
		}
	}
}

func BenchmarkResourceCacheUpsertReplace(b *testing.B) {
	mainObject, desiredPod, configSecret, serviceAccount, role := newResourceCacheBenchmarkObjects()
	cache := ResourceCache{}
	cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		desiredPod.ResourceVersion = strconv.Itoa(i)
		if _, replaced := cache.Upsert(mainObject, desiredPod, configSecret, serviceAccount, role); !replaced {
			b.Fatal("expected cache replacement")
		}
	}
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
