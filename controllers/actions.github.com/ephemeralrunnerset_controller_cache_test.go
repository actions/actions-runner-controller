package actionsgithubcom

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

func TestEphemeralRunnerSetSpecHashCache_HitRequiresMatchingUID(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}

	reconciler.setSpecHashCache(name, types.UID("uid-1"), 7, "hash-1")

	if !reconciler.hasSpecHashCache(name, types.UID("uid-1"), 7, "hash-1") {
		t.Fatalf("expected cache hit for matching uid, generation, and hash")
	}

	if reconciler.hasSpecHashCache(name, types.UID("uid-2"), 7, "hash-1") {
		t.Fatalf("expected cache miss when uid changes")
	}
}

func TestEphemeralRunnerSetSpecHashCache_ClearRemovesEntry(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}

	reconciler.setSpecHashCache(name, types.UID("uid-1"), 3, "hash-3")
	if !reconciler.hasSpecHashCache(name, types.UID("uid-1"), 3, "hash-3") {
		t.Fatalf("expected cache hit before clear")
	}

	reconciler.clearSpecHashCache(name)
	if reconciler.hasSpecHashCache(name, types.UID("uid-1"), 3, "hash-3") {
		t.Fatalf("expected cache miss after clear")
	}
}
