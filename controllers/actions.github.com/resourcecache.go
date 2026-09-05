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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	resourceCacheInitialEntries        = 4096
	resourceCacheInitialMainUIDEntries = 4096
	resourceCacheInitialOwnerEntries   = 8
	resourceCacheMaxDependencyRefs     = 4
)

type ResourceCacheObjectRef struct {
	ObjectType      schema.GroupVersionKind
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
	dependencyKey   resourceCacheDependencyKey
	Object          T
}

type resourceCacheDependencyKey struct {
	count int
	refs  [resourceCacheMaxDependencyRefs]ResourceCacheObjectRef
}

type ResourceCache struct {
	autoscalingListener    *resourceCacheState[*v1alpha1.AutoscalingListener]
	ephemeralRunnerSet     *resourceCacheState[*v1alpha1.EphemeralRunnerSet]
	listenerPod            *resourceCacheState[*corev1.Pod]
	listenerServiceAccount *resourceCacheState[*corev1.ServiceAccount]
	listenerRole           *resourceCacheState[*rbacv1.Role]
	listenerRoleBinding    *resourceCacheState[*rbacv1.RoleBinding]
}

func NewResourceCache() ResourceCache {
	return ResourceCache{
		autoscalingListener:    newResourceCacheState[*v1alpha1.AutoscalingListener](),
		ephemeralRunnerSet:     newResourceCacheState[*v1alpha1.EphemeralRunnerSet](),
		listenerPod:            newResourceCacheState[*corev1.Pod](),
		listenerServiceAccount: newResourceCacheState[*corev1.ServiceAccount](),
		listenerRole:           newResourceCacheState[*rbacv1.Role](),
		listenerRoleBinding:    newResourceCacheState[*rbacv1.RoleBinding](),
	}
}

type resourceCacheState[T client.Object] struct {
	mu               sync.RWMutex
	entries          map[ResourceCacheKey]ResourceCacheValue[T]
	entriesByMainUID map[types.UID]map[ResourceCacheKey]struct{}
}

func newResourceCacheState[T client.Object]() *resourceCacheState[T] {
	return &resourceCacheState[T]{
		entries:          make(map[ResourceCacheKey]ResourceCacheValue[T], resourceCacheInitialEntries),
		entriesByMainUID: make(map[types.UID]map[ResourceCacheKey]struct{}, resourceCacheInitialMainUIDEntries),
	}
}

func (s *resourceCacheState[T]) Get(
	mainObject client.Object,
	desiredObject T,
	dependencies ...client.Object,
) (T, bool) {
	var zero T
	if s == nil || isNilResourceCacheObject(mainObject) || isNilResourceCacheObject(desiredObject) {
		return zero, false
	}
	dependencyKey, ok := newResourceCacheDependencyKey(dependencies...)
	if !ok {
		return zero, false
	}
	if mainObject.GetUID() == "" {
		return zero, false
	}

	key := newResourceCacheKey(mainObject, desiredObject)
	mainObjectRef := newResourceCacheObjectRef(mainObject)

	s.mu.RLock()
	value, ok := s.entries[key]
	if ok && value.MainObject == mainObjectRef && value.dependencyKey.Equal(dependencyKey) {
		s.mu.RUnlock()
		return value.Object, true
	}
	s.mu.RUnlock()

	return zero, false
}

