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
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(runners...).
				WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
				WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
				Build()

			reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)

			ctx := context.Background()
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      "test-ers",
				},
			}

			WarmupIteration(b, func() {
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
			})

			b.ResetTimer()
			for b.Loop() {
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
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
				ers.Status.RunningEphemeralRunners = tc.current

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

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_ScaleDown(b *testing.B) {
	testCases := []struct {
		name    string
		current int
		desired int
	}{
		{name: "small_scale_3_to_0", current: 3, desired: 0},
		{name: "medium_scale_15_to_5", current: 15, desired: 5},
		{name: "large_scale_60_to_10", current: 60, desired: 10},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				ers := newBenchmarkEphemeralRunnerSetForConvergence(tc.desired)
				objects := newBenchmarkIdleEphemeralRunnerObjects(ers, tc.current)
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(objects...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()
				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)
				reconciler.ResourceBuilder.SecretResolver = secretresolver.New(
					fakeClient,
					scalefake.NewMultiClient(scalefake.WithClient(scalefake.NewClient(scalefake.WithRemoveRunner(nil)))),
				)
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}
				b.StartTimer()

				if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSet_ScaleUpWithProxy(b *testing.B) {
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
				ers.Labels = map[string]string{
					LabelKeyGitHubScaleSetName:      "test-scale-set",
					LabelKeyGitHubScaleSetNamespace: "default",
				}
				ers.Spec.Replicas = tc.desired
				ers.Spec.EphemeralRunnerSpec.Proxy = &actionsv1alpha1.ProxyConfig{
					HTTP:    &actionsv1alpha1.ProxyServerConfig{URL: "http://proxy.example.com:8080"},
					HTTPS:   &actionsv1alpha1.ProxyServerConfig{URL: "https://proxy.example.com:8443"},
					NoProxy: []string{"kubernetes.default.svc", "127.0.0.1"},
				}
				controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)

				integrityHash := ephemeralRunnerSetIntegrityHash(ers)
				if ers.Annotations == nil {
					ers.Annotations = make(map[string]string)
				}
				ers.Annotations[AnnotationKeyIntegrityHash] = integrityHash

				ers.Status.CurrentReplicas = tc.current
				ers.Status.Phase = actionsv1alpha1.EphemeralRunnerSetPhaseRunning
				ers.Status.RunningEphemeralRunners = tc.current

				proxySecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      proxyEphemeralRunnerSetSecretName(ers),
						Namespace: ers.Namespace,
						Labels: map[string]string{
							LabelKeyGitHubScaleSetName:      ers.Labels[LabelKeyGitHubScaleSetName],
							LabelKeyGitHubScaleSetNamespace: ers.Labels[LabelKeyGitHubScaleSetNamespace],
						},
						Annotations: map[string]string{},
					},
					Data: map[string][]byte{
						"http_proxy":  []byte("http://proxy.example.com:8080"),
						"https_proxy": []byte("https://proxy.example.com:8443"),
						"no_proxy":    []byte("kubernetes.default.svc,127.0.0.1"),
					},
				}
				proxySecret.Annotations[AnnotationKeyIntegrityHash] = ephemeralRunnerSetProxySecretIdentityHash(proxySecret)

				runners := make([]client.Object, 0, tc.current+2)
				runners = append(runners, ers, proxySecret)

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

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
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

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(ers).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
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

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
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

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(runners...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()

				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)

				ctx := context.Background()
				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "default",
						Name:      "test-ers",
					},
				}

				b.StartTimer()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
