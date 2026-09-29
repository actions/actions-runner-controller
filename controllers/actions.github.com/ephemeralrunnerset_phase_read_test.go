package actionsgithubcom

import (
	"context"
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
)

// TestPatchAppliedActionableRevisionStatusDerivesPhaseFromAuthoritativeRead
// pins the read that the derived phase is computed from.
//
// patchAppliedActionableRevisionStatus reads the EphemeralRunnerSet through
// APIReader precisely because a cached read cannot be trusted here, then
// derives Status.Phase from a list of the child runners. If that list goes
// through the cache-backed client instead, the phase is derived from data the
// surrounding optimistic lock does not cover: the lock proves only that the
// EphemeralRunnerSet was live at write time, never that the list was.
//
// A wrong phase does not merely flap. Reconcile's Outdated branch cleans up and
// returns before reaching updateStatus, and this function only runs while
// spec > applied, which it then makes false by advancing the marker. So a phase
// wrongly set to Outdated is never recomputed, and the set stays switched off.
//
// The two clients below disagree on purpose. The authoritative one holds no
// runners, so the correct phase is Running. The cached one holds an Outdated
// runner at the revision being applied, which is the phase a cached list would
// produce. Listing through the cache-backed client must fail this test.
func TestPatchAppliedActionableRevisionStatusDerivesPhaseFromAuthoritativeRead(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	newEphemeralRunnerSet := func() *v1alpha1.EphemeralRunnerSet {
		return &v1alpha1.EphemeralRunnerSet{
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
	}

	// Phase Outdated at the revision being applied, so the classifier counts it
	// as outdated rather than staleOutdated.
	controllerRef := true
	staleView := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-from-cache",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKeyActionableRevision: "7",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "EphemeralRunnerSet",
					Name:       "test-ers",
					UID:        "test-uid",
					Controller: &controllerRef,
				},
			},
		},
		Status: v1alpha1.EphemeralRunnerStatus{
			Phase: v1alpha1.EphemeralRunnerPhaseOutdated,
		},
	}

	builder := func(objs ...client.Object) client.Client {
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
			WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
			Build()
	}

	// The cache-backed client still sees a runner the API server no longer has.
	cached := builder(newEphemeralRunnerSet(), staleView)
	// The authoritative view: the runner is gone, so nothing is outdated.
	authoritative := builder(newEphemeralRunnerSet())

	reconciler := &EphemeralRunnerSetReconciler{
		Client:    cached,
		APIReader: authoritative,
		Log:       logr.Discard(),
		Scheme:    scheme,
	}

	key := types.NamespacedName{Namespace: "default", Name: "test-ers"}
	require.NoError(t, reconciler.patchAppliedActionableRevisionStatus(context.Background(), key, 7))

	var patched v1alpha1.EphemeralRunnerSet
	require.NoError(t, cached.Get(context.Background(), key, &patched))

	assert.Equal(
		t,
		v1alpha1.EphemeralRunnerSetPhaseRunning,
		patched.Status.Phase,
		"phase must be derived from the authoritative read, not the cache: the cached client holds an outdated runner the API server no longer has, and a phase of Outdated is never recomputed because Reconcile's Outdated branch returns before updateStatus",
	)
	assert.Equal(t, int64(7), patched.Status.AppliedActionableRevision)
}
