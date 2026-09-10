package actionsgithubcom

import (
	"strconv"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func outdatedRunnerAtRevision(name string, revision int64) v1alpha1.EphemeralRunner {
	return v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				AnnotationKeyActionableRevision: strconv.FormatInt(revision, 10),
			},
		},
		Status: v1alpha1.EphemeralRunnerStatus{
			Phase: v1alpha1.EphemeralRunnerPhaseOutdated,
		},
	}
}

// TestNewEphemeralRunnersByStates_OutdatedIsRevisionScoped covers the core of the
// outdated-recovery behaviour: a runner that reported Outdated against a runner
// spec that has since been replaced must not be treated as evidence about the
// current spec.
func TestNewEphemeralRunnersByStates_OutdatedIsRevisionScoped(t *testing.T) {
	tests := []struct {
		name                  string
		runners               []v1alpha1.EphemeralRunner
		appliedRevision       int64
		wantOutdatedNames     []string
		wantStaleOutdatedName []string
	}{
		{
			name:              "runner at the applied revision is genuinely outdated",
			runners:           []v1alpha1.EphemeralRunner{outdatedRunnerAtRevision("current", 3)},
			appliedRevision:   3,
			wantOutdatedNames: []string{"current"},
		},
		{
			name:                  "runner from before the last spec update is stale",
			runners:               []v1alpha1.EphemeralRunner{outdatedRunnerAtRevision("old", 2)},
			appliedRevision:       3,
			wantStaleOutdatedName: []string{"old"},
		},
		{
			name: "mixed revisions are split",
			runners: []v1alpha1.EphemeralRunner{
				outdatedRunnerAtRevision("old", 1),
				outdatedRunnerAtRevision("current", 4),
			},
			appliedRevision:       4,
			wantOutdatedNames:     []string{"current"},
			wantStaleOutdatedName: []string{"old"},
		},
		{
			name: "runner without the annotation is treated as revision 0",
			runners: []v1alpha1.EphemeralRunner{{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy"},
				Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated},
			}},
			appliedRevision:   0,
			wantOutdatedNames: []string{"legacy"},
		},
		{
			name: "legacy runner becomes stale once a revision is applied",
			runners: []v1alpha1.EphemeralRunner{{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy"},
				Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated},
			}},
			appliedRevision:       1,
			wantStaleOutdatedName: []string{"legacy"},
		},
		{
			name: "a runner being deleted is never classified as outdated",
			runners: []v1alpha1.EphemeralRunner{func() v1alpha1.EphemeralRunner {
				runner := outdatedRunnerAtRevision("terminating", 3)
				now := metav1.Now()
				runner.DeletionTimestamp = &now
				runner.Finalizers = []string{ephemeralRunnerFinalizerName}
				return runner
			}()},
			appliedRevision: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := &v1alpha1.EphemeralRunnerList{Items: tt.runners}
			state := newEphemeralRunnersByStates(list, tt.appliedRevision)

			assert.Equal(t, tt.wantOutdatedNames, runnerNames(state.outdated))
			assert.Equal(t, tt.wantStaleOutdatedName, runnerNames(state.staleOutdated))
		})
	}
}

// TestEphemeralRunnersByState_TerminatedIncludesStaleOutdated ensures the cleanup
// paths still collect stale outdated runners; they are excluded from the phase
// decision, not from garbage collection.
func TestEphemeralRunnersByState_TerminatedIncludesStaleOutdated(t *testing.T) {
	list := &v1alpha1.EphemeralRunnerList{Items: []v1alpha1.EphemeralRunner{
		outdatedRunnerAtRevision("stale", 1),
		outdatedRunnerAtRevision("current", 5),
		{
			ObjectMeta: metav1.ObjectMeta{Name: "succeeded"},
			Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "failed"},
			Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed},
		},
	}}

	state := newEphemeralRunnersByStates(list, 5)

	assert.ElementsMatch(t,
		[]string{"succeeded", "failed", "current", "stale"},
		runnerNames(state.terminated()),
	)
}

// TestEphemeralRunnersByState_TerminatedDoesNotAliasBackingArrays guards against
// terminated() corrupting the slices it concatenates, which would silently
// reclassify runners.
func TestEphemeralRunnersByState_TerminatedDoesNotAliasBackingArrays(t *testing.T) {
	list := &v1alpha1.EphemeralRunnerList{Items: []v1alpha1.EphemeralRunner{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "succeeded"},
			Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "failed"},
			Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed},
		},
	}}

	state := newEphemeralRunnersByStates(list, 0)
	_ = state.terminated()

	assert.Equal(t, []string{"succeeded"}, runnerNames(state.finished))
	assert.Equal(t, []string{"failed"}, runnerNames(state.failed))
}

// TestEphemeralRunnerSetOutdatedForAppliedRevision covers the guard that stops the
// AutoscalingRunnerSet from tearing the scale set down on an Outdated verdict that
// predates the runner spec it has just published.
func TestEphemeralRunnerSetOutdatedForAppliedRevision(t *testing.T) {
	tests := []struct {
		name            string
		phase           v1alpha1.EphemeralRunnerSetPhase
		specRevision    int64
		appliedRevision int64
		want            bool
	}{
		{
			name:  "nil-safe: running set is not outdated",
			phase: v1alpha1.EphemeralRunnerSetPhaseRunning,
			want:  false,
		},
		{
			name:            "outdated against the spec it is running",
			phase:           v1alpha1.EphemeralRunnerSetPhaseOutdated,
			specRevision:    4,
			appliedRevision: 4,
			want:            true,
		},
		{
			name:            "outdated verdict predates a spec update that is still propagating",
			phase:           v1alpha1.EphemeralRunnerSetPhaseOutdated,
			specRevision:    5,
			appliedRevision: 4,
			want:            false,
		},
		{
			name:            "legacy set with no revisions recorded still tears down",
			phase:           v1alpha1.EphemeralRunnerSetPhaseOutdated,
			specRevision:    0,
			appliedRevision: 0,
			want:            true,
		},
		{
			name:            "running set with a pending revision is not outdated",
			phase:           v1alpha1.EphemeralRunnerSetPhaseRunning,
			specRevision:    5,
			appliedRevision: 4,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
				Spec:   v1alpha1.EphemeralRunnerSetSpec{ActionableRevision: tt.specRevision},
				Status: v1alpha1.EphemeralRunnerSetStatus{Phase: tt.phase, AppliedActionableRevision: tt.appliedRevision},
			}
			assert.Equal(t, tt.want, ephemeralRunnerSetOutdatedForAppliedRevision(ephemeralRunnerSet))
		})
	}

	assert.False(t, ephemeralRunnerSetOutdatedForAppliedRevision(nil))
}

func runnerNames(runners []*v1alpha1.EphemeralRunner) []string {
	if len(runners) == 0 {
		return nil
	}
	names := make([]string, 0, len(runners))
	for _, runner := range runners {
		names = append(names, runner.Name)
	}
	return names
}
