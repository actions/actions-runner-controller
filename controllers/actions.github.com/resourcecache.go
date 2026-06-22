package actionsgithubcom

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/actions/actions-runner-controller/hash"
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

type ResourceCacheValue struct {
	MainObject      ResourceCacheObjectRef
	ResourceVersion string
	Dependencies    []ResourceCacheObjectRef
	Object          client.Object
}

type resourceCacheShard uint8

const (
	resourceCacheShardAutoscalingListener resourceCacheShard = iota
	resourceCacheShardListenerConfigSecret
	resourceCacheShardListenerPod
	resourceCacheShardListenerServiceAccount
	resourceCacheShardListenerRole
	resourceCacheShardListenerRoleBinding
	resourceCacheShardEphemeralRunnerSet
	resourceCacheShardAutoscalingListenerProxySecret
	resourceCacheShardEphemeralRunner
	resourceCacheShardEphemeralRunnerPod
	resourceCacheShardEphemeralRunnerJitSecret
	resourceCacheShardEphemeralRunnerSetProxySecret
	resourceCacheShardCount
)

type ResourceCache struct {
	shards [resourceCacheShardCount]resourceCacheShardState
}

type resourceCacheShardState struct {
	once    sync.Once
	mu      sync.RWMutex
	entries map[ResourceCacheKey]ResourceCacheValue
}

func (b *ResourceBuilder) cacheDesiredResource(shard resourceCacheShard, mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (ResourceCacheValue, bool) {
	return b.ResourceCache.Upsert(shard, mainObject, desiredObject, dependencies...)
}

func (b *ResourceBuilder) cachedDesiredResource(shard resourceCacheShard, mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (client.Object, bool) {
	return b.ResourceCache.Get(shard, mainObject, desiredObject, dependencies...)
}

func (c *ResourceCache) Get(shard resourceCacheShard, mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (client.Object, bool) {
	state := c.shard(shard)
	key := newResourceCacheKey(mainObject, desiredObject)

	state.mu.RLock()
	value, ok := state.entries[key]
	state.mu.RUnlock()
	if !ok || !value.Matches(mainObject, dependencies...) {
		return nil, false
	}

	return value.Object.DeepCopyObject().(client.Object), true
}

func (c *ResourceCache) Upsert(shard resourceCacheShard, mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (ResourceCacheValue, bool) {
	state := c.shard(shard)
	key := newResourceCacheKey(mainObject, desiredObject)
	mainObjectRef := newResourceCacheObjectRef(mainObject)
	resourceVersion := desiredObject.GetResourceVersion()

	state.mu.RLock()
	previous, ok := state.entries[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependenciesMatch(dependencies...) {
		state.mu.RUnlock()
		return previous, false
	}
	state.mu.RUnlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	previous, ok = state.entries[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependenciesMatch(dependencies...) {
		return previous, false
	}

	dependencyRefs := newResourceCacheObjectRefs(dependencies...)
	value := newResourceCacheValue(mainObjectRef, resourceVersion, dependencyRefs, desiredObject)
	state.entries[key] = value
	return value, true
}

func (c *ResourceCache) DeleteByMainUID(uid types.UID) int {
	var deleted int
	for shard := range c.shards {
		state := &c.shards[shard]
		state.mu.Lock()
		for key := range state.entries {
			if key.MainUID == uid {
				delete(state.entries, key)
				deleted++
			}
		}
		state.mu.Unlock()
	}
	return deleted
}

func (c *ResourceCache) Len() int {
	var count int
	for shard := range c.shards {
		state := &c.shards[shard]
		state.mu.RLock()
		count += len(state.entries)
		state.mu.RUnlock()
	}
	return count
}

func (c *ResourceCache) Value(shard resourceCacheShard, mainObject client.Object, desiredObject client.Object) (ResourceCacheValue, bool) {
	state := c.shard(shard)
	key := newResourceCacheKey(mainObject, desiredObject)

	state.mu.RLock()
	value, ok := state.entries[key]
	state.mu.RUnlock()
	return value, ok
}

func (c *ResourceCache) shard(shard resourceCacheShard) *resourceCacheShardState {
	state := &c.shards[shard]
	state.once.Do(func() {
		state.entries = make(map[ResourceCacheKey]ResourceCacheValue)
	})
	return state
}

func (v ResourceCacheValue) Matches(mainObject client.Object, dependencies ...client.Object) bool {
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

func newResourceCacheValue(mainObjectRef ResourceCacheObjectRef, resourceVersion string, dependencyRefs []ResourceCacheObjectRef, object client.Object) ResourceCacheValue {
	return ResourceCacheValue{
		MainObject:      mainObjectRef,
		ResourceVersion: resourceVersion,
		Dependencies:    dependencyRefs,
		Object:          object.DeepCopyObject().(client.Object),
	}
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

func (v ResourceCacheValue) dependenciesMatch(objects ...client.Object) bool {
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
