/*
Copyright 2020 The actions-runner-controller authors.

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
	"testing"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_AddFinalizer benchmarks the reconcile loop
// when adding a finalizer to a new AutoscalingRunnerSet
func BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_AddFinalizer(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create fake secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create minimal AutoscalingRunnerSet without finalizer
	ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
	ars.Spec.GitHubConfigSecret = "test-secret"
	ars.Labels = map[string]string{
		LabelKeyKubernetesVersion: build.Version,
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, ars).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingRunnerSet{}).
		Build()

	reconciler := &AutoscalingRunnerSetReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ControllerNamespace: "test-ns",
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, NewFakeMultiClient()),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "test-ars",
		},
	}

	for b.Loop() {
		b.StopTimer()
		// Reset the AutoscalingRunnerSet to initial state (no finalizer)
		ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
		ars.Spec.GitHubConfigSecret = "test-secret"
		ars.Labels = map[string]string{
			LabelKeyKubernetesVersion: build.Version,
		}
		ars.Annotations = map[string]string{
			AnnotationKeyIntegrityHash: autoscalingRunnerSetIntegrityHash(ars),
		}
		ResetBenchmarkAutoscalingRunnerSet(b, ctx, fakeClient, ars)
		b.StartTimer()

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_RemoveFinalizer benchmarks the reconcile loop
// when removing a finalizer during deletion
func BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_RemoveFinalizer(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create fake secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create AutoscalingRunnerSet with finalizer and deletion timestamp
	ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
	ars.Spec.GitHubConfigSecret = "test-secret"
	ars.Labels = map[string]string{
		LabelKeyKubernetesVersion: build.Version,
	}
	ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
	now := metav1.Now()
	ars.DeletionTimestamp = &now

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, ars).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingRunnerSet{}).
		Build()

	reconciler := &AutoscalingRunnerSetReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ControllerNamespace: "test-ns",
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, NewFakeMultiClient()),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "test-ars",
		},
	}

	for b.Loop() {
		b.StopTimer()
		// Reset the AutoscalingRunnerSet with finalizer
		ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
		ars.Spec.GitHubConfigSecret = "test-secret"
		ars.Labels = map[string]string{
			LabelKeyKubernetesVersion: build.Version,
		}
		ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
		now := metav1.Now()
		ars.DeletionTimestamp = &now
		_ = fakeClient.Update(ctx, ars)
		b.StartTimer()

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_CreateScaleSet benchmarks the reconcile loop
// when creating a runner scale set
func BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_CreateScaleSet(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create fake secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create AutoscalingRunnerSet with finalizer but no scale set annotations
	ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
	ars.Spec.GitHubConfigSecret = "test-secret"
	ars.Labels = map[string]string{
		LabelKeyKubernetesVersion: build.Version,
	}
	ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
	ars.Spec.RunnerGroup = "default"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, ars).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingRunnerSet{}).
		Build()

	// Create fake scale client with runner group and scale set creation support
	fakeScaleClient := scalefake.NewMultiClient(
		scalefake.WithClient(
			scalefake.NewClient(
				scalefake.WithGetRunnerGroupByNameFunc(func(ctx context.Context, groupName string) (*scaleset.RunnerGroup, error) {
					return &scaleset.RunnerGroup{ID: 1, Name: groupName}, nil
				}),
				scalefake.WithCreateRunnerScaleSetFunc(func(ctx context.Context, rs *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
					return &scaleset.RunnerScaleSet{
						ID:              1,
						Name:            rs.Name,
						RunnerGroupID:   rs.RunnerGroupID,
						RunnerGroupName: "default",
					}, nil
				}),
				scalefake.WithGetRunnerScaleSet(nil, nil),
			),
		),
	)

	reconciler := &AutoscalingRunnerSetReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ControllerNamespace: "test-ns",
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, fakeScaleClient),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "test-ars",
		},
	}

	for b.Loop() {
		b.StopTimer()
		// Reset the AutoscalingRunnerSet to state before scale set creation
		ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
		ars.Spec.GitHubConfigSecret = "test-secret"
		ars.Labels = map[string]string{
			LabelKeyKubernetesVersion: build.Version,
		}
		ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
		ars.Spec.RunnerGroup = "default"
		// Remove scale set annotations to trigger creation
		ars.Annotations = nil
		ResetBenchmarkAutoscalingRunnerSet(b, ctx, fakeClient, ars)
		b.StartTimer()

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_OutdatedCleanup benchmarks the reconcile loop
// when cleaning up outdated resources
func BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_OutdatedCleanup(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create fake secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create AutoscalingRunnerSet in outdated phase
	ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
	ars.Spec.GitHubConfigSecret = "test-secret"
	ars.Labels = map[string]string{
		LabelKeyKubernetesVersion: build.Version,
	}
	ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
	ars.Status.Phase = actionsv1alpha1.AutoscalingRunnerSetPhaseOutdated

	// Create AutoscalingListener to be cleaned up
	listener := NewMinimalAutoscalingListener("test-ns", "test-ars-listener")
	listener.Spec.AutoscalingRunnerSetNamespace = "test-ns"
	listener.Spec.AutoscalingRunnerSetName = "test-ars"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, ars, listener).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingRunnerSet{}, &actionsv1alpha1.AutoscalingListener{}).
		Build()

	reconciler := &AutoscalingRunnerSetReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ControllerNamespace: "test-ns",
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, NewFakeMultiClient()),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "test-ars",
		},
	}

	for b.Loop() {
		b.StopTimer()
		// Reset the AutoscalingRunnerSet to outdated state
		ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
		ars.Spec.GitHubConfigSecret = "test-secret"
		ars.Labels = map[string]string{
			LabelKeyKubernetesVersion: build.Version,
		}
		ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
		ars.Status.Phase = actionsv1alpha1.AutoscalingRunnerSetPhaseOutdated
		ResetBenchmarkAutoscalingRunnerSet(b, ctx, fakeClient, ars)

		// Recreate listener
		listener := NewMinimalAutoscalingListener("test-ns", "test-ars-listener")
		listener.Spec.AutoscalingRunnerSetNamespace = "test-ns"
		listener.Spec.AutoscalingRunnerSetName = "test-ars"
		_ = fakeClient.Create(ctx, listener)
		b.StartTimer()

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_CreateEphemeralRunnerSet benchmarks the reconcile loop
// when creating an EphemeralRunnerSet
func BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_CreateEphemeralRunnerSet(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create fake secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create AutoscalingRunnerSet with finalizer and scale set ID but no EphemeralRunnerSet
	ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
	ars.Spec.GitHubConfigSecret = "test-secret"
	ars.Labels = map[string]string{
		LabelKeyKubernetesVersion: build.Version,
	}
	ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
	ars.Annotations = map[string]string{
		runnerScaleSetIDAnnotationKey:         "1",
		AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
		AnnotationKeyGitHubRunnerGroupName:    "default",
		AnnotationKeyIntegrityHash:            "test-hash",
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, ars).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingRunnerSet{}, &actionsv1alpha1.EphemeralRunnerSet{}).
		Build()

	reconciler := &AutoscalingRunnerSetReconciler{
		Client:              fakeClient,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ControllerNamespace: "test-ns",
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, NewFakeMultiClient()),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "test-ars",
		},
	}

	for b.Loop() {
		b.StopTimer()
		// Delete any existing EphemeralRunnerSet
		ersList := &actionsv1alpha1.EphemeralRunnerSetList{}
		_ = fakeClient.List(ctx, ersList, client.InNamespace("test-ns"))
		for _, ers := range ersList.Items {
			_ = fakeClient.Delete(ctx, &ers)
		}

		// Reset AutoscalingRunnerSet
		ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
		ars.Spec.GitHubConfigSecret = "test-secret"
		ars.Labels = map[string]string{
			LabelKeyKubernetesVersion: build.Version,
		}
		ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
		ars.Annotations = map[string]string{
			runnerScaleSetIDAnnotationKey:         "1",
			AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
			AnnotationKeyGitHubRunnerGroupName:    "default",
			AnnotationKeyIntegrityHash:            "test-hash",
		}
		ResetBenchmarkAutoscalingRunnerSet(b, ctx, fakeClient, ars)
		b.StartTimer()

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_CreateListener benchmarks the reconcile loop
// when creating an AutoscalingListener
func BenchmarkActionsGithub_Reconcile_AutoscalingRunnerSet_CreateListener(b *testing.B) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create fake secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create AutoscalingRunnerSet with finalizer and scale set ID
	ars := NewMinimalAutoscalingRunnerSet("test-ns", "test-ars")
	ars.Spec.GitHubConfigSecret = "test-secret"
	ars.Labels = map[string]string{
		LabelKeyKubernetesVersion: build.Version,
	}
	ars.Finalizers = []string{autoscalingRunnerSetFinalizerName}
	ars.Annotations = map[string]string{
		runnerScaleSetIDAnnotationKey:         "1",
		AnnotationKeyGitHubRunnerScaleSetName: "test-scale-set",
		AnnotationKeyGitHubRunnerGroupName:    "default",
		AnnotationKeyIntegrityHash:            "test-hash",
	}

	// Create EphemeralRunnerSet
	ers := NewMinimalEphemeralRunnerSet("test-ns", "test-ars")
	ers.Annotations = map[string]string{
		AnnotationKeyIntegrityHash: "test-hash",
	}
	ers.Spec.Replicas = 1

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, ars, ers).
		WithStatusSubresource(
			&actionsv1alpha1.AutoscalingRunnerSet{},
			&actionsv1alpha1.EphemeralRunnerSet{},
			&actionsv1alpha1.AutoscalingListener{},
		).
		Build()

	reconciler := &AutoscalingRunnerSetReconciler{
		Client:                             fakeClient,
		Scheme:                             scheme,
		Log:                                logr.Discard(),
		ControllerNamespace:                "test-ns",
		DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc:latest",
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, NewFakeMultiClient()),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "test-ars",
		},
	}

	for b.Loop() {
		b.StopTimer()
		// Delete any existing AutoscalingListener
		listenerList := &actionsv1alpha1.AutoscalingListenerList{}
		_ = fakeClient.List(ctx, listenerList, client.InNamespace("test-ns"))
		for _, listener := range listenerList.Items {
			_ = fakeClient.Delete(ctx, &listener)
		}
		b.StartTimer()

		_, _ = reconciler.Reconcile(ctx, req)
	}
}
