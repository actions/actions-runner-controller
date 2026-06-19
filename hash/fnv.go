package hash

import (
	"hash/fnv"
	"strconv"

	"k8s.io/apimachinery/pkg/util/rand"
)

func FNVHashStringObjects(objs ...interface{}) string {
	hash := fnv.New32a()

	for _, obj := range objs {
		DeepHashObject(hash, obj)
	}

	return rand.SafeEncodeString(strconv.FormatUint(uint64(hash.Sum32()), 10))
}

func FNVHashString(name string) string {
	hash := fnv.New32a()
	hash.Write([]byte(name))
	return rand.SafeEncodeString(strconv.FormatUint(uint64(hash.Sum32()), 10))
}
