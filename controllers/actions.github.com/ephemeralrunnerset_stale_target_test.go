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

// TestPatchAppliedActionableRevisionStatusIgnoresATargetBehindTheLiveRevision
// pins which revision the runners are classified against.
//
// Reconcile reads the EphemeralRunnerSet through the cache and passes
// Spec.ActionableRevision as the target, while this function re-reads the
// status through APIReader. The two can disagree: if another reconcile has
// already advanced the live marker, a lagging reconcile arrives with a target
// behind it. The monotonicity guard stops the marker regressing, but the phase
// is derived separately and has no such protection.
//
// Classifying against the stale target rates a runner from the superseded
// revision as current rather than staleOutdated, which flips a set that has
// already moved on back to Outdated. That is not self-correcting: Reconcile's
// Outdated branch returns before updateStatus, and this function only runs
// while spec > applied, so the set stays switched off until the next spec
// change.
//
// A single client is enough here, because the property under test is which
// integer reaches the classifier, not how the data was read. The revision gap
// is created in the stored object rather than by disagreeing views.
func TestPatchAppliedActionableRevisionStatusIgnoresATargetBehindTheLiveRevision(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	// The live set has already applied revision 5.
	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ers",
			Namespace: "default",
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			ActionableRevision: 5,
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			AppliedActionableRevision: 5,
			Phase:                     v1alpha1.EphemeralRunnerSetPhaseRunning,
		},
	}

	// A runner left over from revision 4 reporting Outdated. Against the live
	// revision 5 it is staleOutdated and must not hold the set Outdated.
	controllerRef := true
	supersededRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-from-revision-4",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationKeyActionableRevision: "4",
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

	// The lagging reconcile still believes revision 4 is the one to apply.
	require.NoError(t, reconciler.patchAppliedActionableRevisionStatus(context.Background(), key, 4))

	var patched v1alpha1.EphemeralRunnerSet
	require.NoError(t, fakeClient.Get(context.Background(), key, &patched))

	assert.Equal(
		t,
		v1alpha1.EphemeralRunnerSetPhaseRunning,
		patched.Status.Phase,
		"a runner from a superseded revision must be judged against the live applied revision, not against a target that has fallen behind it: rating it as current flips the set to Outdated, and the Outdated phase is absorbing, so the set never scales again until the spec changes",
	)

	assert.Equal(
		t,
		int64(5),
		patched.Status.AppliedActionableRevision,
		"the monotonicity guard must keep the marker from regressing to the stale target",
	)
}
