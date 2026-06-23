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
	"fmt"
	"testing"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	clientgotesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_NoOp benchmarks the steady-state
// no-op reconciliation path where the EphemeralRunnerSet already has finalizer,
// correct spec hash, and the desired number of runners
func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_NoOp(b *testing.B) {
	testCases := []struct {
		name     string
		replicas int
	}{
		{name: "small_3_replicas", replicas: 3},
		{name: "medium_10_replicas", replicas: 10},
		{name: "large_50_replicas", replicas: 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			ers := NewMinimalEphemeralRunnerSet("default", "test-ers")
			ers.Spec.Replicas = tc.replicas
			controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)

			integrityHash := ephemeralRunnerSetIntegrityHash(ers)
			if ers.Annotations == nil {
				ers.Annotations = make(map[string]string)
			}
			ers.Annotations[AnnotationKeyIntegrityHash] = integrityHash

			ers.Status.CurrentReplicas = tc.replicas
			ers.Status.Phase = actionsv1alpha1.EphemeralRunnerSetPhaseRunning

			runners := make([]client.Object, 0, tc.replicas+1)
			runners = append(runners, ers)

			for i := 0; i < tc.replicas; i++ {
				runner := NewMinimalEphemeralRunner("default", fmt.Sprintf("test-runner-%d", i))
				runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseRunning
				runner.Status.RunnerID = i + 100
				runner.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: actionsv1alpha1.GroupVersion.String(),
						Kind:       "EphemeralRunnerSet",
						Name:       ers.Name,
						UID:        ers.UID,
						Controller: func() *bool { t := true; return &t }(),
					},
				}
				runners = append(runners, runner)
			}

			fakeClient := newLegacyFakeClientBuilder(scheme).
				WithObjects(runners...).
				WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
				WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
				Build()

			reconciler := &EphemeralRunnerSetReconciler{
				Client:          fakeClient,
				Scheme:          scheme,
				Log:             logr.Discard(),
				ResourceBuilder: &ResourceBuilder{Scheme: scheme},
			}

			ctx := context.Background()
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "test-ers",
				},
			}

			WarmupIteration(b, func() {
				_, _ = reconciler.Reconcile(ctx, req)
			})

			b.ResetTimer()
			for b.Loop() {
				_, _ = reconciler.Reconcile(ctx, req)
			}
		})
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_ScaleUp benchmarks the scale-up
// reconciliation path where new EphemeralRunners need to be created
func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_ScaleUp(b *testing.B) {
	testCases := []struct {
		name      string
		current   int
		desired   int
		scaleUpBy int
	}{
		{name: "small_scale_0_to_3", current: 0, desired: 3, scaleUpBy: 3},
		{name: "medium_scale_5_to_15", current: 5, desired: 15, scaleUpBy: 10},
		{name: "large_scale_10_to_60", current: 10, desired: 60, scaleUpBy: 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()

				ers := NewMinimalEphemeralRunnerSet("default", "test-ers")
				ers.Spec.Replicas = tc.desired
				controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)

				integrityHash := ephemeralRunnerSetIntegrityHash(ers)
				if ers.Annotations == nil {
					ers.Annotations = make(map[string]string)
				}
				ers.Annotations[AnnotationKeyIntegrityHash] = integrityHash

				ers.Status.CurrentReplicas = tc.current
				ers.Status.Phase = actionsv1alpha1.EphemeralRunnerSetPhaseRunning

				runners := make([]client.Object, 0, tc.current+1)
				runners = append(runners, ers)

				for j := 0; j < tc.current; j++ {
					runner := NewMinimalEphemeralRunner("default", fmt.Sprintf("test-runner-%d", j))
					runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseRunning
					runner.Status.RunnerID = j + 100
					runner.OwnerReferences = []metav1.OwnerReference{
						{
							APIVersion: actionsv1alpha1.GroupVersion.String(),
							Kind:       "EphemeralRunnerSet",
							Name:       ers.Name,
							UID:        ers.UID,
							Controller: func() *bool { t := true; return &t }(),
						},
					}
					runners = append(runners, runner)
				}

				fakeClient := newLegacyFakeClientBuilder(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := &EphemeralRunnerSetReconciler{
					Client:          fakeClient,
					Scheme:          scheme,
					Log:             logr.Discard(),
					ResourceBuilder: &ResourceBuilder{Scheme: scheme},
				}

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				_, _ = reconciler.Reconcile(ctx, req)
			}
		})
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_FinalizerAdd benchmarks the
// reconciliation path where the finalizer needs to be added to a new EphemeralRunnerSet
func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_FinalizerAdd(b *testing.B) {
	testCases := []struct {
		name     string
		replicas int
	}{
		{name: "small_3_replicas", replicas: 3},
		{name: "medium_10_replicas", replicas: 10},
		{name: "large_50_replicas", replicas: 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()

				ers := NewMinimalEphemeralRunnerSet("default", "test-ers")
				ers.Spec.Replicas = tc.replicas

				fakeClient := newLegacyFakeClientBuilder(scheme).
					WithObjects(ers).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := &EphemeralRunnerSetReconciler{
					Client:          fakeClient,
					Scheme:          scheme,
					Log:             logr.Discard(),
					ResourceBuilder: &ResourceBuilder{Scheme: scheme},
				}

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				_, _ = reconciler.Reconcile(ctx, req)
			}
		})
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_HashChangeDetection benchmarks the
// reconciliation path that detects a spec hash change (but before cleanup begins)
func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_HashChangeDetection(b *testing.B) {
	testCases := []struct {
		name     string
		replicas int
	}{
		{name: "small_5_replicas", replicas: 5},
		{name: "medium_20_replicas", replicas: 20},
		{name: "large_100_replicas", replicas: 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()

				ers := NewMinimalEphemeralRunnerSet("default", "test-ers")
				ers.Spec.Replicas = tc.replicas
				controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)

				if ers.Annotations == nil {
					ers.Annotations = make(map[string]string)
				}
				ers.Annotations[AnnotationKeyIntegrityHash] = "outdated-hash-12345"

				ers.Status.CurrentReplicas = tc.replicas
				ers.Status.Phase = actionsv1alpha1.EphemeralRunnerSetPhaseRunning

				runners := make([]client.Object, 0, tc.replicas+1)
				runners = append(runners, ers)

				for j := 0; j < tc.replicas; j++ {
					runner := NewMinimalEphemeralRunner("default", fmt.Sprintf("test-runner-%d", j))
					runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseSucceeded
					runner.Status.RunnerID = j + 100
					runner.OwnerReferences = []metav1.OwnerReference{
						{
							APIVersion: actionsv1alpha1.GroupVersion.String(),
							Kind:       "EphemeralRunnerSet",
							Name:       ers.Name,
							UID:        ers.UID,
							Controller: func() *bool { t := true; return &t }(),
						},
					}
					runners = append(runners, runner)
				}

				fakeClient := newLegacyFakeClientBuilder(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := &EphemeralRunnerSetReconciler{
					Client:          fakeClient,
					Scheme:          scheme,
					Log:             logr.Discard(),
					ResourceBuilder: &ResourceBuilder{Scheme: scheme},
				}

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				_, _ = reconciler.Reconcile(ctx, req)
			}
		})
	}
}

// BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_OutdatedPhaseStatusUpdate benchmarks the
// reconciliation path where the EphemeralRunnerSet is in Outdated phase with only finished runners
func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_OutdatedPhaseStatusUpdate(b *testing.B) {
	testCases := []struct {
		name        string
		runnerCount int
	}{
		{name: "small_5_runners", runnerCount: 5},
		{name: "medium_20_runners", runnerCount: 20},
		{name: "large_100_runners", runnerCount: 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()

				ers := NewMinimalEphemeralRunnerSet("default", "test-ers")
				ers.Spec.Replicas = tc.runnerCount
				controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)

				integrityHash := ephemeralRunnerSetIntegrityHash(ers)
				if ers.Annotations == nil {
					ers.Annotations = make(map[string]string)
				}
				ers.Annotations[AnnotationKeyIntegrityHash] = integrityHash

				ers.Status.CurrentReplicas = tc.runnerCount
				ers.Status.Phase = actionsv1alpha1.EphemeralRunnerSetPhaseOutdated

				runners := make([]client.Object, 0, tc.runnerCount+1)
				runners = append(runners, ers)

				for j := 0; j < tc.runnerCount; j++ {
					runner := NewMinimalEphemeralRunner("default", fmt.Sprintf("test-runner-%d", j))
					runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseSucceeded
					runner.Status.RunnerID = j + 100
					runner.OwnerReferences = []metav1.OwnerReference{
						{
							APIVersion: actionsv1alpha1.GroupVersion.String(),
							Kind:       "EphemeralRunnerSet",
							Name:       ers.Name,
							UID:        ers.UID,
							Controller: func() *bool { t := true; return &t }(),
						},
					}
					runners = append(runners, runner)
				}

				fakeClient := newLegacyFakeClientBuilder(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := &EphemeralRunnerSetReconciler{
					Client:          fakeClient,
					Scheme:          scheme,
					Log:             logr.Discard(),
					ResourceBuilder: &ResourceBuilder{Scheme: scheme},
				}

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				_, _ = reconciler.Reconcile(ctx, req)
			}
		})
	}
}

func newLegacyFakeClientBuilder(scheme *runtime.Scheme) *fake.ClientBuilder {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjectTracker(clientgotesting.NewObjectTracker(scheme, serializer.NewCodecFactory(scheme).UniversalDecoder()))
}
