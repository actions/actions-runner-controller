package hash

import (
	"fmt"
	"hash/fnv"
	"k8s.io/apimachinery/pkg/util/rand"
)

func FNVHashStringObjects(objs ...interface{}) string {
	hash := fnv.New32a()

	for _, obj := range objs {
		DeepHashObject(hash, obj)
	}

	return rand.SafeEncodeString(fmt.Sprint(hash.Sum32()))
}
