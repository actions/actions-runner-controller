package actionsgithubcom

import (
	"encoding/json"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type listenerEmptyCollectionTestCase struct {
	name          string
	runnerSetSpec string
	collections   func(v1alpha1.AutoscalingListenerSpec) []any
}

func listenerEmptyCollectionTestCases() []listenerEmptyCollectionTestCase {
	return []listenerEmptyCollectionTestCase{
		{
			name:          "baseline",
			runnerSetSpec: `{}`,
			collections:   func(v1alpha1.AutoscalingListenerSpec) []any { return nil },
		},
		{
			name:          "metrics maps",
			runnerSetSpec: `{"listenerMetrics":{"counters":{},"gauges":{},"histograms":{}}}`,
			collections: func(s v1alpha1.AutoscalingListenerSpec) []any {
				return []any{s.Metrics.Counters, s.Metrics.Gauges, s.Metrics.Histograms}
			},
		},
		{
			name:          "template collections",
			runnerSetSpec: `{"listenerTemplate":{"spec":{"containers":[{"name":"listener","env":[]}],"nodeSelector":{},"tolerations":[]}}}`,
			collections: func(s v1alpha1.AutoscalingListenerSpec) []any {
				return []any{s.Template.Spec.Containers[0].Env, s.Template.Spec.NodeSelector, s.Template.Spec.Tolerations}
			},
		},
		{
			name:          "service account metadata",
			runnerSetSpec: `{"listenerServiceAccountMetadata":{"labels":{},"annotations":{}}}`,
			collections: func(s v1alpha1.AutoscalingListenerSpec) []any {
				return []any{s.ServiceAccountMetadata.Labels, s.ServiceAccountMetadata.Annotations}
			},
		},
		{
			name:          "role metadata",
			runnerSetSpec: `{"listenerRoleMetadata":{"labels":{},"annotations":{}}}`,
			collections: func(s v1alpha1.AutoscalingListenerSpec) []any {
				return []any{s.RoleMetadata.Labels, s.RoleMetadata.Annotations}
			},
		},
		{
			name:          "role binding metadata",
			runnerSetSpec: `{"listenerRoleBindingMetadata":{"labels":{},"annotations":{}}}`,
			collections: func(s v1alpha1.AutoscalingListenerSpec) []any {
				return []any{s.RoleBindingMetadata.Labels, s.RoleBindingMetadata.Annotations}
			},
		},
		{
			name:          "config secret metadata",
			runnerSetSpec: `{"listenerConfigSecretMetadata":{"labels":{},"annotations":{}}}`,
			collections: func(s v1alpha1.AutoscalingListenerSpec) []any {
				return []any{s.ConfigSecretMetadata.Labels, s.ConfigSecretMetadata.Annotations}
			},
		},
	}
}

func TestListenerSpecChanged_EmptyCollectionsRoundTrip(t *testing.T) {
	for _, tc := range listenerEmptyCollectionTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			runnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "runners",
					Namespace:   "runners",
					Annotations: map[string]string{runnerScaleSetIDAnnotationKey: "1"},
				},
			}
			require.NoError(t, json.Unmarshal([]byte(tc.runnerSetSpec), &runnerSet.Spec))
			runnerSet.Spec.GitHubConfigUrl = "https://github.com/owner/repo"
			builder := ResourceBuilder{ResourceCache: newTestResourceCache()}
			desired, err := builder.newAutoscalingListener(
				runnerSet, &v1alpha1.EphemeralRunnerSet{}, "controller", "listener:latest", nil,
			)
			require.NoError(t, err)

			raw, err := json.Marshal(desired)
			require.NoError(t, err)
			current := new(v1alpha1.AutoscalingListener)
			require.NoError(t, json.Unmarshal(raw, current))

			persistedCollections := tc.collections(current.Spec)
			for i, collection := range tc.collections(desired.Spec) {
				require.NotNil(t, collection, "the desired fixture must retain the explicit empty collection")
				require.Empty(t, collection)
				require.Nil(t, persistedCollections[i], "omitempty must drop the persisted collection")
			}

			currentBefore, desiredBefore := current.DeepCopy(), desired.DeepCopy()
			assert.False(t, listenerSpecChanged(current, desired),
				"serialization of empty collections must not cause continuous listener replacement")
			assert.False(t, listenerSpecChanged(desired, current), "comparison must be symmetric")
			assert.Equal(t, currentBefore, current, "comparison must not mutate the current listener")
			assert.Equal(t, desiredBefore, desired, "comparison must not mutate the desired listener")
		})
	}
}

