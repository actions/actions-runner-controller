package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func intp(i int) *int { return &i }

// multiVariantARS returns an AutoscalingRunnerSet with two named variants and
// the annotations a reconciled multi-variant set carries (scalar id for the
// default plus the variant id map).
func multiVariantARS() *v1alpha1.AutoscalingRunnerSet {
	return &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ms",
			Namespace: "ns",
			Labels: map[string]string{
				LabelKeyKubernetesVersion: "0.14.2",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:        "10",
				AnnotationKeyGitHubRunnerScaleSetIDs: `{"big":12,"small":11}`,
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl:    "https://github.com/o/r",
			GitHubConfigSecret: "s",
			RunnerScaleSetName: "ms",
			MinRunners:         intp(0),
			MaxRunners:         intp(5),
			RunnerVariants: []v1alpha1.RunnerVariant{
				{Name: "small"},
				{Name: "big", MaxRunners: intp(20), Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "gpu:1"}}},
				}},
			},
		},
	}
}

func TestEphemeralRunnerSetNaming(t *testing.T) {
	ars := multiVariantARS()
	variants := ars.Spec.EffectiveVariants()
	require.Len(t, variants, 2)
	assert.Equal(t, "ms-small", ephemeralRunnerSetName(ars, variants[0]))
	assert.Equal(t, "ms-big", ephemeralRunnerSetName(ars, variants[1]))

	// The default (empty-name) variant keeps the classic ARS name.
	def := v1alpha1.EffectiveVariant{Name: ""}
	assert.Equal(t, "ms", ephemeralRunnerSetName(ars, def))
}

func TestVariantScaleSetIDResolution(t *testing.T) {
	ars := multiVariantARS()

	// default variant reads the scalar annotation
	id, err := variantScaleSetID(ars, v1alpha1.EffectiveVariant{Name: ""})
	require.NoError(t, err)
	assert.Equal(t, 10, id)

	// named variants read the map
	id, err = variantScaleSetID(ars, v1alpha1.EffectiveVariant{Name: "small"})
	require.NoError(t, err)
	assert.Equal(t, 11, id)
	id, err = variantScaleSetID(ars, v1alpha1.EffectiveVariant{Name: "big"})
	require.NoError(t, err)
	assert.Equal(t, 12, id)

	// unknown variant errors instead of silently registering the wrong scale set
	_, err = variantScaleSetID(ars, v1alpha1.EffectiveVariant{Name: "missing"})
	require.Error(t, err)
}

func TestRunnerScaleSetIDsRoundTrip(t *testing.T) {
	in := map[string]int{"a": 1, "b": 2}
	raw, err := encodeRunnerScaleSetIDs(in)
	require.NoError(t, err)
	out, err := decodeRunnerScaleSetIDs(raw)
	require.NoError(t, err)
	assert.Equal(t, in, out)

	empty, err := decodeRunnerScaleSetIDs("")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestShouldCreateScaleSetMultiVariant(t *testing.T) {
	// All ids present -> no registration needed.
	ars := multiVariantARS()
	assert.False(t, shouldCreateScaleSet(ars))

	// A newly added variant with no id in the map re-enters registration.
	ars.Spec.RunnerVariants = append(ars.Spec.RunnerVariants, v1alpha1.RunnerVariant{Name: "huge"})
	assert.True(t, shouldCreateScaleSet(ars))

	// A set with no variants uses the classic scalar check.
	classic := &v1alpha1.AutoscalingRunnerSet{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{runnerScaleSetIDAnnotationKey: "1"}}}
	assert.False(t, shouldCreateScaleSet(classic))
	classic.Annotations = map[string]string{}
	assert.True(t, shouldCreateScaleSet(classic))
}

func TestNewEphemeralRunnerSetForVariant(t *testing.T) {
	b := ResourceBuilder{}
	ars := multiVariantARS()
	variants := ars.Spec.EffectiveVariants()

	small, err := b.newEphemeralRunnerSetForVariant(ars, variants[0])
	require.NoError(t, err)
	assert.Equal(t, "ms-small", small.Name)
	assert.Equal(t, "small", small.Labels[LabelKeyGitHubRunnerVariant])
	assert.Equal(t, 11, small.Spec.EphemeralRunnerSpec.RunnerScaleSetID)

	big, err := b.newEphemeralRunnerSetForVariant(ars, variants[1])
	require.NoError(t, err)
	assert.Equal(t, "ms-big", big.Name)
	assert.Equal(t, "big", big.Labels[LabelKeyGitHubRunnerVariant])
	assert.Equal(t, 12, big.Spec.EphemeralRunnerSpec.RunnerScaleSetID)
	// big overrides the template
	require.NotEmpty(t, big.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers)
	assert.Equal(t, "gpu:1", big.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image)
}

func TestListenerScaleSetsAnnotationRoundTrip(t *testing.T) {
	tuples := []v1alpha1.ListenerScaleSet{
		{VariantName: "small", RunnerScaleSetID: 11, EphemeralRunnerSetName: "ms-small", MinRunners: 0, MaxRunners: 5},
		{VariantName: "big", RunnerScaleSetID: 12, EphemeralRunnerSetName: "ms-big", MinRunners: 0, MaxRunners: 20},
	}
	listener := &v1alpha1.AutoscalingListener{
		Spec: v1alpha1.AutoscalingListenerSpec{EphemeralRunnerSetName: "ms"},
	}
	require.NoError(t, stampListenerScaleSets(listener, tuples))

	got, err := decodeListenerScaleSets(listener.Annotations[AnnotationKeyGitHubListenerScaleSets])
	require.NoError(t, err)
	assert.Equal(t, tuples, got)

	// The Role scopes over every child ERS name for a multi-variant listener.
	assert.ElementsMatch(t, []string{"ms-small", "ms-big"}, listenerEphemeralRunnerSetNames(listener))
}

func TestStampListenerScaleSetsNoOpForSingle(t *testing.T) {
	listener := &v1alpha1.AutoscalingListener{
		Spec: v1alpha1.AutoscalingListenerSpec{EphemeralRunnerSetName: "solo"},
	}
	require.NoError(t, stampListenerScaleSets(listener, nil))
	// no annotation added -> single-variant listener stays byte-for-byte the same
	_, ok := listener.Annotations[AnnotationKeyGitHubListenerScaleSets]
	assert.False(t, ok)
	// Role scopes over exactly the one scalar ERS name.
	assert.Equal(t, []string{"solo"}, listenerEphemeralRunnerSetNames(listener))
}

func TestIsMultiVariant(t *testing.T) {
	// No variants -> classic single-set path (byte-for-byte, pinned by golden tests).
	assert.False(t, isMultiVariant(&v1alpha1.AutoscalingRunnerSet{}))
	// Any declared variant -> fan-out path, even a single named variant, so orphan
	// cleanup still runs when a set drops from many variants to one.
	assert.True(t, isMultiVariant(&v1alpha1.AutoscalingRunnerSet{Spec: v1alpha1.AutoscalingRunnerSetSpec{RunnerVariants: []v1alpha1.RunnerVariant{{Name: "only"}}}}))
	assert.True(t, isMultiVariant(multiVariantARS()))
}
