package actionsgithubcom

import (
	"testing"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAggregateEphemeralRunnerLifecycle(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name     string
		runners  []v1alpha1.EphemeralRunner
		expected EphemeralRunnerLifecycleBuckets
	}{
		{
			name:    "empty list",
			runners: []v1alpha1.EphemeralRunner{},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 0, Running: 0, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 0,
			},
		},
		{
			name: "all phases without deletion timestamp",
			runners: []v1alpha1.EphemeralRunner{
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhasePending}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated}},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 1, Running: 1, Succeeded: 1, Failed: 1, Outdated: 1, Deleting: 0,
			},
		},
		{
			name: "empty phase defaults to pending",
			runners: []v1alpha1.EphemeralRunner{
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: ""}},
				{Status: v1alpha1.EphemeralRunnerStatus{}},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 2, Running: 0, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 0,
			},
		},
		{
			name: "unrecognized phase defaults to pending",
			runners: []v1alpha1.EphemeralRunner{
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: "UnknownPhase"}},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 1, Running: 0, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 0,
			},
		},
		{
			name: "deletion timestamp takes precedence over all phases",
			runners: []v1alpha1.EphemeralRunner{
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhasePending},
				},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded},
				},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed},
				},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated},
				},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 0, Running: 0, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 5,
			},
		},
		{
			name: "edge case: running phase with deletion timestamp goes to deleting",
			runners: []v1alpha1.EphemeralRunner{
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning},
				},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 0, Running: 0, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 1,
			},
		},
		{
			name: "mixed runners with and without deletion timestamp",
			runners: []v1alpha1.EphemeralRunner{
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning},
				},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed}},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed},
				},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 0, Running: 1, Succeeded: 0, Failed: 1, Outdated: 0, Deleting: 2,
			},
		},
		{
			name: "multiple runners in same phase",
			runners: []v1alpha1.EphemeralRunner{
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 0, Running: 3, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 0,
			},
		},
		{
			name: "deletion timestamp with empty phase goes to deleting",
			runners: []v1alpha1.EphemeralRunner{
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: ""},
				},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 0, Running: 0, Succeeded: 0, Failed: 0, Outdated: 0, Deleting: 1,
			},
		},
		{
			name: "comprehensive mixed scenario",
			runners: []v1alpha1.EphemeralRunner{
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhasePending}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhasePending}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseFailed}},
				{Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseOutdated}},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
					Status:     v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded},
				},
			},
			expected: EphemeralRunnerLifecycleBuckets{
				Pending: 2, Running: 3, Succeeded: 1, Failed: 2, Outdated: 1, Deleting: 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AggregateEphemeralRunnerLifecycle(tt.runners)

			if result.Pending != tt.expected.Pending {
				t.Errorf("Pending count mismatch: got %d, want %d", result.Pending, tt.expected.Pending)
			}
			if result.Running != tt.expected.Running {
				t.Errorf("Running count mismatch: got %d, want %d", result.Running, tt.expected.Running)
			}
			if result.Succeeded != tt.expected.Succeeded {
				t.Errorf("Succeeded count mismatch: got %d, want %d", result.Succeeded, tt.expected.Succeeded)
			}
			if result.Failed != tt.expected.Failed {
				t.Errorf("Failed count mismatch: got %d, want %d", result.Failed, tt.expected.Failed)
			}
			if result.Outdated != tt.expected.Outdated {
				t.Errorf("Outdated count mismatch: got %d, want %d", result.Outdated, tt.expected.Outdated)
			}
			if result.Deleting != tt.expected.Deleting {
				t.Errorf("Deleting count mismatch: got %d, want %d", result.Deleting, tt.expected.Deleting)
			}

			total := result.Pending + result.Running + result.Succeeded + result.Failed + result.Outdated + result.Deleting
			if total != len(tt.runners) {
				t.Errorf("Total count mismatch: counted %d runners but input had %d runners", total, len(tt.runners))
			}
		})
	}
}

func TestAggregateEphemeralRunnerLifecycle_EdgePrecedence(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name            string
		phase           v1alpha1.EphemeralRunnerPhase
		hasDeletionTime bool
		expectedBucket  string
	}{
		{
			name:            "Running without deletion timestamp",
			phase:           v1alpha1.EphemeralRunnerPhaseRunning,
			hasDeletionTime: false,
			expectedBucket:  "running",
		},
		{
			name:            "Running with deletion timestamp goes to deleting",
			phase:           v1alpha1.EphemeralRunnerPhaseRunning,
			hasDeletionTime: true,
			expectedBucket:  "deleting",
		},
		{
			name:            "Succeeded without deletion timestamp",
			phase:           v1alpha1.EphemeralRunnerPhaseSucceeded,
			hasDeletionTime: false,
			expectedBucket:  "succeeded",
		},
		{
			name:            "Succeeded with deletion timestamp goes to deleting",
			phase:           v1alpha1.EphemeralRunnerPhaseSucceeded,
			hasDeletionTime: true,
			expectedBucket:  "deleting",
		},
		{
			name:            "Failed without deletion timestamp",
			phase:           v1alpha1.EphemeralRunnerPhaseFailed,
			hasDeletionTime: false,
			expectedBucket:  "failed",
		},
		{
			name:            "Failed with deletion timestamp goes to deleting",
			phase:           v1alpha1.EphemeralRunnerPhaseFailed,
			hasDeletionTime: true,
			expectedBucket:  "deleting",
		},
		{
			name:            "Outdated without deletion timestamp",
			phase:           v1alpha1.EphemeralRunnerPhaseOutdated,
			hasDeletionTime: false,
			expectedBucket:  "outdated",
		},
		{
			name:            "Outdated with deletion timestamp goes to deleting",
			phase:           v1alpha1.EphemeralRunnerPhaseOutdated,
			hasDeletionTime: true,
			expectedBucket:  "deleting",
		},
		{
			name:            "Pending without deletion timestamp",
			phase:           v1alpha1.EphemeralRunnerPhasePending,
			hasDeletionTime: false,
			expectedBucket:  "pending",
		},
		{
			name:            "Pending with deletion timestamp goes to deleting",
			phase:           v1alpha1.EphemeralRunnerPhasePending,
			hasDeletionTime: true,
			expectedBucket:  "deleting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := v1alpha1.EphemeralRunner{
				Status: v1alpha1.EphemeralRunnerStatus{Phase: tt.phase},
			}
			if tt.hasDeletionTime {
				runner.DeletionTimestamp = &now
			}

			result := AggregateEphemeralRunnerLifecycle([]v1alpha1.EphemeralRunner{runner})

			var foundBucket string
			if result.Pending == 1 {
				foundBucket = "pending"
			} else if result.Running == 1 {
				foundBucket = "running"
			} else if result.Succeeded == 1 {
				foundBucket = "succeeded"
			} else if result.Failed == 1 {
				foundBucket = "failed"
			} else if result.Outdated == 1 {
				foundBucket = "outdated"
			} else if result.Deleting == 1 {
				foundBucket = "deleting"
			}

			if foundBucket != tt.expectedBucket {
				t.Errorf("Expected runner to be in %s bucket, but found in %s bucket", tt.expectedBucket, foundBucket)
			}

			total := result.Pending + result.Running + result.Succeeded + result.Failed + result.Outdated + result.Deleting
			if total != 1 {
				t.Errorf("Expected exactly 1 runner to be counted, but got %d", total)
			}
		})
	}
}
