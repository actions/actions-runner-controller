package actionsgithubcom

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func BenchmarkActionsGithub_Converge_EphemeralRunnerSet_ScaleUpCreates(b *testing.B) {
	testCases := []struct {
		name    string
		desired int
	}{
		{name: "small_10", desired: 10},
		{name: "large_100", desired: 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			ctx := context.Background()
			var totalElapsed time.Duration
			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				ers := newBenchmarkEphemeralRunnerSetForConvergence(tc.desired)
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(ers).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()
				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)
				req := NewBenchmarkRequest("default", "test-ers")
				b.StartTimer()

				started := time.Now()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
				elapsed := time.Since(started)
				totalElapsed += elapsed
				b.StopTimer()
				assertBenchmarkEphemeralRunnerCount(b, ctx, fakeClient, tc.desired)
				b.StartTimer()
			}
			b.ReportMetric(float64(totalElapsed.Microseconds())/float64(b.N), "converge_us/op")
		})
	}
}

func BenchmarkActionsGithub_Converge_EphemeralRunnerSet_IdleScaleDown(b *testing.B) {
	testCases := []struct {
		name    string
		current int
		desired int
	}{
		{name: "small_10_to_0", current: 10, desired: 0},
		{name: "large_100_to_0", current: 100, desired: 0},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scheme := runtime.NewScheme()
			_ = actionsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			ctx := context.Background()
			var totalElapsed time.Duration
			var totalRemoveCalls int64
			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				var removeCalls int64
				ers := newBenchmarkEphemeralRunnerSetForConvergence(tc.desired)
				objects := newBenchmarkIdleEphemeralRunnerObjects(ers, tc.current)
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(objects...).
					WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}, &actionsv1alpha1.EphemeralRunner{}).
					WithIndex(&actionsv1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
					Build()
				scaleClient := scalefake.NewClient(scalefake.WithRemoveRunnerFunc(func(ctx context.Context, runnerID int64) error {
					atomic.AddInt64(&removeCalls, 1)
					return nil
				}))
				reconciler := newBenchmarkEphemeralRunnerSetReconciler(fakeClient, scheme)
				reconciler.ResourceBuilder.SecretResolver = secretresolver.New(
					fakeClient,
					scalefake.NewMultiClient(scalefake.WithClient(scaleClient)),
				)
				req := NewBenchmarkRequest("default", "test-ers")
				b.StartTimer()

				started := time.Now()
				if _, err := reconciler.Reconcile(ctx, req); err != nil {
					b.Fatal(err)
				}
				elapsed := time.Since(started)
				totalElapsed += elapsed
				totalRemoveCalls += atomic.LoadInt64(&removeCalls)
				b.StopTimer()
				assertBenchmarkEphemeralRunnerCount(b, ctx, fakeClient, tc.desired)
				b.StartTimer()
			}
			b.ReportMetric(float64(totalElapsed.Microseconds())/float64(b.N), "converge_us/op")
			b.ReportMetric(float64(totalRemoveCalls)/float64(b.N), "remove_calls/op")
		})
	}
}

func newBenchmarkEphemeralRunnerSetForConvergence(replicas int) *actionsv1alpha1.EphemeralRunnerSet {
	ers := NewMinimalEphemeralRunnerSet("default", "test-ers")
	ers.Spec.PatchID = 0
	ers.Spec.Replicas = replicas
	controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)
	if ers.Annotations == nil {
		ers.Annotations = map[string]string{}
	}
	ers.Annotations[AnnotationKeyIntegrityHash] = ephemeralRunnerSetIntegrityHash(ers)
	ers.Status.Phase = actionsv1alpha1.EphemeralRunnerSetPhaseRunning
	ers.Status.CurrentReplicas = replicas
	ers.Status.RunningEphemeralRunners = replicas
	return ers
}

func newBenchmarkIdleEphemeralRunnerObjects(ers *actionsv1alpha1.EphemeralRunnerSet, count int) []client.Object {
	objects := make([]client.Object, 0, count+1)
	objects = append(objects, ers, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      benchmarkGitHubConfigSecret,
			Namespace: ers.Namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("test-token"),
		},
	})
	for i := 0; i < count; i++ {
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
		objects = append(objects, runner)
	}
	return objects
}

func assertBenchmarkEphemeralRunnerCount(b *testing.B, ctx context.Context, k8sClient client.Client, expected int) {
	b.Helper()
	list := &actionsv1alpha1.EphemeralRunnerList{}
	if err := k8sClient.List(ctx, list, client.InNamespace("default"), client.MatchingFields{resourceOwnerKey: "test-ers"}); err != nil {
		b.Fatalf("list ephemeral runners: %v", err)
	}
	if len(list.Items) != expected {
		b.Fatalf("expected %d ephemeral runners, got %d", expected, len(list.Items))
	}
}
