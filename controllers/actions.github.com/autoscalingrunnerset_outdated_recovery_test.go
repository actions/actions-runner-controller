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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
)

func TestAutoscalingRunnerSetParksFirstRejectionOfPublishedGeneration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	const (
		name       = "test"
		namespace  = "test"
		generation = int64(2)
		revision   = int64(1)
	)
	template := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "runner", Image: "runner:new"}},
		},
	}
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
			Template:        template,
		},
		Status: v1alpha1.AutoscalingRunnerSetStatus{
			Phase:              v1alpha1.AutoscalingRunnerSetPhasePending,
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
			ActionableRevision: revision,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				RunnerScaleSetID: 1,
				GitHubConfigURL:  autoscalingRunnerSet.Spec.GitHubConfigUrl,
				PodTemplateSpec:  template,
			},
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			Phase:                     v1alpha1.EphemeralRunnerSetPhaseOutdated,
			AppliedActionableRevision: revision,
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

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
	})
	require.NoError(t, err)

	gotARS := new(v1alpha1.AutoscalingRunnerSet)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, gotARS))
	require.Equal(t, v1alpha1.AutoscalingRunnerSetPhaseOutdated, gotARS.Status.Phase)

	gotERS := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, gotERS))
	require.Equal(t, revision, gotERS.Spec.ActionableRevision)
	require.Zero(t, gotERS.Spec.Replicas)
	require.Zero(t, gotERS.Spec.PatchID)
}
