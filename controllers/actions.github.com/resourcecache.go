package actionsgithubcom

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/hash"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var resourceCacheObjectTypes sync.Map

type ResourceCacheObjectRef struct {
	ObjectType      string
	Namespace       string
	Name            string
	UID             types.UID
	ResourceVersion string
}

type ResourceCacheKey struct {
	MainUID   types.UID
	Namespace string
	Name      string
}

type ResourceCacheValue[T client.Object] struct {
	MainObject      ResourceCacheObjectRef
	ResourceVersion string
	Dependencies    []ResourceCacheObjectRef
	Object          T
}

type ResourceCache struct {
	autoscalingListener            *resourceCacheState[*v1alpha1.AutoscalingListener]
	listenerConfigSecret           *resourceCacheState[*corev1.Secret]
	listenerPod                    *resourceCacheState[*corev1.Pod]
	listenerServiceAccount         *resourceCacheState[*corev1.ServiceAccount]
	listenerRole                   *resourceCacheState[*rbacv1.Role]
	listenerRoleBinding            *resourceCacheState[*rbacv1.RoleBinding]
	ephemeralRunnerSet             *resourceCacheState[*v1alpha1.EphemeralRunnerSet]
	autoscalingListenerProxySecret *resourceCacheState[*corev1.Secret]
	ephemeralRunner                *resourceCacheState[*v1alpha1.EphemeralRunner]
	ephemeralRunnerPod             *resourceCacheState[*corev1.Pod]
	ephemeralRunnerJitSecret       *resourceCacheState[*corev1.Secret]
	ephemeralRunnerSetProxySecret  *resourceCacheState[*corev1.Secret]
}

func NewResourceCache() ResourceCache {
	return ResourceCache{
		autoscalingListener:            newResourceCacheState[*v1alpha1.AutoscalingListener](),
		listenerConfigSecret:           newResourceCacheState[*corev1.Secret](),
		listenerPod:                    newResourceCacheState[*corev1.Pod](),
		listenerServiceAccount:         newResourceCacheState[*corev1.ServiceAccount](),
		listenerRole:                   newResourceCacheState[*rbacv1.Role](),
		listenerRoleBinding:            newResourceCacheState[*rbacv1.RoleBinding](),
		ephemeralRunnerSet:             newResourceCacheState[*v1alpha1.EphemeralRunnerSet](),
		autoscalingListenerProxySecret: newResourceCacheState[*corev1.Secret](),
		ephemeralRunner:                newResourceCacheState[*v1alpha1.EphemeralRunner](),
		ephemeralRunnerPod:             newResourceCacheState[*corev1.Pod](),
		ephemeralRunnerJitSecret:       newResourceCacheState[*corev1.Secret](),
		ephemeralRunnerSetProxySecret:  newResourceCacheState[*corev1.Secret](),
	}
}

type resourceCacheState[T client.Object] struct {
	mu      sync.RWMutex
	entries map[ResourceCacheKey]ResourceCacheValue[T]
}

func newResourceCacheState[T client.Object]() *resourceCacheState[T] {
	return &resourceCacheState[T]{
		entries: make(map[ResourceCacheKey]ResourceCacheValue[T], 512),
	}
}

func (s *resourceCacheState[T]) Get(
	mainObject client.Object,
	desiredObject T,
	dependencies ...client.Object,
) (T, bool) {
	key := newResourceCacheKey(mainObject, desiredObject)

	s.mu.RLock()
	value, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok || !value.Matches(mainObject, dependencies...) {
		var zero T
		return zero, false
	}

	return value.Object, true
}

