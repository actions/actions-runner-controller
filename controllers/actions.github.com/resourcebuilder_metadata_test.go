package actionsgithubcom

import (
	"maps"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMergeAnnotations(t *testing.T) {
	tests := []struct {
		name      string
		base      map[string]string
		overwrite map[string]string
		want      map[string]string
	}{
		{name: "both nil"},
		{name: "nil base and empty overwrite", overwrite: map[string]string{}, want: map[string]string{}},
		{name: "empty base and nil overwrite", base: map[string]string{}, want: map[string]string{}},
		{name: "both empty", base: map[string]string{}, overwrite: map[string]string{}, want: map[string]string{}},
		{
			name: "nil base", overwrite: map[string]string{"generated": "value"},
			want: map[string]string{"generated": "value"},
		},
		{
			name: "empty base", base: map[string]string{}, overwrite: map[string]string{"generated": "value"},
			want: map[string]string{"generated": "value"},
		},
		{
			name: "nil overwrite", base: map[string]string{"custom": "value"},
			want: map[string]string{"custom": "value"},
		},
		{
			name: "empty overwrite", base: map[string]string{"custom": "value"}, overwrite: map[string]string{},
			want: map[string]string{"custom": "value"},
		},
		{
			name:      "overwrite takes precedence",
			base:      map[string]string{"custom": "value", "reserved": "user"},
			overwrite: map[string]string{"generated": "value", "reserved": "controller"},
			want:      map[string]string{"custom": "value", "generated": "value", "reserved": "controller"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, overwrite := maps.Clone(tt.base), maps.Clone(tt.overwrite)
			var b ResourceBuilder
			var merged map[string]string
			require.NotPanics(t, func() { merged = b.mergeAnnotations(tt.base, tt.overwrite) })
			require.Equal(t, tt.want, merged)
			assert.Equal(t, base, tt.base)
			assert.Equal(t, overwrite, tt.overwrite)

			for key := range merged {
				merged[key] = "changed"
			}
			if merged != nil {
				merged["new"] = "value"
			}
			assert.Equal(t, base, tt.base, "the result must not alias the base")
			assert.Equal(t, overwrite, tt.overwrite, "the result must not alias the overwrite")
		})
	}
}

func TestMetadataPropagationWithoutAnnotations(t *testing.T) {
	for _, tt := range []struct {
		name        string
		annotations map[string]string
	}{
		{name: "omitted annotations"},
		{name: "empty annotations", annotations: map[string]string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			metadata := &v1alpha1.ResourceMeta{
				Labels:      map[string]string{"example.com/custom": "value"},
				Annotations: tt.annotations,
			}
			ars := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-set", Namespace: "test-ns", Generation: 7,
					Annotations: map[string]string{
						runnerScaleSetIDAnnotationKey:         "1",
						AnnotationKeyGitHubRunnerGroupName:    "test-group",
						AnnotationKeyGitHubRunnerScaleSetName: "test-set",
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:                     "https://github.com/org/repo",
					AutoscalingListenerMetadata:         metadata.DeepCopy(),
					ListenerServiceAccountMetadata:      metadata.DeepCopy(),
					ListenerRoleMetadata:                metadata.DeepCopy(),
					ListenerRoleBindingMetadata:         metadata.DeepCopy(),
					ListenerConfigSecretMetadata:        metadata.DeepCopy(),
					EphemeralRunnerSetMetadata:          metadata.DeepCopy(),
					EphemeralRunnerMetadata:             metadata.DeepCopy(),
					EphemeralRunnerConfigSecretMetadata: metadata.DeepCopy(),
					Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner", Image: "runner:latest"}},
					}},
				},
			}
			original := ars.DeepCopy()
			b := ResourceBuilder{ResourceCache: newTestResourceCache()}
			var ers *v1alpha1.EphemeralRunnerSet
			require.NotPanics(t, func() {
				var err error
				ers, err = b.newEphemeralRunnerSet(ars)
				require.NoError(t, err)
			})
			listener, err := b.newAutoscalingListener(ars, ers, ars.Namespace, "listener:latest", nil)
			require.NoError(t, err)
			sa, err := b.newScaleSetListenerServiceAccount(listener)
			require.NoError(t, err)
			role := b.newScaleSetListenerRole(listener)
			binding := b.newScaleSetListenerRoleBinding(listener, role, sa)
			config, err := b.newScaleSetListenerConfig(listener, &appconfig.AppConfig{Token: "test-token"}, nil, "")
			require.NoError(t, err)
			listenerPod, err := b.newScaleSetListenerPod(listener, config, sa, role, binding, nil)
			require.NoError(t, err)
			ers.Spec.PatchID = 3
			ers.Spec.ActionableRevision = 2
			runner, err := b.newEphemeralRunner(ers)
			require.NoError(t, err)
			runner.Name = "test-runner"
			jit, err := b.newEphemeralRunnerJitSecret(runner, &scaleset.RunnerScaleSetJitRunnerConfig{
				Runner: &scaleset.RunnerReference{ID: 1, Name: runner.Name, RunnerScaleSetID: 1},
			})
			require.NoError(t, err)
			runnerPod, err := b.newEphemeralRunnerPod(runner, jit)
			require.NoError(t, err)

			for _, obj := range []client.Object{ers, listener, sa, role, binding, config, listenerPod, runner, jit, runnerPod} {
				assert.Equal(t, "value", obj.GetLabels()["example.com/custom"], "%T labels", obj)
				assert.NotContains(t, obj.GetAnnotations(), "example.com/custom", "%T annotations", obj)
			}
			assert.Equal(t, "7", ers.Annotations[AnnotationKeyAutoscalingRunnerSetGeneration])
			assert.Equal(t, "test-group", ers.Annotations[AnnotationKeyGitHubRunnerGroupName])
			assert.Equal(t, "test-set", ers.Annotations[AnnotationKeyGitHubRunnerScaleSetName])
			assert.Equal(t, "3", runner.Annotations[AnnotationKeyPatchID])
			assert.Equal(t, "2", runner.Annotations[AnnotationKeyActionableRevision])
			assert.Equal(t, original, ars, "building resources must not mutate the input metadata")
		})
	}
}
