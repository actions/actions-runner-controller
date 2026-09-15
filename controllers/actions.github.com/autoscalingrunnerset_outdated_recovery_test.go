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
func outdatedFixture(
	t *testing.T,
	runnerSetPhase v1alpha1.AutoscalingRunnerSetPhase,
	generation int64,
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
				AnnotationKeyAutoscalingRunnerSetGeneration: strconv.FormatInt(generation, 10),
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
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhasePending, 2, "runner:rejected")
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
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, "runner:rejected")
	key := client.ObjectKeyFromObject(autoscalingRunnerSet)

	reconcileOutdatedFixture(t, reconciler, key)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), key, gotERS))
	require.Equal(t, outdatedFixtureRevision, gotERS.Spec.ActionableRevision, "the rejected runner spec must not be retried")
}

// Correcting the runner spec is the one edit that recovers the scale set: the
// phase leaves outdated and the new spec is published with a higher revision, so
// the EphemeralRunnerSet stops judging itself by the runners that failed.
func TestAutoscalingRunnerSetRecoversOnRunnerSpecChange(t *testing.T) {
	autoscalingRunnerSet, reconciler, c := outdatedFixture(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, 5, "runner:fixed")
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
