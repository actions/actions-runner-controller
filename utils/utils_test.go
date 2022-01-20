package utils

import (
	"testing"
)

func TestContainsString(t *testing.T) {
	values := []string{"foo", "bar"}
	t.Run("Contains", func(t *testing.T) {
		if !ContainsString(values, "foo") {
			t.Errorf("ContainsString() is false, want true")
		}
	})
	t.Run("NotContains", func(t *testing.T) {
		if ContainsString(values, "missing") {
			t.Errorf("ContainsString() is true, want false")
		}
	})
}
