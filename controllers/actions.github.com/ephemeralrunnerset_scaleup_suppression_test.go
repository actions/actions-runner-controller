package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestScaleUpServicedByFinishedRunnerCleanup pins down the decision that
// suppresses scale up after finished runners were cleaned up.
//
// The interesting case is the third one. The marker is written by one reconcile
// and read back by the next, and the deletions performed by the first reconcile
// are themselves what triggers the second. That next reconcile is regularly
// served from an informer cache that has not yet observed the controller's own
// status write, so the decision must not be made from the cached copy alone. An
// envtest spec only hits that window under load, which is why this is asserted
// directly instead.
func TestScaleUpServicedByFinishedRunnerCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	key := types.NamespacedName{Namespace: "test-ns", Name: "test-ers"}

	newSet := func(specPatchID, statusCleanupPatchID int) *v1alpha1.EphemeralRunnerSet {
		return &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 2,
				PatchID:  specPatchID,
			},
			Status: v1alpha1.EphemeralRunnerSetStatus{
				FinishedRunnerCleanupPatchID: statusCleanupPatchID,
			},
		}
	}

	newReader := func(objects ...client.Object) client.Client {
		return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	}

	t.Run("suppresses when the cached marker already matches the spec patch ID", func(t *testing.T) {
		// A cached hit needs no confirmation, so the reconciler is deliberately
		// left without an APIReader: reaching for one here would be a bug.
		r := &EphemeralRunnerSetReconciler{}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, newSet(2, 2))
		require.NoError(t, err)
		assert.True(t, suppressed, "the cleanup for this patch ID is already recorded, so the shortfall is not new demand")
	})

	t.Run("does not suppress when neither the cache nor the API server records the patch ID", func(t *testing.T) {
		r := &EphemeralRunnerSetReconciler{
			Client:    newReader(newSet(2, 0)),
			APIReader: newReader(newSet(2, 0)),
		}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, newSet(2, 0))
		require.NoError(t, err)
		assert.False(t, suppressed, "no cleanup was performed for this patch ID, so the shortfall is genuine demand")
	})

	t.Run("suppresses when the cache is stale but the API server records the patch ID", func(t *testing.T) {
		// This is the race: the cleanup reconcile wrote the marker and returned,
		// and the reconcile triggered by its own deletions is still reading a
		// pre-write cache. Client stands in for that lagging cache, APIReader for
		// the API server that already has the write.
		stale := newSet(2, 0)
		r := &EphemeralRunnerSetReconciler{
			Client:    newReader(newSet(2, 0)),
			APIReader: newReader(newSet(2, 2)),
		}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, stale)
		require.NoError(t, err)
		assert.True(t, suppressed, "the decision must come from the API server, not from a cache that has not caught up")
	})

	t.Run("never suppresses when the listener published patch ID zero", func(t *testing.T) {
		// Patch ID 0 is the idle-at-minimum state the listener republishes
		// without incrementing. Suppressing on it would let a scale set sit
		// below its minimum indefinitely.
		r := &EphemeralRunnerSetReconciler{
			Client:    newReader(newSet(0, 0)),
			APIReader: newReader(newSet(0, 0)),
		}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, newSet(0, 0))
		require.NoError(t, err)
		assert.False(t, suppressed, "patch ID 0 must always be free to scale up")
	})

	t.Run("fails loudly when it cannot read past the cache", func(t *testing.T) {
		r := &EphemeralRunnerSetReconciler{Client: newReader(newSet(2, 2))}

		_, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, newSet(2, 0))
		assert.Error(t, err, "a missing APIReader must surface rather than silently fall back to the cached marker")
	})
}
