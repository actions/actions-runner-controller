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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestPatchAppliedActionableRevisionStatusClassifiesAgainstTheAdvancedRevision
// pins the other half of the classifier contract: which revision is used when
// the monotonicity guard actually fires.
//
// TestPatchAppliedActionableRevisionStatusIgnoresATargetBehindTheLiveRevision
// covers the case where the target has fallen behind the live marker, so the
// guard does not fire and the applied revision is unchanged. That case cannot
// observe this one. The guard writes the advance to a copy of the status, so
// only when it fires do the copy and the stored status hold different
// revisions, and only then can the classifier be pointed at the wrong one.
//
// Reading the pre-advance revision rates a runner left over from the superseded
// revision as current instead of staleOutdated, which sets the Outdated phase.
// That phase is then persisted next to the freshly advanced revision, which is
// the state this function exists to avoid: Reconcile's Outdated branch returns
// before updateStatus, and this function only runs while spec > applied, so
// nothing recomputes the phase and the set stays switched off until the next
// spec change.
func TestPatchAppliedActionableRevisionStatusClassifiesAgainstTheAdvancedRevision(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	// The spec has moved to revision 2 while the status still records 1, so the
	// guard below advances the marker and the two revisions differ.
	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			ActionableRevision: 2,
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			AppliedActionableRevision: 1,
			Phase:                     v1alpha1.EphemeralRunnerSetPhaseRunning,
		},
	}

	// A runner built from revision 1 that the service rejected. Applying
	// revision 2 deletes the idle and pending runners, but this one can survive
	// the cleanup: the cleanup list is read through the cache and can be stale,
	// which is the case the reviewer of this code identified.
	//
	// Its revision equals the pre-advance marker, so classifying against that
	// value puts it in outdated rather than staleOutdated. Against the advanced
	// revision it is correctly stale.
	controllerRef := true
	supersededRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-from-revision-1",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKeyActionableRevision: "1",
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

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ephemeralRunnerSet, supersededRunner).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		Build()

	reconciler := &EphemeralRunnerSetReconciler{
		Client:    fakeClient,
		APIReader: fakeClient,
		Log:       logr.Discard(),
		Scheme:    scheme,
	}

	key := types.NamespacedName{Namespace: "default", Name: "test-ers"}

	require.NoError(t, reconciler.patchAppliedActionableRevisionStatus(context.Background(), key, 2))

	var patched v1alpha1.EphemeralRunnerSet
	require.NoError(t, fakeClient.Get(context.Background(), key, &patched))

	assert.Equal(
		t,
		int64(2),
		patched.Status.AppliedActionableRevision,
		"the guard must advance the marker to the target revision",
	)

	assert.Equal(
		t,
		v1alpha1.EphemeralRunnerSetPhaseRunning,
		patched.Status.Phase,
		"the leftover runner reported Outdated against revision 1, which this call has just superseded, so it must be classified against the advanced revision and ignored: counting it against the pre-advance revision saves the Outdated phase alongside revision 2, and nothing recomputes the phase from there",
	)
}
