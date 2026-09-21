package actionsgithubcom

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// A stop the listener controller never observed - because the scale set
// recovered before it reconciled, and the queue coalesced the two phase writes -
// leaves the listener pod in place. Nothing re-runs the cleanup that was skipped,
// so the restart has to converge the pod on its own: what the scale set comes
// back with must be the pod its current spec describes, not the one it was
// parked with. Reconciliation is level-triggered, so the comparison below is
// what makes the skipped stop harmless rather than a stale listener.
func TestAStoppedListenerThatKeptItsPodStillConvergesOnRestart(t *testing.T) {
	current := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name:  "listener",
		Image: "listener:image",
		Env:   []corev1.EnvVar{{Name: "MIN_RUNNERS", Value: "1"}},
	}}}}

	desired := current.DeepCopy()
	desired.Spec.Containers[0].Env[0].Value = "2"

	require.True(
		t,
		listenerPodSpecRequiresRecreation(current, desired),
		"a pod left over from before the stop must be rebuilt when the spec moved on while parked",
	)
	require.False(
		t,
		listenerPodSpecRequiresRecreation(current, current.DeepCopy()),
		"a pod that already matches must be kept, so a skipped stop is not a reason to churn it",
	)
}
