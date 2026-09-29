package actionsgithubcom

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestPatchFinishedRunnerCleanupPatchIDStatusUsesOptimisticLock pins the
// resourceVersion precondition on the cleanup marker patch.
//
// The marker decides whether a shortfall below Spec.Replicas is suppressed, so a
// write that lands on the wrong patch ID re-enables the spurious scale up this
// layer exists to prevent. The helper sets the marker to whatever patch ID the
// reconcile is carrying rather than only ever advancing it, so without the lock
// the API server cannot reject a stale write, retry.RetryOnConflict never fires,
// and a reconcile serving an older patch ID can overwrite a marker recorded for
// a newer one.
//
// Asserted on the bytes the production code emits rather than on an
// independently built patch, so it cannot pass while the reconciler constructs
// its patch some other way. Reverting the option to a plain client.MergeFrom
// must fail this test.
func TestPatchFinishedRunnerCleanupPatchIDStatusUsesOptimisticLock(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			PatchID: 4,
		},
	}

	var capturedPatch []byte
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ephemeralRunnerSet).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, clt client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				data, err := patch.Data(obj)
				if err != nil {
					return err
				}
				capturedPatch = data
				return clt.Status().Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	reconciler := &EphemeralRunnerSetReconciler{
		Client:    c,
		APIReader: c,
		Log:       logr.Discard(),
		Scheme:    scheme,
	}

	key := types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}
	require.NoError(t, reconciler.patchFinishedRunnerCleanupPatchIDStatus(context.Background(), key, 4))

	require.NotEmpty(t, capturedPatch, "expected the reconciler to emit a status patch")

	var emitted struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Status struct {
			FinishedRunnerCleanupPatchID int `json:"finishedRunnerCleanupPatchID"`
		} `json:"status"`
	}
	require.NoError(t, json.Unmarshal(capturedPatch, &emitted))

	assert.NotEmpty(
		t,
		emitted.Metadata.ResourceVersion,
		"status patch must carry a resourceVersion precondition so a stale write is rejected instead of recording the wrong patch ID, got %s",
		string(capturedPatch),
	)
	assert.Equal(t, 4, emitted.Status.FinishedRunnerCleanupPatchID)

	var updated v1alpha1.EphemeralRunnerSet
	require.NoError(t, c.Get(context.Background(), key, &updated))
	assert.Equal(t, 4, updated.Status.FinishedRunnerCleanupPatchID)
}

// TestPatchFinishedRunnerCleanupPatchIDStatusRecordsALowerPatchID pins the
// equality check against being "tidied" into the >= monotonicity check its
// neighbour uses.
//
// Applied revisions come from metadata.generation and only climb, but listener
// patch IDs do not: the scaler publishes 0 whenever the set is idle at
// MinRunners with nothing dirty, restarts its sequence from 0 on a listener
// restart, and wraps explicitly at math.MaxInt32. A marker that refused to move
// down would sit above every value the listener subsequently publishes, and
// because the scale-up guard suppresses only on an exact match, suppression
// would never fire again.
func TestPatchFinishedRunnerCleanupPatchIDStatusRecordsALowerPatchID(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			FinishedRunnerCleanupPatchID: 7,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ephemeralRunnerSet).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
		Build()

	reconciler := &EphemeralRunnerSetReconciler{
		Client:    c,
		APIReader: c,
		Log:       logr.Discard(),
		Scheme:    scheme,
	}

	key := types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}
	require.NoError(t, reconciler.patchFinishedRunnerCleanupPatchIDStatus(context.Background(), key, 1))

	var updated v1alpha1.EphemeralRunnerSet
	require.NoError(t, c.Get(context.Background(), key, &updated))
	assert.Equal(t, 1, updated.Status.FinishedRunnerCleanupPatchID,
		"a restarted or collapsed patch sequence must be recorded, or suppression can never match Spec.PatchID again")
}

// TestPatchFinishedRunnerCleanupPatchIDStatusIsIdempotent covers the early
// return: a marker already recording this patch ID must not be rewritten, so
// repeated reconciles for one patch do not churn the status.
func TestPatchFinishedRunnerCleanupPatchIDStatusIsIdempotent(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			FinishedRunnerCleanupPatchID: 4,
		},
	}

	patched := false
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ephemeralRunnerSet).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, clt client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patched = true
				return clt.Status().Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	reconciler := &EphemeralRunnerSetReconciler{
		Client:    c,
		APIReader: c,
		Log:       logr.Discard(),
		Scheme:    scheme,
	}

	key := types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}
	require.NoError(t, reconciler.patchFinishedRunnerCleanupPatchIDStatus(context.Background(), key, 4))

	assert.False(t, patched, "recording a patch ID the marker already holds must not emit a write")
}