func (s *resourceCacheState[T]) Upsert(
	mainObject client.Object,
	desiredObject T,
	dependencies ...client.Object,
) (ResourceCacheValue[T], bool) {
	key := newResourceCacheKey(mainObject, desiredObject)
	mainObjectRef := newResourceCacheObjectRef(mainObject)
	resourceVersion := desiredObject.GetResourceVersion()

	s.mu.RLock()
	previous, ok := s.entries[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependenciesMatch(dependencies...) {
		s.mu.RUnlock()
		return previous, false
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	previous, ok = s.entries[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependenciesMatch(dependencies...) {
		return previous, false
	}

	dependencyRefs := newResourceCacheObjectRefs(dependencies...)
	value := newResourceCacheValue(mainObjectRef, resourceVersion, dependencyRefs, desiredObject)
	s.entries[key] = value
	return value, true
}

func (c *ResourceCache) DeleteByMainUID(uid types.UID) int {
	var deleted int
	deleted += c.autoscalingListener.DeleteByMainUID(uid)
	deleted += c.listenerConfigSecret.DeleteByMainUID(uid)
	deleted += c.listenerPod.DeleteByMainUID(uid)
	deleted += c.listenerServiceAccount.DeleteByMainUID(uid)
	deleted += c.listenerRole.DeleteByMainUID(uid)
	deleted += c.listenerRoleBinding.DeleteByMainUID(uid)
	deleted += c.ephemeralRunnerSet.DeleteByMainUID(uid)
	deleted += c.autoscalingListenerProxySecret.DeleteByMainUID(uid)
	deleted += c.ephemeralRunner.DeleteByMainUID(uid)
	deleted += c.ephemeralRunnerPod.DeleteByMainUID(uid)
	deleted += c.ephemeralRunnerJitSecret.DeleteByMainUID(uid)
	deleted += c.ephemeralRunnerSetProxySecret.DeleteByMainUID(uid)
	return deleted
}

func (s *resourceCacheState[T]) DeleteByMainUID(uid types.UID) int {
	var deleted int
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if key.MainUID == uid {
			delete(s.entries, key)
			deleted++
		}
	}
	return deleted
}

func (s *resourceCacheState[T]) Value(mainObject client.Object, desiredObject T) (ResourceCacheValue[T], bool) {
	key := newResourceCacheKey(mainObject, desiredObject)

	s.mu.RLock()
	value, ok := s.entries[key]
	s.mu.RUnlock()
	return value, ok
}

func (v ResourceCacheValue[T]) Matches(mainObject client.Object, dependencies ...client.Object) bool {
	if v.MainObject != newResourceCacheObjectRef(mainObject) {
		return false
	}

	return v.dependenciesMatch(dependencies...)
}

func newResourceCacheKey(mainObject client.Object, desiredObject client.Object) ResourceCacheKey {
	return ResourceCacheKey{
		MainUID:   mainObject.GetUID(),
		Namespace: desiredObject.GetNamespace(),
		Name:      resourceCacheObjectName(desiredObject),
	}
}

func newResourceCacheValue[T client.Object](
	mainObjectRef ResourceCacheObjectRef,
	resourceVersion string,
	dependencyRefs []ResourceCacheObjectRef,
	object T,
) ResourceCacheValue[T] {
	return ResourceCacheValue[T]{
		MainObject:      mainObjectRef,
		ResourceVersion: resourceVersion,
		Dependencies:    dependencyRefs,
		Object:          cloneResourceCacheObject(object),
	}
}

func cloneResourceCacheObject[T client.Object](object T) T {
	return object.DeepCopyObject().(T)
}

func newResourceCacheObjectRefs(objects ...client.Object) []ResourceCacheObjectRef {
	refs := make([]ResourceCacheObjectRef, 0, len(objects))
	for _, object := range objects {
		refs = append(refs, newResourceCacheObjectRef(object))
	}
	slices.SortFunc(refs, func(a, b ResourceCacheObjectRef) int {
		return compareResourceCacheObjectRefs(a, b)
	})
	return refs
}

func (v ResourceCacheValue[T]) dependenciesMatch(objects ...client.Object) bool {
	if len(v.Dependencies) != len(objects) {
		return false
	}

	for _, object := range objects {
		ref := newResourceCacheObjectRef(object)
		if !slices.Contains(v.Dependencies, ref) {
			return false
		}
	}

	return true
}

func newResourceCacheObjectRef(object client.Object) ResourceCacheObjectRef {
	resourceVersion := object.GetResourceVersion()
	if resourceVersion == "" {
		resourceVersion = hash.ComputeTemplateHashJSON(object)
	}

	return ResourceCacheObjectRef{
		ObjectType:      resourceCacheObjectType(object),
		Namespace:       object.GetNamespace(),
		Name:            resourceCacheObjectName(object),
		UID:             object.GetUID(),
		ResourceVersion: resourceVersion,
	}
}

func compareResourceCacheObjectRefs(a, b ResourceCacheObjectRef) int {
	if c := strings.Compare(a.ObjectType, b.ObjectType); c != 0 {
		return c
	}
	if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
		return c
	}
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.UID), string(b.UID)); c != 0 {
		return c
	}
	return strings.Compare(a.ResourceVersion, b.ResourceVersion)
}

func resourceCacheObjectType(object client.Object) string {
	t := reflect.TypeOf(object)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if objectType, ok := resourceCacheObjectTypes.Load(t); ok {
		return objectType.(string)
	}

	objectType := t.PkgPath() + "." + t.Name()
	actual, _ := resourceCacheObjectTypes.LoadOrStore(t, objectType)
	return actual.(string)
}

func resourceCacheObjectName(object client.Object) string {
	if object.GetName() != "" {
		return object.GetName()
	}
	return object.GetGenerateName()
}
