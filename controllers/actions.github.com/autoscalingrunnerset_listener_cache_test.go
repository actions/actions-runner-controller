package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// newAutoscalingListener serves a shared pointer out of the resource cache, so a
// caller that stamps the stopped phase onto what it gets back stamps it onto
// every later caller's desired listener too. That would eventually create a
// listener that is born stopped and never starts.
func TestCreatingAStoppedListenerDoesNotPoisonTheResourceCache(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 4, "runner:rejected")
	ctx := context.Background()

	ephemeralRunnerSet := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(autoscalingRunnerSet), ephemeralRunnerSet))
	// The cache is keyed on the UID, and the fake client does not assign one.
	autoscalingRunnerSet.UID = "ars-uid"

	first, err := reconciler.newAutoscalingListener(
		autoscalingRunnerSet,
		ephemeralRunnerSet,
		reconciler.ControllerNamespace,
		reconciler.DefaultRunnerScaleSetListenerImage,
		reconciler.listenerImagePullSecrets(),
	)
	require.NoError(t, err)
	cached, hit := reconciler.ResourceCache.autoscalingListener.Get(
		autoscalingRunnerSet,
		first,
		ephemeralRunnerSet,
		resourceCacheInputObject("autoscaling-listener-inputs", struct {
			Namespace        string
			Image            string
			ImagePullSecrets []corev1.LocalObjectReference
		}{
			Namespace:        reconciler.ControllerNamespace,
			Image:            reconciler.DefaultRunnerScaleSetListenerImage,
			ImagePullSecrets: reconciler.listenerImagePullSecrets(),
		}),
	)
	require.True(t, hit, "this test is only meaningful when the cache is actually serving the listener")
	require.Same(t, first, cached, "the cache hands out a shared pointer")

	require.NoError(t, reconciler.createStoppedListener(ctx, autoscalingRunnerSet, ephemeralRunnerSet, logr.Discard()))

	again, err := reconciler.newAutoscalingListener(
		autoscalingRunnerSet,
		ephemeralRunnerSet,
		reconciler.ControllerNamespace,
		reconciler.DefaultRunnerScaleSetListenerImage,
		reconciler.listenerImagePullSecrets(),
	)
	require.NoError(t, err)
	require.False(
		t,
		again.Spec.Phase.Stopped(),
		"the desired listener every other caller derives must not have been switched off",
	)
}