func (s *resourceCacheState[T]) Upsert(
	mainObject client.Object,
	desiredObject T,
	dependencies ...client.Object,
) (ResourceCacheValue[T], bool) {
	var zero ResourceCacheValue[T]
	if s == nil || isNilResourceCacheObject(mainObject) || isNilResourceCacheObject(desiredObject) {
		return zero, false
	}
	dependencyKey, ok := newResourceCacheDependencyKey(dependencies...)
	if !ok {
		return zero, false
	}
	if mainObject.GetUID() == "" {
		return zero, false
	}

	key := newResourceCacheKey(mainObject, desiredObject)
	mainObjectRef := newResourceCacheObjectRef(mainObject)
	resourceVersion := desiredObject.GetResourceVersion()

	s.mu.RLock()
	previous, ok := s.entries[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependencyKey.Equal(dependencyKey) {
		s.mu.RUnlock()
		return previous, false
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	previous, ok = s.entries[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependencyKey.Equal(dependencyKey) {
		return previous, false
	}

	value := ResourceCacheValue[T]{
		MainObject:      mainObjectRef,
		ResourceVersion: resourceVersion,
		dependencyKey:   dependencyKey,
		Object:          desiredObject,
	}
	s.entries[key] = value
	s.indexKeyLocked(key)
	return value, true
}

func (c *ResourceCache) Delete(mainObject client.Object) {
	if mainObject == nil {
		return
	}

	c.autoscalingListener.Delete(mainObject)
	c.ephemeralRunnerSet.Delete(mainObject)
	c.listenerPod.Delete(mainObject)
	c.listenerServiceAccount.Delete(mainObject)
	c.listenerRole.Delete(mainObject)
	c.listenerRoleBinding.Delete(mainObject)
}

func (s *resourceCacheState[T]) Delete(mainObject client.Object) {
	if s == nil || mainObject == nil {
		return
	}

	uid := mainObject.GetUID()
	if uid == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range s.entriesByMainUID[uid] {
		delete(s.entries, key)
	}
	delete(s.entriesByMainUID, uid)
}

func (s *resourceCacheState[T]) indexKeyLocked(key ResourceCacheKey) {
	keys, ok := s.entriesByMainUID[key.MainUID]
	if !ok {
		keys = make(map[ResourceCacheKey]struct{}, resourceCacheInitialOwnerEntries)
		s.entriesByMainUID[key.MainUID] = keys
	}
	keys[key] = struct{}{}
}

func newResourceCacheKey(mainObject client.Object, desiredObject client.Object) ResourceCacheKey {
	return ResourceCacheKey{
		MainUID:   mainObject.GetUID(),
		Namespace: desiredObject.GetNamespace(),
		Name:      resourceCacheObjectName(desiredObject),
	}
}

func newResourceCacheDependencyKey(objects ...client.Object) (resourceCacheDependencyKey, bool) {
	if len(objects) > resourceCacheMaxDependencyRefs {
		return resourceCacheDependencyKey{}, false
	}

	key := resourceCacheDependencyKey{count: len(objects)}
	for i, object := range objects {
		if isNilResourceCacheObject(object) {
			return resourceCacheDependencyKey{}, false
		}
		key.refs[i] = newResourceCacheObjectRef(object)
	}
	slices.SortFunc(key.refs[:key.count], func(a, b ResourceCacheObjectRef) int {
		return compareResourceCacheObjectRefs(a, b)
	})
	return key, true
}

func (k resourceCacheDependencyKey) Equal(other resourceCacheDependencyKey) bool {
	if k.count != other.count {
		return false
	}
	if k.count > len(k.refs) || other.count > len(other.refs) {
		return false
	}

	for i := 0; i < k.count; i++ {
		if k.refs[i] != other.refs[i] {
			return false
		}
	}

	return true
}

func newResourceCacheObjectRef(object client.Object) ResourceCacheObjectRef {
	resourceVersion := object.GetResourceVersion()
	if resourceVersion == "" {
		resourceVersion = hash.ComputeTemplateHash(object)
	}

	return ResourceCacheObjectRef{
		ObjectType:      object.GetObjectKind().GroupVersionKind(),
		Namespace:       object.GetNamespace(),
		Name:            resourceCacheObjectName(object),
		UID:             object.GetUID(),
		ResourceVersion: resourceVersion,
	}
}

func compareResourceCacheObjectRefs(a, b ResourceCacheObjectRef) int {
	if c := compareGroupVersionKinds(a.ObjectType, b.ObjectType); c != 0 {
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

func compareGroupVersionKinds(a, b schema.GroupVersionKind) int {
	if c := strings.Compare(a.Group, b.Group); c != 0 {
		return c
	}
	if c := strings.Compare(a.Version, b.Version); c != 0 {
		return c
	}
	return strings.Compare(a.Kind, b.Kind)
}

func resourceCacheObjectName(object client.Object) string {
	if object.GetName() != "" {
		return object.GetName()
	}
	return object.GetGenerateName()
}

func isNilResourceCacheObject[T client.Object](object T) bool {
	var clientObject client.Object = object
	if clientObject == nil {
		return true
	}

	value := reflect.ValueOf(clientObject)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
