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

// TestUpdateStatusStaleOutdatedRunnersNeverSetTheOutdatedPhase pins the
// invariant that makes the Outdated early return in Reconcile safe.
//
// The Outdated branch returns through cleanUpEphemeralRunners without
// recomputing the phase, so the stale-runner recovery block further down is
// unreachable while the set is already Outdated. That is only harmless because
// a stale runner cannot put the set into the Outdated phase in the first place:
// updateStatus switches to Outdated on len(outdated) alone, and a runner whose
// revision predates the applied one is classified staleOutdated instead.
//
// The case that matters for upgrades is a runner carrying no revision
// annotation at all. It parses to revision 0, so on a set that has applied any
// revision above 0 it is stale, not outdated, and cannot strand the set in a
// phase that stops it scaling. Stale runners are still collected -- terminated()
// includes them -- they simply are not evidence about the current runner spec.
//
// Recomputing the phase inside the Outdated branch instead, as review has twice
// proposed, would not fix anything here and would break the phase outright:
// cleanUpEphemeralRunners deletes terminated(), which includes outdated, so
// "no outdated runners remain" is trivially true immediately after it runs. The
// set would return to Running against an unchanged spec, scale a replacement
// runner from that same spec, and be told Outdated again on the next pass.
func TestUpdateStatusStaleOutdatedRunnersNeverSetTheOutdatedPhase(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	legacyRunner := func() v1alpha1.EphemeralRunner {
		return v1alpha1.EphemeralRunner{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy-runner", Namespace: "default"},
			Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated},
		}
	}

	tests := []struct {
		name            string
		appliedRevision int64
		startingPhase   v1alpha1.EphemeralRunnerSetPhase
		wantPhase       v1alpha1.EphemeralRunnerSetPhase
	}{
		{
			// The upgrade case from review: an annotation-less runner on a set
			// that has already applied a revision must not stop it scaling.
			name:            "legacy runner on an upgraded set leaves the phase Running",
			appliedRevision: 4,
			startingPhase:   v1alpha1.EphemeralRunnerSetPhaseRunning,
			wantPhase:       v1alpha1.EphemeralRunnerSetPhaseRunning,
		},
		{
			// Before any revision is applied the same runner is genuinely
			// outdated, which is what keeps upgrades behaving as they do today.
			name:            "legacy runner before any revision is applied still reports Outdated",
			appliedRevision: 0,
			startingPhase:   v1alpha1.EphemeralRunnerSetPhaseRunning,
			wantPhase:       v1alpha1.EphemeralRunnerSetPhaseOutdated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ers", Namespace: "default"},
				Status: v1alpha1.EphemeralRunnerSetStatus{
					AppliedActionableRevision: tt.appliedRevision,
					Phase:                     tt.startingPhase,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(ephemeralRunnerSet).
				WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
				Build()

			reconciler := &EphemeralRunnerSetReconciler{
				Client: fakeClient,
				Log:    logr.Discard(),
				Scheme: scheme,
			}

			list := &v1alpha1.EphemeralRunnerList{Items: []v1alpha1.EphemeralRunner{legacyRunner()}}
			state := newEphemeralRunnersByStates(list, tt.appliedRevision)

			require.NoError(t, reconciler.updateStatus(context.Background(), ephemeralRunnerSet, state, logr.Discard()))

			var patched v1alpha1.EphemeralRunnerSet
			require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "test-ers"}, &patched))

			assert.Equal(
				t,
				tt.wantPhase,
				patched.Status.Phase,
				"a stale runner must not drive the set into a phase that stops it scaling, because the Outdated branch returns without recomputing the phase",
			)
		})
	}
}
