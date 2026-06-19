// Copyright 2015 The Kubernetes Authors.
// hash.go is copied from kubernetes's pkg/util/hash.go
// See https://github.com/kubernetes/kubernetes/blob/e1c617a88ec286f5f6cb2589d6ac562d095e1068/pkg/util/hash/hash.go#L25-L37

package hash

import (
	"encoding/json"
	"hash"
	"hash/fnv"
	"strconv"

	"github.com/davecgh/go-spew/spew"
	"k8s.io/apimachinery/pkg/util/rand"
)

var deepObjectPrinter = spew.ConfigState{
	Indent:         " ",
	SortKeys:       true,
	DisableMethods: true,
	SpewKeys:       true,
}

// DeepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func DeepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	deepObjectPrinter.Fprintf(hasher, "%#v", objectToWrite)
}

// ComputeHash returns a hash value calculated from template and
// a collisionCount to avoid hash collision. The hash will be safe encoded to
// avoid bad words. It expects **template. In other words, you should pass an address
// of a DeepCopy result.
//
// Proudly modified and adopted from k8s.io/kubernetes/pkg/util/hash.DeepHashObject and
// k8s.io/kubernetes/pkg/controller.ComputeHash.
func ComputeTemplateHash(template interface{}) string {
	hasher := fnv.New32a()
	DeepHashObject(hasher, template)

	return rand.SafeEncodeString(strconv.FormatUint(uint64(hasher.Sum32()), 10))
}

// ComputeTemplateHashJSON computes a stable hash using JSON marshaling.
// It is faster than spew-based hashing for large nested specs with map/slice fields.
func ComputeTemplateHashJSON(template interface{}) string {
	bytes, err := json.Marshal(template)
	if err != nil {
		return ComputeTemplateHash(template)
	}

	hasher := fnv.New32a()
	_, _ = hasher.Write(bytes)

	return rand.SafeEncodeString(strconv.FormatUint(uint64(hasher.Sum32()), 10))
}
