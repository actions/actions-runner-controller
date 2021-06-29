package testing

import (
	"os"
	"testing"
)

func Getenv(t *testing.T, name string) string {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		t.Fatal(name + " must be set")
	}
	return v
}
