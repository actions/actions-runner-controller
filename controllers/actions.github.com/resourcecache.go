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
	MainUID    types.UID
	ObjectType string
	Namespace  string
	Name       string
}

type ResourceCacheValue struct {
	MainObject      ResourceCacheObjectRef
	ResourceVersion string
	Dependencies    []ResourceCacheObjectRef
	Object          client.Object
}

type ResourceCache map[ResourceCacheKey]ResourceCacheValue

func (b *ResourceBuilder) cacheDesiredResource(mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (ResourceCacheValue, bool) {
	return b.ResourceCache.Upsert(mainObject, desiredObject, dependencies...)
}

func (b *ResourceBuilder) cachedDesiredResource(mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (client.Object, bool) {
	return b.ResourceCache.Get(mainObject, desiredObject, dependencies...)
}

func (c ResourceCache) Get(mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (client.Object, bool) {
	value, ok := c[newResourceCacheKey(mainObject, desiredObject)]
	if !ok || !value.Matches(mainObject, dependencies...) {
		return nil, false
	}

	return value.Object.DeepCopyObject().(client.Object), true
}

func (c *ResourceCache) Upsert(mainObject client.Object, desiredObject client.Object, dependencies ...client.Object) (ResourceCacheValue, bool) {
	if *c == nil {
		*c = make(ResourceCache)
	}

	key := newResourceCacheKey(mainObject, desiredObject)
	mainObjectRef := newResourceCacheObjectRef(mainObject)
	resourceVersion := desiredObject.GetResourceVersion()

	previous, ok := (*c)[key]
	if ok && previous.MainObject == mainObjectRef && previous.ResourceVersion == resourceVersion && previous.dependenciesMatch(dependencies...) {
		return previous, false
	}

	dependencyRefs := newResourceCacheObjectRefs(dependencies...)
	value := newResourceCacheValue(mainObjectRef, resourceVersion, dependencyRefs, desiredObject)
	(*c)[key] = value
	return value, true
}

func (c ResourceCache) DeleteByMainUID(uid types.UID) int {
	var deleted int
	for key := range c {
		if key.MainUID == uid {
			delete(c, key)
			deleted++
		}
	}
	return deleted
}

func (v ResourceCacheValue) Matches(mainObject client.Object, dependencies ...client.Object) bool {
	if v.MainObject != newResourceCacheObjectRef(mainObject) {
		return false
	}

	return v.dependenciesMatch(dependencies...)
}

func newResourceCacheKey(mainObject client.Object, desiredObject client.Object) ResourceCacheKey {
	return ResourceCacheKey{
		MainUID:    mainObject.GetUID(),
		ObjectType: resourceCacheObjectType(desiredObject),
		Namespace:  desiredObject.GetNamespace(),
		Name:       resourceCacheObjectName(desiredObject),
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
