package actionsgithubcom

import (
	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

// EphemeralRunnerLifecycleBuckets represents the counts of ephemeral runners
// in each lifecycle state according to the precedence contract:
// 1. deleting if DeletionTimestamp != nil
// 2. Otherwise by Status.Phase (Running/Succeeded/Failed/Outdated)
// 3. Fallback to pending for empty/unset/other
type EphemeralRunnerLifecycleBuckets struct {
	Pending   int
	Running   int
	Succeeded int
	Failed    int
	Outdated  int
	Deleting  int
}

// AggregateEphemeralRunnerLifecycle classifies a list of EphemeralRunners into
// lifecycle buckets using deterministic precedence to ensure each runner is counted
// in exactly one bucket. This helper is independent from EphemeralRunnerSet-specific
// structures and can be called directly by any controller managing EphemeralRunners.
//
// Precedence contract:
// 1. deleting if DeletionTimestamp != nil (takes precedence over all phases)
// 2. Otherwise by Status.Phase: Running/Succeeded/Failed/Outdated
// 3. Fallback to pending for empty/unset/other phases
func AggregateEphemeralRunnerLifecycle(runners []v1alpha1.EphemeralRunner) EphemeralRunnerLifecycleBuckets {
	var buckets EphemeralRunnerLifecycleBuckets

	for i := range runners {
		r := &runners[i]

		// Precedence 1: DeletionTimestamp takes precedence over all phases
		if !r.DeletionTimestamp.IsZero() {
			buckets.Deleting++
			continue
		}

		// Precedence 2: Classify by Status.Phase
		switch r.Status.Phase {
		case v1alpha1.EphemeralRunnerPhaseRunning:
			buckets.Running++
		case v1alpha1.EphemeralRunnerPhaseSucceeded:
			buckets.Succeeded++
		case v1alpha1.EphemeralRunnerPhaseFailed:
			buckets.Failed++
		case v1alpha1.EphemeralRunnerPhaseOutdated:
			buckets.Outdated++
		default:
			// Precedence 3: Fallback to pending for empty/unset/other phases
			// This includes EphemeralRunnerPhasePending and any unset or unrecognized phases
			buckets.Pending++
		}
	}

	return buckets
}
