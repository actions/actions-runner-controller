// Copyright 2015 The Kubernetes Authors.
// hash.go is copied from kubernetes's pkg/util/hash.go
// See https://github.com/kubernetes/kubernetes/blob/e1c617a88ec286f5f6cb2589d6ac562d095e1068/pkg/util/hash/hash.go#L25-L37

package hash

import (
	"hash"

	"github.com/davecgh/go-spew/spew"
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
