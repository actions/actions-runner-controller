package actionsgithubcom

import (
	"encoding/json"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// roundTripThroughAPIServer simulates what happens when the controller writes an
// EphemeralRunnerSet and then reads it back: the Go value is marshalled to JSON
// (dropping fields tagged omitempty, including explicitly-empty slices and maps)
// and decoded again. Fields that were empty-but-non-nil come back nil.
func roundTripThroughAPIServer(t *testing.T, spec v1alpha1.EphemeralRunnerSpec) v1alpha1.EphemeralRunnerSpec {
	t.Helper()
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	var out v1alpha1.EphemeralRunnerSpec
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestEphemeralRunnerSetActionableSpecChanged_EmptySliceRoundTrip is a
// regression test for a permanent drift loop.
//
// A user may legitimately write `env: []` (or an empty nodeSelector, tolerations,
// volumes, ...) in the AutoscalingRunnerSet template. Those fields carry
// `omitempty`, so when the controller writes the derived EphemeralRunnerSet the
// empty slice is dropped entirely and reads back as nil. If the drift check
// treats nil and empty as different it reports drift forever: every reconcile
// bumps ActionableRevision, which makes the EphemeralRunnerSet controller delete
// every idle and pending runner, permanently.
func TestEphemeralRunnerSetActionableSpecChanged_EmptySliceRoundTrip(t *testing.T) {
	desiredSpec := v1alpha1.EphemeralRunnerSpec{
		GitHubConfigURL:    "https://github.com/owner/repo",
		GitHubConfigSecret: "gh-config",
		RunnerScaleSetID:   42,
		PodTemplateSpec: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "runner",
					Image: "ghcr.io/actions/actions-runner:2.337.0",
					// Explicitly empty, exactly as a user writing `env: []` would produce.
					Env: []corev1.EnvVar{},
				}},
				NodeSelector: map[string]string{},
				Tolerations:  []corev1.Toleration{},
			},
		},
	}

	desired := &v1alpha1.EphemeralRunnerSet{
		Spec: v1alpha1.EphemeralRunnerSetSpec{EphemeralRunnerSpec: desiredSpec},
	}

	// The live object is what the API server hands back after the controller
	// persisted exactly this desired spec.
	current := &v1alpha1.EphemeralRunnerSet{
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			EphemeralRunnerSpec: roundTripThroughAPIServer(t, desiredSpec),
		},
	}

	// Sanity check that the round trip really does produce the nil/empty split,
	// otherwise this test would pass vacuously.
	require.NotNil(t, desired.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env)
	require.Nil(t, current.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env)

	assert.False(t,
		ephemeralRunnerSetActionableSpecChanged(current, desired),
		"an empty slice that was dropped by omitempty on write must not be reported as drift; "+
			"reporting drift here bumps ActionableRevision on every reconcile and deletes idle runners forever",
	)
}

// TestEphemeralRunnerSetActionableSpecChanged_RealChangeStillDetected guards the
// opposite direction: relaxing nil-vs-empty must not blind us to genuine drift.
func TestEphemeralRunnerSetActionableSpecChanged_RealChangeStillDetected(t *testing.T) {
	base := func() *v1alpha1.EphemeralRunnerSet {
		return &v1alpha1.EphemeralRunnerSet{
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:  "https://github.com/owner/repo",
					RunnerScaleSetID: 42,
					PodTemplateSpec: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  "runner",
								Image: "ghcr.io/actions/actions-runner:2.337.0",
								Env:   []corev1.EnvVar{{Name: "A", Value: "1"}},
							}},
						},
					},
				},
			},
		}
	}

	tests := map[string]func(*v1alpha1.EphemeralRunnerSet){
		"image changed": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image = "ghcr.io/actions/actions-runner:2.338.0"
		},
		"env value changed": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env[0].Value = "2"
		},
		"env var removed": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env = nil
		},
		"env var added": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env = append(
				e.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env,
				corev1.EnvVar{Name: "B", Value: "2"},
			)
		},
		"scale set id changed": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.RunnerScaleSetID = 43
		},
		"config url changed": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.GitHubConfigURL = "https://github.com/owner/other"
		},
		"container removed": func(e *v1alpha1.EphemeralRunnerSet) {
			e.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers = nil
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			current, desired := base(), base()
			mutate(desired)
			assert.True(t, ephemeralRunnerSetActionableSpecChanged(current, desired),
				"genuine drift must still be detected")
		})
	}

	t.Run("identical specs report no drift", func(t *testing.T) {
		assert.False(t, ephemeralRunnerSetActionableSpecChanged(base(), base()))
	})

	t.Run("nil handling", func(t *testing.T) {
		assert.False(t, ephemeralRunnerSetActionableSpecChanged(nil, nil))
		assert.True(t, ephemeralRunnerSetActionableSpecChanged(nil, base()))
		assert.True(t, ephemeralRunnerSetActionableSpecChanged(base(), nil))
	})
}
