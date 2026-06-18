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
	"errors"
	"testing"
	"time"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_NoOp benchmarks a no-op reconcile
// scenario where an existing EphemeralRunner with finalizers requires no changes
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_NoOp(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme and fake client
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create minimal EphemeralRunner with finalizers already added
	runner := NewMinimalEphemeralRunner("default", "test-runner")
	runner.Finalizers = []string{
		ephemeralRunnerFinalizerName,
		ephemeralRunnerActionsFinalizerName,
	}
	runner.Spec.GitHubConfigSecret = "config-secret"
	runner.Status.RunnerID = 12345
	runner.Status.RunnerName = "test-runner"

	// Create config secret
	configSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "config-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	}

	// Create JIT secret for the runner (already exists, so no JIT creation needed)
	jitSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runner",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"jitToken":   []byte("test-jit-token"),
			"runnerId":   []byte("12345"),
			"runnerName": []byte("test-runner"),
		},
	}

	// Create pod (already exists, so no pod creation needed)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runner",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "runner",
					Image: "ghcr.io/actions/runner:latest",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	k8sClient := clientfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runner, configSecret, jitSecret, pod).
		WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}, &corev1.Pod{}).
		Build()

	// Setup reconciler
	multiClient := NewFakeMultiClient()
	reconciler := &EphemeralRunnerReconciler{
		Client: k8sClient,
		Scheme: scheme,
		Log:    logr.Discard(),
		ResourceBuilder: ResourceBuilder{
			SecretResolver: secretresolver.New(k8sClient, multiClient),
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "test-runner",
		},
	}

	// Warmup iteration
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	// Benchmark loop

	for b.Loop() {
		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_FinalizerAdd benchmarks the
// path where finalizers are added to a new EphemeralRunner
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_FinalizerAdd(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	multiClient := fake.NewMultiClient(
		fake.WithClient(
			fake.NewClient(
				fake.WithGetRunner(nil, scaleset.RunnerNotFoundError),
				fake.WithGenerateJitRunnerConfig(
					&scaleset.RunnerScaleSetJitRunnerConfig{
						Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
						EncodedJITConfig: "fake-jit-config",
					},
					nil,
				),
			),
		),
	)

	// Warmup iteration
	{
		runner := NewMinimalEphemeralRunner("default", "warmup-runner")
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "warmup-runner",
			},
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}

	// Benchmark loop: Each iteration needs fresh objects since finalizers are added

	for b.Loop() {
		b.StopTimer()

		runner := NewMinimalEphemeralRunner("default", "test-runner")
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-runner",
			},
		}

		b.StartTimer()

		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_Deletion benchmarks the deletion
// path where finalizers are removed and resources are cleaned up
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_Deletion(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	multiClient := NewFakeMultiClient()

	// Warmup iteration
	{
		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "warmup-runner")
		runner.Finalizers = []string{ephemeralRunnerFinalizerName}
		runner.DeletionTimestamp = &now
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "warmup-runner",
			},
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}

	// Benchmark loop: Each iteration needs fresh objects

	for b.Loop() {
		b.StopTimer()

		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "test-runner")
		runner.Finalizers = []string{ephemeralRunnerFinalizerName}
		runner.DeletionTimestamp = &now
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-runner",
			},
		}

		b.StartTimer()

		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_DeletionWithActionsCleanup benchmarks
// the deletion path with actions finalizer requiring service cleanup
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_DeletionWithActionsCleanup(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Configure fake client to simulate successful cleanup
	multiClient := fake.NewMultiClient(
		fake.WithClient(
			fake.NewClient(
				fake.WithRemoveRunner(nil), // Successful cleanup
			),
		),
	)

	// Warmup iteration
	{
		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "warmup-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.DeletionTimestamp = &now
		runner.Status.RunnerID = 12345
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "warmup-runner",
			},
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}

	// Benchmark loop

	for b.Loop() {
		b.StopTimer()

		// Create EphemeralRunner marked for deletion with both finalizers
		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "test-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.DeletionTimestamp = &now
		runner.Status.RunnerID = 12345
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-runner",
			},
		}

		b.StartTimer()

		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_RetryableError benchmarks the
