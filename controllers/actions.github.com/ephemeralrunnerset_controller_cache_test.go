package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEphemeralRunnerSetSpecHashCache_HitRequiresMatchingUID(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}

	reconciler.setSpecHashCache(name, types.UID("uid-1"), 7, "hash-1")

	require.True(t, reconciler.hasSpecHashCache(name, types.UID("uid-1"), 7, "hash-1"), "expected cache hit for matching uid, generation, and hash")

	require.False(t, reconciler.hasSpecHashCache(name, types.UID("uid-2"), 7, "hash-1"), "expected cache miss when uid changes")
}

func TestEphemeralRunnerSetSpecHashCache_ClearRemovesEntry(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}

	reconciler.setSpecHashCache(name, types.UID("uid-1"), 3, "hash-3")
	require.True(t, reconciler.hasSpecHashCache(name, types.UID("uid-1"), 3, "hash-3"), "expected cache hit before clear")

	reconciler.clearSpecHashCache(name)
	require.False(t, reconciler.hasSpecHashCache(name, types.UID("uid-1"), 3, "hash-3"), "expected cache miss after clear")
}

func TestEphemeralRunnerSetScaleStateCache_InitializesFromStatus(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}
	runnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-1")},
		Spec:       v1alpha1.EphemeralRunnerSetSpec{PatchID: 7, Replicas: 3},
		Status:     v1alpha1.EphemeralRunnerSetStatus{ReservedPatchID: 7, ReservedReplicas: 3},
	}

	state := reconciler.getScaleState(name, runnerSet, 0)
	require.Equal(t, runnerSet.UID, state.uid)
	require.Equal(t, 7, state.patchID)
	require.Equal(t, 3, state.replicas)
}

func TestEphemeralRunnerSetScaleStateCache_UIDMismatchReinitializes(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}
	reconciler.setScaleStateCache(name, scaleStateCacheEntry{uid: types.UID("uid-1"), patchID: 7, replicas: 4})
	runnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-2")},
		Spec:       v1alpha1.EphemeralRunnerSetSpec{PatchID: 7, Replicas: 2},
	}

	state := reconciler.getScaleState(name, runnerSet, 2)
	require.Equal(t, runnerSet.UID, state.uid)
	require.Equal(t, 7, state.patchID)
	require.Equal(t, 2, state.replicas)
}

func TestEphemeralRunnerSetScaleStateCache_ClusterCountIsFloor(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}
	runnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-1")},
		Spec:       v1alpha1.EphemeralRunnerSetSpec{PatchID: 7, Replicas: 3},
		Status:     v1alpha1.EphemeralRunnerSetStatus{ReservedPatchID: 7, ReservedReplicas: 1},
	}

	state := reconciler.getScaleState(name, runnerSet, 2)
	require.Equal(t, 2, state.replicas, "expected cluster count to be a floor for reserved replicas")
}

func TestEphemeralRunnerSetScaleStateCache_CapsAtDesiredReplicas(t *testing.T) {
	t.Parallel()

	reconciler := &EphemeralRunnerSetReconciler{}
	name := types.NamespacedName{Namespace: "default", Name: "test-ers"}
	runnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-1")},
		Spec:       v1alpha1.EphemeralRunnerSetSpec{PatchID: 7, Replicas: 1},
		Status:     v1alpha1.EphemeralRunnerSetStatus{ReservedPatchID: 7, ReservedReplicas: 3},
	}

	state := reconciler.getScaleState(name, runnerSet, 2)
	require.Equal(t, 1, state.replicas, "expected reserved replicas to be capped by desired replicas")
}

func TestEphemeralRunnerSetStateTracksLatestPatchID(t *testing.T) {
	t.Parallel()

	runners := &v1alpha1.EphemeralRunnerList{
		Items: []v1alpha1.EphemeralRunner{
			{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKeyPatchID: "7"}}},
			{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKeyPatchID: "12"}}},
			{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKeyPatchID: "8"}}},
			{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationKeyPatchID: "invalid"}}},
		},
	}

	state := newEphemeralRunnersByStates(runners, false, true)
	require.Equal(t, 12, state.latestPatchID)
}

func TestEphemeralRunnerUpdateAffectsScale(t *testing.T) {
	t.Parallel()

	runningRunner := &v1alpha1.EphemeralRunner{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}}
	readyRunner := runningRunner.DeepCopy()
	readyRunner.Status.Ready = true
	succeededRunner := runningRunner.DeepCopy()
	succeededRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
	failedRunner := runningRunner.DeepCopy()
	failedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseFailed
	deletingRunner := runningRunner.DeepCopy()
	now := metav1.Now()
	deletingRunner.DeletionTimestamp = &now
	newPatchRunner := runningRunner.DeepCopy()
	newPatchRunner.Annotations = map[string]string{AnnotationKeyPatchID: "9"}

	require.False(t, ephemeralRunnerUpdateAffectsScale(runningRunner, readyRunner), "expected readiness-only runner update to be ignored")
	require.True(t, ephemeralRunnerUpdateAffectsScale(runningRunner, succeededRunner), "expected succeeded runner update to affect scale")
	require.True(t, ephemeralRunnerUpdateAffectsScale(runningRunner, failedRunner), "expected failed runner update to refresh status counts")
	require.True(t, ephemeralRunnerUpdateAffectsScale(runningRunner, deletingRunner), "expected deleting runner update to affect scale")
	require.True(t, ephemeralRunnerUpdateAffectsScale(runningRunner, newPatchRunner), "expected patch ID update to affect scale")
	registeredRunner := runningRunner.DeepCopy()
	registeredRunner.Status.RunnerID = 1
	require.True(t, ephemeralRunnerUpdateAffectsScale(runningRunner, registeredRunner), "expected runner registration update to affect scale")
}

func TestDeleteAllEphemeralRunnersForSetDeletesByUIDLabel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	runnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runner-set",
			Namespace: "default",
			UID:       types.UID("runner-set-uid"),
		},
	}
	linkedRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linked-runner",
			Namespace: runnerSet.Namespace,
			Labels: map[string]string{
				LabelKeyEphemeralRunnerSetUID: string(runnerSet.UID),
			},
		},
	}
	unrelatedRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unrelated-runner",
			Namespace: runnerSet.Namespace,
			Labels: map[string]string{
				LabelKeyEphemeralRunnerSetUID: "other-runner-set-uid",
			},
		},
	}
	otherNamespaceRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-namespace-runner",
			Namespace: "other-namespace",
			Labels: map[string]string{
				LabelKeyEphemeralRunnerSetUID: string(runnerSet.UID),
			},
		},
	}

	reconciler := &EphemeralRunnerSetReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(linkedRunner, unrelatedRunner, otherNamespaceRunner).Build(),
	}

	require.NoError(t, reconciler.deleteAllEphemeralRunnersForSet(ctx, runnerSet, logr.Discard()))

	err := reconciler.Get(ctx, types.NamespacedName{Namespace: linkedRunner.Namespace, Name: linkedRunner.Name}, &v1alpha1.EphemeralRunner{})
	require.True(t, kerrors.IsNotFound(err), "expected linked runner to be deleted, got %v", err)
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Namespace: unrelatedRunner.Namespace, Name: unrelatedRunner.Name}, &v1alpha1.EphemeralRunner{}), "expected unrelated runner to remain")
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Namespace: otherNamespaceRunner.Namespace, Name: otherNamespaceRunner.Name}, &v1alpha1.EphemeralRunner{}), "expected other namespace runner to remain")
}
