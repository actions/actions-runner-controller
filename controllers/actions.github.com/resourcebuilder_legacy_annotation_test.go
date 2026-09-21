package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// legacyIntegrityHashAnnotation is the annotation this package used to stamp on
// the resources it builds. It is deliberately spelled out rather than
// referenced, because the constant it mirrors has been deleted; these tests
// pin the upgrade behaviour for objects that were created while it still
// existed.
const legacyIntegrityHashAnnotation = "actions.github.com/integrity-hash"

func newLegacyAnnotationTestAutoscalingRunnerSet() *v1alpha1.AutoscalingRunnerSet {
	return &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-scale-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelKeyKubernetesPartOf:  labelValueKubernetesPartOf,
				LabelKeyKubernetesVersion: "0.2.0",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "1",
				AnnotationKeyGitHubRunnerGroupName:    "test-group",
				AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/org/repo",
		},
	}
}

// TestLegacyIntegrityHashAnnotationCausesOneTimeListenerRecreation documents an
// upgrade consequence of no longer stamping the integrity hash.
//
// AutoscalingRunnerSetReconciler compares the live listener's annotations
// against the desired ones with cmp.Equal and deletes the listener when they
// differ. A listener created by an older controller carries the legacy
// annotation, the desired listener no longer does, so the first reconcile after
// an upgrade recreates it.
//
// This is a one-time rollout, not a reconcile loop: the replacement is built by
// the same code path and carries no annotation, so the next comparison matches.
// In practice the rollout is not additional either, since the listener runs the
// manager's own image, and a controller upgrade changes that image and so
// already forces the same recreation through the Spec comparison.
func TestLegacyIntegrityHashAnnotationCausesOneTimeListenerRecreation(t *testing.T) {
	autoscalingRunnerSet := newLegacyAnnotationTestAutoscalingRunnerSet()

	cache := NewResourceCache()
	b := ResourceBuilder{ResourceCache: &cache}

	ephemeralRunnerSet, err := b.newEphemeralRunnerSet(autoscalingRunnerSet)
	require.NoError(t, err)

	desired, err := b.newAutoscalingListener(autoscalingRunnerSet, ephemeralRunnerSet, "controller-ns", "test:latest", nil)
	require.NoError(t, err)
	assert.NotContains(t, desired.Annotations, legacyIntegrityHashAnnotation)

	// A listener as an older controller would have left it behind.
	live := desired.DeepCopy()
	live.Annotations = map[string]string{legacyIntegrityHashAnnotation: "some-stale-hash"}

	assert.False(
		t,
		cmp.Equal(live.Annotations, desired.Annotations),
		"a listener carrying the legacy annotation must not compare equal to the desired listener, otherwise it would never be replaced",
	)

	// The replacement converges, so the recreation happens once rather than on
	// every reconcile.
	replacement, err := b.newAutoscalingListener(autoscalingRunnerSet, ephemeralRunnerSet, "controller-ns", "test:latest", nil)
	require.NoError(t, err)
	assert.NotContains(t, replacement.Annotations, legacyIntegrityHashAnnotation)
	assert.True(
		t,
		cmp.Equal(replacement.Annotations, desired.Annotations),
		"the recreated listener must match the desired one, otherwise the controller would rebuild it forever",
	)
}

// TestLegacyIntegrityHashAnnotationRetainedOnExistingObjects pins the scope of
// this removal: it stops the annotation being written, it does not strip it
// from objects that already carry it.
//
// Every reconciler that updates an object it does not replace merges the live
// annotations under the desired ones, so a key that is only absent from the
// desired set survives. Nothing reads the annotation any more, so it is inert
// metadata rather than a behavioural leftover; removing it from existing
// objects would mean patching every managed object on upgrade and is a separate
// decision from this change.
func TestLegacyIntegrityHashAnnotationRetainedOnExistingObjects(t *testing.T) {
	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-scale-set",
			Namespace: "test-ns",
			Labels: map[string]string{
				LabelKeyGitHubScaleSetName:      "test-scale-set",
				LabelKeyGitHubScaleSetNamespace: "test-ns",
			},
		},
	}

	var b ResourceBuilder
	desired, err := b.newEphemeralRunnerSetProxySecret(ephemeralRunnerSet, map[string][]byte{
		"http_proxy": []byte("http://proxy.example.com"),
	})
	require.NoError(t, err)
	require.NotContains(t, desired.Annotations, legacyIntegrityHashAnnotation)

	// A proxy secret as an older controller would have left it behind.
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
			Annotations: map[string]string{
				legacyIntegrityHashAnnotation: "some-stale-hash",
				"example.com/user-annotation": "user-value",
			},
		},
	}

	merged := b.mergeAnnotations(live.Annotations, desired.Annotations)

	assert.Contains(t, merged, legacyIntegrityHashAnnotation,
		"the legacy annotation survives on existing objects; this change stops it being written, it does not migrate it away")
	assert.Equal(t, "user-value", merged["example.com/user-annotation"],
		"unrelated annotations must be preserved")
}