// path where a retryable error occurs during reconciliation
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_RetryableError(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Configure fake client to return transient error
	retryableErr := errors.New("transient API error")
	multiClient := fake.NewMultiClient(
		fake.WithClient(
			fake.NewClient(
				fake.WithRemoveRunner(retryableErr),
			),
		),
	)

	// Warmup iteration
	{
		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "warmup-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.DeletionTimestamp = &now
		runner.Status.RunnerID = 12345
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "warmup-runner",
			},
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}

	// Benchmark loop

	for b.Loop() {
		b.StopTimer()

		// Create EphemeralRunner that will trigger error during cleanup
		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "test-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.DeletionTimestamp = &now
		runner.Status.RunnerID = 12345
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-runner",
			},
		}

		b.StartTimer()

		result, err := reconciler.Reconcile(ctx, req)

		// Verify error is returned (expected behavior for retryable errors)
		if err == nil {
			b.Fatalf("expected error, got nil")
		}
		if result.RequeueAfter != 0 {
			b.Fatalf("expected no requeue delay on error, got %v", result.RequeueAfter)
		}
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_DonePhaseCleanup benchmarks
// the path where an EphemeralRunner in Done phase triggers resource cleanup
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_DonePhaseCleanup(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	multiClient := NewFakeMultiClient()

	// Warmup iteration
	{
		runner := NewMinimalEphemeralRunner("default", "warmup-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.Status.Phase = "Succeeded"
		runner.Spec.GitHubConfigSecret = "config-secret"

		// Create a pod that will be cleaned up
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "warmup-runner",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: actionsv1alpha1.GroupVersion.String(),
						Kind:       "EphemeralRunner",
						Name:       "warmup-runner",
						UID:        runner.UID,
					},
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "runner",
						Image: "ghcr.io/actions/runner:latest",
					},
				},
			},
		}

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, pod, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "warmup-runner",
			},
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}

	// Benchmark loop

	for b.Loop() {
		b.StopTimer()

		// Create EphemeralRunner in Done phase (Succeeded)
		runner := NewMinimalEphemeralRunner("default", "test-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.Status.Phase = "Succeeded"
		runner.Spec.GitHubConfigSecret = "config-secret"

		// Create a pod that will be cleaned up
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-runner",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: actionsv1alpha1.GroupVersion.String(),
						Kind:       "EphemeralRunner",
						Name:       "test-runner",
						UID:        runner.UID,
					},
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "runner",
						Image: "ghcr.io/actions/runner:latest",
					},
				},
			},
		}

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, pod, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-runner",
			},
		}

		b.StartTimer()

		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunner_RequeueAfter benchmarks the
// path where cleanup is not finished and a requeue is requested
func BenchmarkActionsGithub_Reconcile_EphemeralRunner_RequeueAfter(b *testing.B) {
	ctx := context.Background()

	// Setup: Create scheme
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	multiClient := fake.NewMultiClient(
		fake.WithClient(
			fake.NewClient(
				fake.WithRemoveRunner(scaleset.JobStillRunningError),
			),
		),
	)

	// Warmup iteration
	{
		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "warmup-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.DeletionTimestamp = &now
		runner.Status.RunnerID = 12345
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "warmup-runner",
			},
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}

	// Benchmark loop

	for b.Loop() {
		b.StopTimer()

		now := metav1.Now()
		runner := NewMinimalEphemeralRunner("default", "test-runner")
		runner.Finalizers = []string{
			ephemeralRunnerFinalizerName,
			ephemeralRunnerActionsFinalizerName,
		}
		runner.DeletionTimestamp = &now
		runner.Status.RunnerID = 12345
		runner.Spec.GitHubConfigSecret = "config-secret"

		configSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"github_token": []byte("test-token"),
			},
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(runner, configSecret).
			WithStatusSubresource(&actionsv1alpha1.EphemeralRunner{}).
			Build()

		reconciler := &EphemeralRunnerReconciler{
			Client: k8sClient,
			Scheme: scheme,
			Log:    logr.Discard(),
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: "default",
				Name:      "test-runner",
			},
		}

		b.StartTimer()

		result, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}

		if result.RequeueAfter != 30*time.Second {
			b.Fatalf("expected RequeueAfter=30s, got %v", result.RequeueAfter)
		}
	}
}
