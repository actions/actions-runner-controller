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

	t.Run("suppresses when the API server records the spec patch ID", func(t *testing.T) {
		r := &EphemeralRunnerSetReconciler{
			Client:    newReader(newSet(2, 2)),
			APIReader: newReader(newSet(2, 2)),
		}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, newSet(2, 2))
		require.NoError(t, err)
		assert.True(t, suppressed, "the cleanup for this patch ID is recorded, so the shortfall is not new demand")
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
		// The cleanup reconcile wrote the marker and returned, and the reconcile
		// triggered by its own deletions is still reading a pre-write cache.
		// Client stands in for that lagging cache, APIReader for the API server
		// that already has the write.
		stale := newSet(2, 0)
		r := &EphemeralRunnerSetReconciler{
			Client:    newReader(newSet(2, 0)),
			APIReader: newReader(newSet(2, 2)),
		}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, stale)
		require.NoError(t, err)
		assert.True(t, suppressed, "the decision must come from the API server, not from a cache that has not caught up")
	})

	t.Run("does not suppress when the cache still shows a marker the API server has cleared", func(t *testing.T) {
		// The mirror image of the case above, and the one that opens up once
		// applying a new revision clears the marker. A cached hit is no longer
		// self-evidently safe: here the cache still carries the marker from
		// before the spec change while the API server has already cleared it, and
		// trusting the cache would suppress the scale up that rebuilds the pool.
		stale := newSet(2, 2)
		r := &EphemeralRunnerSetReconciler{
			Client:    newReader(newSet(2, 2)),
			APIReader: newReader(newSet(2, 0)),
		}

		suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(context.Background(), key, stale)
		require.NoError(t, err)
		assert.False(t, suppressed, "a cleared marker on the API server must win over a stale cached one")
	})

	t.Run("never suppresses when the listener published patch ID zero", func(t *testing.T) {
		// Patch ID 0 is the collapsed state the listener republishes on every
		// long-poll timeout once the set is idle at its minimum. Suppressing on
		// it would let a scale set sit below its minimum indefinitely.
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

// TestPatchAppliedActionableRevisionStatusClearsFinishedRunnerCleanupPatchID
// covers the marker's lifetime across a spec change.
//
// The marker is a patch ID, and patch IDs are only meaningful within one
// listener incarnation. A spec change restarts the listener, which numbers its
// patches from 0 upwards and so passes through any leftover value. Carrying the
// marker across the revision boundary therefore suppresses the scale up that is
// supposed to rebuild the pool the revision cleanup just deleted.
func TestPatchAppliedActionableRevisionStatusClearsFinishedRunnerCleanupPatchID(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	key := types.NamespacedName{Namespace: "test-ns", Name: "test-ers"}

	newSet := func(appliedRevision int64, cleanupPatchID int) *v1alpha1.EphemeralRunnerSet {
		return &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
			Status: v1alpha1.EphemeralRunnerSetStatus{
				AppliedActionableRevision:    appliedRevision,
				FinishedRunnerCleanupPatchID: cleanupPatchID,
			},
		}
	}

	newClient := func(object client.Object) client.Client {
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(object).
			WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
			// patchAppliedActionableRevisionStatus lists the child runners to
			// recompute the phase, so the fake client needs the same index
			// SetupIndexers registers on the manager.
			WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
			Build()
	}

	t.Run("clears the marker when the applied revision advances", func(t *testing.T) {
		c := newClient(newSet(3, 7))
		r := &EphemeralRunnerSetReconciler{Client: c, APIReader: c}

		require.NoError(t, r.patchAppliedActionableRevisionStatus(context.Background(), key, 4))

		var got v1alpha1.EphemeralRunnerSet
		require.NoError(t, c.Get(context.Background(), key, &got))
		assert.EqualValues(t, 4, got.Status.AppliedActionableRevision, "the applied revision should advance")
		assert.Zero(t, got.Status.FinishedRunnerCleanupPatchID, "a marker from the previous patch sequence must not survive the spec change")
	})

	t.Run("decides against authoritative state rather than the cache", func(t *testing.T) {
		// The helper both decides and diffs against the object it reads, so that
		// read has to bypass the cache. Here the cache has already caught up to
		// revision 4 while the API server has not, which is the shape a cache
		// takes when it has observed a write the reconcile is about to redo: a
		// cached read would conclude there is nothing to do and leave the stale
		// marker in place.
		authoritative := newClient(newSet(3, 7))
		lagging := newClient(newSet(4, 7))
		r := &EphemeralRunnerSetReconciler{Client: lagging, APIReader: authoritative}

		require.NoError(t, r.patchAppliedActionableRevisionStatus(context.Background(), key, 4))

		var got v1alpha1.EphemeralRunnerSet
		require.NoError(t, lagging.Get(context.Background(), key, &got))
		assert.Zero(t, got.Status.FinishedRunnerCleanupPatchID, "the clear must be computed against authoritative state")
	})

	t.Run("leaves the marker alone when the revision has already been applied", func(t *testing.T) {
		// Nothing was cleaned up here, so there is no reason to disturb a marker
		// that is still describing the current patch sequence.
		c := newClient(newSet(4, 7))
		r := &EphemeralRunnerSetReconciler{Client: c, APIReader: c}

		require.NoError(t, r.patchAppliedActionableRevisionStatus(context.Background(), key, 4))

		var got v1alpha1.EphemeralRunnerSet
		require.NoError(t, c.Get(context.Background(), key, &got))
		assert.EqualValues(t, 7, got.Status.FinishedRunnerCleanupPatchID, "an unchanged revision must not clear the marker")
	})
}
