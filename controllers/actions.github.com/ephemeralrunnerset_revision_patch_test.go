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

// TestPatchAppliedActionableRevisionStatusUsesOptimisticLock pins the property
// that makes the applied revision marker monotonic: the status patch must carry
// a resourceVersion precondition.
//
// Without it the API server cannot reject the write as conflicting, so the
// surrounding retry.RetryOnConflict never fires and the freshness check inside
// it is unsound. Both the target revision and the re-fetched object come from
// the cache-backed client, so a stale reconcile can compare a stale target
// against an equally stale read, pass the check, and patch the applied revision
// backwards. That re-satisfies the spec > applied comparison in Reconcile and
// sends the controller through the idle and pending runner cleanup again.
//
// The property is asserted on the bytes the production code actually emits
// rather than on an independently constructed patch, so the test cannot pass
// while the reconciler builds its patch some other way. Reverting the patch
// option to a plain client.MergeFrom must fail this test.
func TestPatchAppliedActionableRevisionStatusUsesOptimisticLock(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			ActionableRevision: 7,
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			AppliedActionableRevision: 2,
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
		Client: c,
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	key := types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}
	require.NoError(t, reconciler.patchAppliedActionableRevisionStatus(context.Background(), key, 7))

	require.NotEmpty(t, capturedPatch, "expected the reconciler to emit a status patch")

	var emitted struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Status struct {
			AppliedActionableRevision int64 `json:"appliedActionableRevision"`
		} `json:"status"`
	}
	require.NoError(t, json.Unmarshal(capturedPatch, &emitted))

	assert.NotEmpty(
		t,
		emitted.Metadata.ResourceVersion,
		"status patch must carry a resourceVersion precondition so a stale write is rejected instead of moving the applied revision backwards, got %s",
		string(capturedPatch),
	)
	assert.Equal(t, int64(7), emitted.Status.AppliedActionableRevision)

	var updated v1alpha1.EphemeralRunnerSet
	require.NoError(t, c.Get(context.Background(), key, &updated))
	assert.Equal(t, int64(7), updated.Status.AppliedActionableRevision)
}

// TestPatchAppliedActionableRevisionStatusDoesNotMoveBackwards covers the guard
// inside the retry: a reconcile carrying an older target revision must leave a
// marker that has already advanced further alone.
func TestPatchAppliedActionableRevisionStatusDoesNotMoveBackwards(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			AppliedActionableRevision: 5,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ephemeralRunnerSet).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
		Build()

	reconciler := &EphemeralRunnerSetReconciler{
		Client: c,
		Log:    logr.Discard(),
		Scheme: scheme,
	}

	key := types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}
	require.NoError(t, reconciler.patchAppliedActionableRevisionStatus(context.Background(), key, 3))

	var updated v1alpha1.EphemeralRunnerSet
	require.NoError(t, c.Get(context.Background(), key, &updated))
	assert.Equal(t, int64(5), updated.Status.AppliedActionableRevision)
}
