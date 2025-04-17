// Copyright 2015 The Kubernetes Authors.
// hash.go is copied from kubernetes's pkg/util/hash.go
// See https://github.com/kubernetes/kubernetes/blob/e1c617a88ec286f5f6cb2589d6ac562d095e1068/pkg/util/hash/hash.go#L25-L37

package hash

import (
	"fmt"
	"hash"
	"hash/fnv"

	"github.com/davecgh/go-spew/spew"
	"k8s.io/apimachinery/pkg/util/rand"
)

// DeepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func DeepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", objectToWrite)
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

	hasher.Reset()

	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", template)

	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}

func ComputeCombinedObjectsHash(first any, others ...any) string {
	hasher := fnv.New32a()

	hasher.Reset()

	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}

	for _, obj := range append([]any{first}, others...) {
		printer.Fprintf(hasher, "%#v", obj)
	}

	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}