func TestListenerSpecChanged_RealChangeStillDetected(t *testing.T) {
	base := &v1alpha1.AutoscalingListener{
		Spec: v1alpha1.AutoscalingListenerSpec{
			Image:      "listener:latest",
			MaxRunners: 10,
			Metrics: &v1alpha1.MetricsConfig{
				Counters: map[string]*v1alpha1.CounterMetric{
					"jobs_started": {Labels: []string{"repository"}},
				},
			},
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "listener",
						Env:  []corev1.EnvVar{{Name: "A", Value: "1"}},
					}},
					NodeSelector: map[string]string{"pool": "listeners"},
				},
			},
			ServiceAccountMetadata: &v1alpha1.ResourceMeta{Labels: map[string]string{"team": "arc"}},
			RoleMetadata:           &v1alpha1.ResourceMeta{Annotations: map[string]string{"team": "arc"}},
			RoleBindingMetadata:    &v1alpha1.ResourceMeta{Labels: map[string]string{"team": "arc"}},
			ConfigSecretMetadata:   &v1alpha1.ResourceMeta{Annotations: map[string]string{"team": "arc"}},
		},
	}
	tests := map[string]func(*v1alpha1.AutoscalingListenerSpec){
		"image changed":         func(s *v1alpha1.AutoscalingListenerSpec) { s.Image = "listener:updated" },
		"runner bounds changed": func(s *v1alpha1.AutoscalingListenerSpec) { s.MaxRunners++ },
		"metric added": func(s *v1alpha1.AutoscalingListenerSpec) {
			s.Metrics.Counters["jobs_completed"] = &v1alpha1.CounterMetric{Labels: []string{"repository"}}
		},
		"metric removed": func(s *v1alpha1.AutoscalingListenerSpec) { s.Metrics.Counters = nil },
		"metric labels changed": func(s *v1alpha1.AutoscalingListenerSpec) {
			s.Metrics.Counters["jobs_started"].Labels = []string{"organization"}
		},
		"env added": func(s *v1alpha1.AutoscalingListenerSpec) {
			s.Template.Spec.Containers[0].Env = append(s.Template.Spec.Containers[0].Env, corev1.EnvVar{Name: "B", Value: "2"})
		},
		"env removed":                    func(s *v1alpha1.AutoscalingListenerSpec) { s.Template.Spec.Containers[0].Env = []corev1.EnvVar{} },
		"env value changed":              func(s *v1alpha1.AutoscalingListenerSpec) { s.Template.Spec.Containers[0].Env[0].Value = "2" },
		"node selector added":            func(s *v1alpha1.AutoscalingListenerSpec) { s.Template.Spec.NodeSelector["zone"] = "east" },
		"node selector removed":          func(s *v1alpha1.AutoscalingListenerSpec) { s.Template.Spec.NodeSelector = map[string]string{} },
		"node selector changed":          func(s *v1alpha1.AutoscalingListenerSpec) { s.Template.Spec.NodeSelector["pool"] = "other" },
		"service account label added":    func(s *v1alpha1.AutoscalingListenerSpec) { s.ServiceAccountMetadata.Labels["app"] = "arc" },
		"service account label removed":  func(s *v1alpha1.AutoscalingListenerSpec) { s.ServiceAccountMetadata.Labels = nil },
		"service account label changed":  func(s *v1alpha1.AutoscalingListenerSpec) { s.ServiceAccountMetadata.Labels["team"] = "other" },
		"role annotation removed":        func(s *v1alpha1.AutoscalingListenerSpec) { s.RoleMetadata.Annotations = nil },
		"role binding label changed":     func(s *v1alpha1.AutoscalingListenerSpec) { s.RoleBindingMetadata.Labels["team"] = "other" },
		"config secret annotation added": func(s *v1alpha1.AutoscalingListenerSpec) { s.ConfigSecretMetadata.Annotations["app"] = "arc" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			current, desired := base.DeepCopy(), base.DeepCopy()
			mutate(&desired.Spec)
			currentBefore, desiredBefore := current.DeepCopy(), desired.DeepCopy()
			assert.True(t, listenerSpecChanged(current, desired), "genuine drift must still require replacement")
			assert.True(t, listenerSpecChanged(desired, current), "comparison must be symmetric")
			assert.Equal(t, currentBefore, current)
			assert.Equal(t, desiredBefore, desired)
		})
	}
}

func TestListenerSpecChanged_PhaseAndNilHandling(t *testing.T) {
	t.Run("phase only", func(t *testing.T) {
		phases := []v1alpha1.AutoscalingListenerPhase{
			"", v1alpha1.AutoscalingListenerPhaseRunning, v1alpha1.AutoscalingListenerPhaseStopped,
		}
		for _, currentPhase := range phases {
			for _, desiredPhase := range phases {
				current := &v1alpha1.AutoscalingListener{Spec: v1alpha1.AutoscalingListenerSpec{Phase: currentPhase}}
				desired := &v1alpha1.AutoscalingListener{Spec: v1alpha1.AutoscalingListenerSpec{Phase: desiredPhase}}
				assert.False(t, listenerSpecChanged(current, desired))
				assert.Equal(t, currentPhase, current.Spec.Phase)
				assert.Equal(t, desiredPhase, desired.Spec.Phase)
				desired.Spec.Image = "listener:updated"
				assert.True(t, listenerSpecChanged(current, desired), "phase exclusion must not hide config drift")
			}
		}
	})

	t.Run("nil listeners", func(t *testing.T) {
		listener := &v1alpha1.AutoscalingListener{}
		assert.False(t, listenerSpecChanged(nil, nil))
		assert.True(t, listenerSpecChanged(nil, listener))
		assert.True(t, listenerSpecChanged(listener, nil))
	})

	t.Run("nil metadata is not an empty metadata object", func(t *testing.T) {
		current := &v1alpha1.AutoscalingListener{}
		desired := &v1alpha1.AutoscalingListener{
			Spec: v1alpha1.AutoscalingListenerSpec{ServiceAccountMetadata: &v1alpha1.ResourceMeta{}},
		}
		raw, err := json.Marshal(desired)
		require.NoError(t, err)
		persisted := new(v1alpha1.AutoscalingListener)
		require.NoError(t, json.Unmarshal(raw, persisted))
		require.NotNil(t, persisted.Spec.ServiceAccountMetadata, "the enclosing metadata object survives serialization")
		assert.False(t, listenerSpecChanged(persisted, desired))
		assert.True(t, listenerSpecChanged(current, persisted))
		assert.True(t, listenerSpecChanged(persisted, current))
	})
}
