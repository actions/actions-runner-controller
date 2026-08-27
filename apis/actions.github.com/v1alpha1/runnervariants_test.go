package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func intPtr(i int) *int { return &i }

// With no runnerVariants the spec resolves to exactly one default variant with
// an empty name carrying the top level values, so every builder can loop over
// the result while the single-shape path stays unchanged.
func TestEffectiveVariantsDefault(t *testing.T) {
	spec := AutoscalingRunnerSetSpec{
		RunnerScaleSetLabels: []string{"self-hosted", "linux"},
		MinRunners:           intPtr(1),
		MaxRunners:           intPtr(5),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "top:1"}}},
		},
	}

	got := spec.EffectiveVariants()
	require.Len(t, got, 1)
	assert.Equal(t, "", got[0].Name)
	assert.Equal(t, []string{"self-hosted", "linux"}, got[0].RunnerScaleSetLabels)
	assert.Equal(t, intPtr(1), got[0].MinRunners)
	assert.Equal(t, intPtr(5), got[0].MaxRunners)
	assert.Equal(t, "top:1", got[0].Template.Spec.Containers[0].Image)
}

// Each variant inherits the top level values it does not override.
func TestEffectiveVariantsInheritance(t *testing.T) {
	spec := AutoscalingRunnerSetSpec{
		RunnerScaleSetLabels: []string{"base-label"},
		MinRunners:           intPtr(2),
		MaxRunners:           intPtr(8),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "top:1"}}},
		},
		RunnerVariants: []RunnerVariant{
			{
				// inherits everything from the top level
				Name: "small",
			},
			{
				// overrides labels, template and bounds
				Name:                 "big",
				RunnerScaleSetLabels: []string{"gpu"},
				MaxRunners:           intPtr(20),
				MinRunners:           intPtr(0),
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "gpu:1"}}},
				},
			},
		},
	}

	got := spec.EffectiveVariants()
	require.Len(t, got, 2)

	// small inherits
	assert.Equal(t, "small", got[0].Name)
	assert.Equal(t, []string{"base-label"}, got[0].RunnerScaleSetLabels)
	assert.Equal(t, intPtr(2), got[0].MinRunners)
	assert.Equal(t, intPtr(8), got[0].MaxRunners)
	assert.Equal(t, "top:1", got[0].Template.Spec.Containers[0].Image)

	// big overrides
	assert.Equal(t, "big", got[1].Name)
	assert.Equal(t, []string{"gpu"}, got[1].RunnerScaleSetLabels)
	assert.Equal(t, intPtr(0), got[1].MinRunners)
	assert.Equal(t, intPtr(20), got[1].MaxRunners)
	assert.Equal(t, "gpu:1", got[1].Template.Spec.Containers[0].Image)
}

// A variant that sets no labels falls back to the top level labels; a variant
// that sets an empty (non-nil) list gets an empty list, not the fallback.
func TestEffectiveVariantsLabelFallback(t *testing.T) {
	spec := AutoscalingRunnerSetSpec{
		RunnerScaleSetLabels: []string{"top"},
		RunnerVariants: []RunnerVariant{
			{Name: "inherit"},
			{Name: "explicit-empty", RunnerScaleSetLabels: []string{}},
		},
	}

	got := spec.EffectiveVariants()
	require.Len(t, got, 2)
	assert.Equal(t, []string{"top"}, got[0].RunnerScaleSetLabels, "nil variant labels should inherit top level labels")
	assert.Equal(t, []string{}, got[1].RunnerScaleSetLabels, "explicit empty variant labels should stay empty")
}
