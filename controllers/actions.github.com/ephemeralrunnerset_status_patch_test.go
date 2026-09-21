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

// TestUpdateStatusNeverRepublishesTheCleanupMarker pins the invariant that makes
// it safe for Reconcile to copy the cleanup marker onto the object it carries
// from the top of the reconcile.
//
// That object comes from the cache, so its marker can be older than the API
// server's by the time the final status patch runs. The reason this cannot
// resurrect a superseded value is that updateStatus copies both the marker and
// the applied revision verbatim into desiredStatus and computes its patch as a
// diff against a copy taken at entry. Both sides of the diff therefore hold the
// same value, and a JSON merge patch emits nothing for a field that did not
// change: the only key updateStatus can ever produce is the phase.
//
// The invariant is not obvious from reading the function, it is load-bearing,
// and it has now been read the wrong way round twice in review -- once as
// updateStatus clobbering the marker with a stale zero, once as it restoring a
// stale marker over a newer one. Neither is possible while the field is copied
// rather than computed, so this test asserts on the emitted bytes.
//
// Two independent changes would make both readings real, and the test is
// written to fail on each of them. Taking original before the assignment rather
// than after puts the field in the diff. Computing the marker inside
// updateStatus, for instance from Spec.PatchID since the marker means "cleanup
// ran for this patch ID", puts a value in the diff that the caller never
// approved. The second is only detectable if the fixture gives Spec.PatchID a
// value distinct from the marker, which is why run refuses to accept equal ones.
func TestUpdateStatusNeverRepublishesTheCleanupMarker(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	key := types.NamespacedName{Namespace: "default", Name: "test-ers"}

	// specPatchID is the patch ID the listener has published; serverMarker is what
	// the API server holds by the time updateStatus runs; carriedMarker is what the
	// cached object in Reconcile carries, including the assignment made after the
	// authoritative write.
	//
	// All three have to be distinct. The marker is assigned from Spec.PatchID on
	// the cleanup path, so it is tempting to reuse one value for the spec and the
	// carried marker, but then copying the marker out of the status and computing
	// it from the spec produce the same number and no assertion can tell them
	// apart -- which is exactly the substitution this test exists to catch. The
	// require below keeps that from being reintroduced quietly.
	run := func(t *testing.T, specPatchID, serverMarker, carriedMarker int) ([]byte, int) {
		t.Helper()
		require.NotEqual(t, specPatchID, carriedMarker, "spec patch ID must differ from the carried marker or compute-from-spec is indistinguishable from copy-from-status")
		require.NotEqual(t, specPatchID, serverMarker, "spec patch ID must differ from the server marker or a republished value is indistinguishable from an untouched one")

		stored := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
			Spec:       v1alpha1.EphemeralRunnerSetSpec{PatchID: specPatchID},
			Status: v1alpha1.EphemeralRunnerSetStatus{
				Phase:                        v1alpha1.EphemeralRunnerSetPhaseRunning,
				FinishedRunnerCleanupPatchID: serverMarker,
			},
		}

		var capturedPatch []byte
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(stored).
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

		// The object Reconcile carries: a cached read whose marker has since been
		// overtaken, plus the assignment, and an empty phase so that updateStatus
		// has a genuine change to patch.
		carried := stored.DeepCopy()
		carried.Status.Phase = ""
		carried.Status.FinishedRunnerCleanupPatchID = carriedMarker

		require.NoError(t, reconciler.updateStatus(context.Background(), carried, &ephemeralRunnersByState{}, logr.Discard()))
		require.NotEmpty(t, capturedPatch, "expected updateStatus to emit a status patch for the phase change")

		var updated v1alpha1.EphemeralRunnerSet
		require.NoError(t, c.Get(context.Background(), key, &updated))

		return capturedPatch, updated.Status.FinishedRunnerCleanupPatchID
	}

	assertMarkerAbsent := func(t *testing.T, patch []byte) {
		t.Helper()

		var emitted struct {
			Status map[string]json.RawMessage `json:"status"`
		}
		require.NoError(t, json.Unmarshal(patch, &emitted))

		_, present := emitted.Status["finishedRunnerCleanupPatchID"]
		assert.False(t, present,
			"updateStatus must not carry the cleanup marker, or a cached reconcile could overwrite an authoritative write, got %s",
			string(patch))
	}

	t.Run("does not restore a marker another reconcile has cleared", func(t *testing.T) {
		// An actionable revision advanced and cleared the marker between the
		// authoritative write and this patch. Restoring it here would suppress the
		// scale up that rebuilds the pool for the new spec.
		patch, marker := run(t, 7, 0, 4)

		assertMarkerAbsent(t, patch)
		assert.Zero(t, marker, "a cleared marker must stay cleared")
	})

	t.Run("does not lower a marker another reconcile has advanced", func(t *testing.T) {
		// A concurrent cleanup recorded a newer patch ID. Lowering it back would
		// stop suppression matching the patch that was actually serviced.
		patch, marker := run(t, 7, 9, 4)

		assertMarkerAbsent(t, patch)
		assert.Equal(t, 9, marker, "a newer marker must not be overwritten by a cached one")
	})
}
