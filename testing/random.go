package testing

import (
	"math/rand"
	"time"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyz"

var (
	random = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// Copied from https://stackoverflow.com/a/31832326 with thanks
func RandStringBytesRmndr(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[random.Int63()%int64(len(letterBytes))]
	}
	return string(b)
}
