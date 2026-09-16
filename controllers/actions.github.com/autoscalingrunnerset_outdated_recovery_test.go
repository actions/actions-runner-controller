/*
Copyright 2026 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actionsgithubcom

import (
	"context"
	"strconv"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
)

// outdatedFixture builds an AutoscalingRunnerSet whose EphemeralRunnerSet has
// reported that its runners rejected the runner spec, together with a client and
// a reconciler wired to them.
// publishedGeneration is the AutoscalingRunnerSet generation recorded on the
// EphemeralRunnerSet. Lagging behind the live generation is what a
// generation-based recovery signal reads as "retry the spec the runners
// rejected"; matching it is what such a signal reads as "never retry", even when
// the runner spec itself has changed.
func outdatedFixture(
	t *testing.T,
	runnerSetPhase v1alpha1.AutoscalingRunnerSetPhase,
	generation int64,
	publishedGeneration int64,
	desiredImage string,
) (*v1alpha1.AutoscalingRunnerSet, *AutoscalingRunnerSetReconciler, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	const (
		name      = "test"
		namespace = "test"
	)

	rejectedTemplate := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "runner", Image: "runner:rejected"}},
		},
	}
	desiredTemplate := *rejectedTemplate.DeepCopy()
	desiredTemplate.Spec.Containers[0].Image = desiredImage

	autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: generation,
			Finalizers: []string{autoscalingRunnerSetFinalizerName},
			Labels:     map[string]string{LabelKeyKubernetesVersion: build.Version},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "1",
				AnnotationKeyGitHubRunnerGroupName:    "group",
				AnnotationKeyGitHubRunnerScaleSetName: name,
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl: "https://github.com/owner/repo",
			Template:        desiredTemplate,
		},
		Status: v1alpha1.AutoscalingRunnerSetStatus{
			Phase:              runnerSetPhase,
			ObservedGeneration: generation - 1,
		},
	}
	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				AnnotationKeyAutoscalingRunnerSetGeneration: strconv.FormatInt(publishedGeneration, 10),
			},
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas:           3,
			PatchID:            7,
			ActionableRevision: outdatedFixtureRevision,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				RunnerScaleSetID: 1,
				GitHubConfigURL:  autoscalingRunnerSet.Spec.GitHubConfigUrl,
				PodTemplateSpec:  rejectedTemplate,
			},
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			Phase:                     v1alpha1.EphemeralRunnerSetPhaseOutdated,
			AppliedActionableRevision: outdatedFixtureRevision,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(autoscalingRunnerSet, ephemeralRunnerSet).
		WithObjects(autoscalingRunnerSet, ephemeralRunnerSet).
		Build()
	resourceCache := NewResourceCache()
	reconciler := &AutoscalingRunnerSetReconciler{
		Client:              c,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ControllerNamespace: namespace,
		ResourceBuilder: ResourceBuilder{
			ResourceCache: &resourceCache,
			Scheme:        scheme,
		},
	}

	return autoscalingRunnerSet, reconciler, c
}

const outdatedFixtureRevision = int64(1)

func reconcileOutdatedFixture(t *testing.T, reconciler *AutoscalingRunnerSetReconciler, key types.NamespacedName) {
	t.Helper()

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
}

// The set is switched off on the first reconcile that sees the rejection, even
// though the AutoscalingRunnerSet still has an unobserved generation. The
// generation says nothing about the spec the runners objected to.
func TestAutoscalingRunnerSetParksFirstRejection(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhasePending, 2, 1, "runner:rejected")
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	reconcileOutdatedFixture(t, reconciler, key)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotERS))
	require.Equal(t, outdatedFixtureRevision, gotERS.Spec.ActionableRevision)
	require.Zero(t, gotERS.Spec.Replicas)
	require.Zero(t, gotERS.Spec.PatchID)
}

// An already outdated scale set stays outdated while the runner spec is the one
// that was rejected, no matter how far its generation has moved on.
func TestAutoscalingRunnerSetStaysOutdatedWithoutRunnerSpecChange(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 4, "runner:rejected")
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	reconcileOutdatedFixture(t, reconciler, key)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotERS))
	require.Equal(t, outdatedFixtureRevision, gotERS.Spec.ActionableRevision, "the rejected runner spec must not be retried")

	// The fixture carries a nonzero target, which is what a listener that was
	// still draining when the set was parked would have left behind. Re-pinning
	// it is what makes that harmless: the AutoscalingRunnerSet owns the
	// EphemeralRunnerSet, so a target published while parked re-enqueues it and
	// is taken straight back to zero.
	require.Zero(t, gotERS.Spec.Replicas, "a target published while parked must be taken back to zero")
	require.Zero(t, gotERS.Spec.PatchID)
}

// Correcting the runner spec is the one edit that recovers the scale set: the
// phase leaves outdated and the new spec is published with a higher revision, so
// the EphemeralRunnerSet stops judging itself by the runners that failed.
func TestAutoscalingRunnerSetRecoversOnRunnerSpecChange(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 4, "runner:fixed")
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	reconcileOutdatedFixture(t, reconciler, key)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhasePending, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotERS))
	require.Equal(t, "runner:fixed", gotERS.Spec.EphemeralRunnerSpec.Spec.Containers[0].Image)
	require.Greater(t, gotERS.Spec.ActionableRevision, outdatedFixtureRevision, "the revision must advance so the failed runners are treated as stale")
}

// The mirror image of over-parking: the runner spec changed, but the generation
// recorded on the EphemeralRunnerSet is already current, which is what happens
// whenever the derived runner spec moves without metadata.generation moving with
// it. A generation-based signal refuses the recovery; comparing the spec does
// not.
func TestAutoscalingRunnerSetRecoversWithoutAnUnobservedGeneration(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 5, "runner:fixed")
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	reconcileOutdatedFixture(t, reconciler, key)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhasePending, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotERS))
	require.Equal(t, "runner:fixed", gotERS.Spec.EphemeralRunnerSpec.Spec.Containers[0].Image)
	require.Greater(t, gotERS.Spec.ActionableRevision, outdatedFixtureRevision)
}

// Runner metadata is published to the set the same way the runner spec is, and
// changes what the next runner looks like, so it recovers the scale set too. The
// revision must advance with it: without that the EphemeralRunnerSet would keep
// judging itself by the runners that failed and push the scale set straight back
// to outdated.
func TestAutoscalingRunnerSetRecoversOnRunnerMetadataChange(t *testing.T) {
	// Published generation deliberately current, so the recovery can only come
	// from the metadata comparison and not from a generation that lags.
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 5, "runner:rejected")
	autoscalingRunnerSet.Spec.EphemeralRunnerMetadata = &v1alpha1.ResourceMeta{
		Labels: map[string]string{"arc.test/runner": "corrected"},
	}
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)
	require.NoError(t, c.Update(context.Background(), autoscalingRunnerSet))

	reconcileOutdatedFixture(t, reconciler, key)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhasePending, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotERS))
	require.NotNil(t, gotERS.Spec.EphemeralRunnerMetadata)
	require.Equal(t, "corrected", gotERS.Spec.EphemeralRunnerMetadata.Labels["arc.test/runner"])
	require.Greater(t, gotERS.Spec.ActionableRevision, outdatedFixtureRevision, "the revision must advance so the failed runners are treated as stale")
}

// A parked scale set still accepts edits that are not a recovery signal, and
// reconcileOutdated returns before any of them reach the listener. Starting the
// listener again therefore has to reckon with a spec that has moved on since it
// was stopped: it is replaced, not simply switched back on, so it never runs for
// a moment under the configuration the scale set was parked with.
func TestAutoscalingRunnerSetReplacesAStoppedListenerWithADriftedSpec(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseRunning, 5, 5, "runner:rejected")
	ctx := context.Background()
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	// The edit the scale set took while it was parked. It is not a runner spec
	// change, so it never propagated to the listener.
	maxRunners := 20
	autoscalingRunnerSet.Spec.MaxRunners = &maxRunners
	autoscalingRunnerSet.Status.ObservedGeneration = autoscalingRunnerSet.Generation
	require.NoError(t, c.Update(ctx, autoscalingRunnerSet))
	require.NoError(t, c.Status().Update(ctx, autoscalingRunnerSet))

	// The set is settled on the current spec and no longer complaining, so this
	// reconcile has nothing to publish to it and reaches the listener.
	desiredERS, err := reconciler.newEphemeralRunnerSet(autoscalingRunnerSet)
	require.NoError(t, err)
	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(ctx, key, gotERS))
	gotERS.Spec = desiredERS.Spec
	gotERS.Labels = desiredERS.Labels
	gotERS.Annotations = desiredERS.Annotations
	require.NoError(t, c.Update(ctx, gotERS))
	require.NoError(t, c.Get(ctx, key, gotERS))
	gotERS.Status.Phase = v1alpha1.EphemeralRunnerSetPhaseRunning
	require.NoError(t, c.Status().Update(ctx, gotERS))

	parked := autoscalingRunnerSet.DeepCopy()
	parked.Spec.MaxRunners = nil
	listener, err := reconciler.newAutoscalingListener(parked, gotERS, reconciler.ControllerNamespace, "listener:image", nil)
	require.NoError(t, err)
	listener.Spec.Phase = v1alpha1.AutoscalingListenerPhaseStopped
	require.NoError(t, c.Create(ctx, listener))

	reconcileOutdatedFixture(t, reconciler, key)

	err = c.Get(ctx, client.ObjectKeyFromObject(listener), new(v1alpha1.AutoscalingListener))
	require.True(
		t,
		kerrors.IsNotFound(err),
		"a stopped listener whose spec has drifted must be replaced rather than started, so it is never running with the spec the scale set was parked with",
	)
}

// A parked scale set still accepts edits that are not a recovery signal. They
// must reach the objects they belong to: the scale set stays switched off, but
// switched off is not the same as frozen, and an edit that silently never lands
// would come back as a surprise whenever the set eventually recovers.
func TestAutoscalingRunnerSetPropagatesUnrelatedEditsWhileOutdated(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 4, "runner:rejected")
	ctx := context.Background()
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	// Neither edit touches the runner spec, so neither recovers the scale set.
	maxRunners := 20
	autoscalingRunnerSet.Spec.MaxRunners = &maxRunners
	autoscalingRunnerSet.Labels["arc.test/edited-while-parked"] = "yes"
	require.NoError(t, c.Update(ctx, autoscalingRunnerSet))

	listener, err := reconciler.newAutoscalingListener(
		autoscalingRunnerSet,
		&v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}},
		reconciler.ControllerNamespace,
		"listener:image",
		nil,
	)
	require.NoError(t, err)
	// The listener as it was when the scale set was parked: no max runners.
	listener.Spec.MaxRunners = 0
	listener.Spec.Phase = v1alpha1.AutoscalingListenerPhaseStopped
	require.NoError(t, c.Create(ctx, listener))

	// More than one reconcile, because the edits land on different objects and
	// the controller returns after each patch.
	for range 4 {
		reconcileOutdatedFixture(t, reconciler, key)
	}

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(ctx, key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, gotARS.Status.Phase, "an edit outside the runner spec must not un-park the scale set")

	gotListener := new(v1alpha1.AutoscalingListener)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(listener), gotListener))
	require.Equal(t, v1alpha1.AutoscalingListenerPhaseStopped, gotListener.Spec.Phase, "the listener must stay switched off")
	require.Equal(t, maxRunners, gotListener.Spec.MaxRunners, "the edit must reach the listener even though it is switched off")

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(ctx, key, gotERS))
	require.Equal(t, "yes", gotERS.Labels["arc.test/edited-while-parked"], "the edit must reach the ephemeral runner set even though it is switched off")
	require.Equal(t, outdatedFixtureRevision, gotERS.Spec.ActionableRevision, "the rejected runner spec must not be retried")
	require.Zero(t, gotERS.Spec.Replicas, "the set must stay pinned at zero replicas")
	require.Zero(t, gotERS.Spec.PatchID)
}

// propagateToStoppedListener runs immediately after stopListener has patched the
// phase, and reads the listener back through the cache. That read can still hold
// the pre-patch object, so the live phase is not a value this helper can trust:
// carrying it over would write Running back onto a listener the same reconcile
// has just switched off, while the scale set stays outdated.
//
// The helper only ever runs on the parked path, so the phase it writes is not in
// question. It is Stopped.
func TestPropagateToStoppedListenerNeverRestartsTheListener(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 4, "runner:rejected")
	ctx := context.Background()

	maxRunners := 20
	autoscalingRunnerSet.Spec.MaxRunners = &maxRunners
	require.NoError(t, c.Update(ctx, autoscalingRunnerSet))

	ephemeralRunnerSet := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(autoscalingRunnerSet), ephemeralRunnerSet))

	listener, err := reconciler.newAutoscalingListener(
		autoscalingRunnerSet,
		ephemeralRunnerSet,
		reconciler.ControllerNamespace,
		"listener:image",
		nil,
	)
	require.NoError(t, err)
	// The listener as a stale cached read returns it: still running, and still
	// carrying the spec it was parked with, so there is drift to propagate.
	listener.Spec.MaxRunners = 0
	listener.Spec.Phase = v1alpha1.AutoscalingListenerPhaseRunning
	require.NoError(t, c.Create(ctx, listener))

	require.NoError(t, reconciler.propagateToStoppedListener(ctx, autoscalingRunnerSet, ephemeralRunnerSet, logr.Discard()))

	got := new(v1alpha1.AutoscalingListener)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(listener), got))
	require.Equal(t, maxRunners, got.Spec.MaxRunners, "the edit should still be propagated")
	require.True(
		t,
		got.Spec.Phase.Stopped(),
		"propagating an edit must never restart a listener the same reconcile switched off, whatever phase the cached read reported",
	)
}

// A parked scale set now accepts edits, and the listener's name is a hash over
// the runner group and the config URL, so an edit to either renames the listener
// the controller looks for. Every lookup is by that derived name, so the running
// listener created under the old name is invisible to the parked path: it would
// keep acquiring jobs for a scale set that is supposed to be switched off.
func TestStopListenerSwitchesOffAListenerThatTheRunnerGroupRenamed(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, 4, "runner:rejected")
	ctx := context.Background()

	ephemeralRunnerSet := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(autoscalingRunnerSet), ephemeralRunnerSet))

	listener, err := reconciler.newAutoscalingListener(
		autoscalingRunnerSet,
		ephemeralRunnerSet,
		reconciler.ControllerNamespace,
		"listener:image",
		nil,
	)
	require.NoError(t, err)
	require.NoError(t, c.Create(ctx, listener))

	autoscalingRunnerSet.Spec.RunnerGroup = "moved-to-another-group"
	require.NoError(t, c.Update(ctx, autoscalingRunnerSet))
	require.NotEqual(
		t,
		listener.Name,
		scaleSetListenerName(autoscalingRunnerSet),
		"this test is only meaningful while the runner group renames the listener",
	)

	require.NoError(t, reconciler.stopListener(ctx, autoscalingRunnerSet, logr.Discard()))

	var listeners v1alpha1.AutoscalingListenerList
	require.NoError(t, c.List(ctx, &listeners, client.InNamespace(reconciler.ControllerNamespace)))
	for _, got := range listeners.Items {
		require.True(
			t,
			got.Spec.Phase.Stopped(),
			"listener %q kept acquiring jobs for a switched-off scale set",
			got.Name,
		)
	}
}
