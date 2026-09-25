package actionsgithubcom

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// serviceLatencies are the round trip times the Actions service is stood up
// with. Zero is not a latency anyone observes; it is there to separate the cost
// of the call itself from the cost of waiting for it.
var serviceLatencies = []time.Duration{0, 1 * time.Millisecond, 25 * time.Millisecond}

// Everything here is built with logr.Discard rather than the controller-runtime
// global logger. That logger is a promise nobody fulfills in a benchmark
// binary, and 30 seconds in it resolves itself to a fallback that writes a
// stack trace to stderr. A run long enough to hit that gets its output
// corrupted mid-line and its timings skewed by the writes.

// benchmarkActionsClient returns a client whose RemoveRunner takes latency to
// answer.
func benchmarkActionsClient(latency time.Duration) multiclient.Client {
	return scalefake.NewClient(scalefake.WithRemoveRunnerFunc(func(context.Context, int64) error {
		if latency > 0 {
			time.Sleep(latency)
		}
		return nil
	}))
}

// newFinalizeBenchmarkReconciler builds a reconciler over an in-memory API
// server holding nothing but one runner, its pod and its secret. queue may be
// nil, which makes the push a no-op.
//
// One of these per iteration. The fake client scans every object it holds on
// every read, so a store built up across iterations would charge the reconcile
// for how many runners the benchmark happens to have finalized already.
func newFinalizeBenchmarkReconciler(b *testing.B, scheme *runtime.Scheme, queue *RunnerUnregistrationQueue, phase v1alpha1.EphemeralRunnerPhase, n int) (*EphemeralRunnerReconciler, types.NamespacedName, int) {
	b.Helper()

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.EphemeralRunner{}).
		Build()

	reconciler := &EphemeralRunnerReconciler{
		Client:              c,
		APIReader:           c,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		UnregistrationQueue: queue,
		ResourceBuilder: ResourceBuilder{
			ResourceCache: newTestResourceCache(),
		},
	}

	key, runnerID := createFinalizeBenchmarkRunner(b, c, phase, n)
	return reconciler, key, runnerID
}

// benchmarkScheme registers only the types the finalizer path touches. The fake
// client walks the scheme on every operation, and the full client-go scheme
// puts more time into that bookkeeping than into the reconcile being measured.
func benchmarkScheme(b *testing.B) *runtime.Scheme {
	b.Helper()

	scheme := runtime.NewScheme()
	require.NoError(b, corev1.AddToScheme(scheme))
	require.NoError(b, v1alpha1.AddToScheme(scheme))
	return scheme
}

// createFinalizeBenchmarkRunner puts a deleted EphemeralRunner, its pod and its
// jitconfig secret in front of the reconciler, which is the state the finalizer
// path runs against.
func createFinalizeBenchmarkRunner(b *testing.B, c client.Client, phase v1alpha1.EphemeralRunnerPhase, n int) (types.NamespacedName, int) {
	b.Helper()

	// A distinct runner every iteration, as in production. The API server is
	// rebuilt each time and would not care, but the queue is not: it holds a
	// claim on every registration handed to it, and reusing one would measure
	// the duplicate being turned away rather than the push.
	name := fmt.Sprintf("runner-%d", n)
	ctx := context.Background()
	ephemeralRunner := newExampleRunner(name, "default", "config-secret")
	ephemeralRunner.Finalizers = []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName}
	// Offset so the untimed warmup pass, which runs at -1, still gets a valid
	// registration and takes the same path as the measured ones.
	ephemeralRunner.Status.RunnerID = n + 2
	ephemeralRunner.Status.Phase = phase
	require.NoError(b, c.Create(ctx, ephemeralRunner))

	require.NoError(b, c.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName, Image: "ghcr.io/actions/actions-runner"}},
		},
		// Finished, as the pod of a completed job is. The service is asked
		// before a live pod is deleted, which is not the path measured here.
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}))
	require.NoError(b, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       map[string][]byte{jitTokenKey: []byte("jit")},
	}))

	require.NoError(b, c.Delete(ctx, ephemeralRunner))

	return types.NamespacedName{Namespace: "default", Name: name}, ephemeralRunner.Status.RunnerID
}

// BenchmarkEphemeralRunnerFinalize measures one pass of the finalizer path: the
// work that stands between a completed job and its pod being collected.
//
// The variants are the three ways that pass can go:
//
//   - skipped: the runner exited cleanly, so its registration is already gone
//     and nothing is queued. This is the path every completed job takes.
//   - queued: the registration is handed to the workers. Present behaviour for
//     a runner that may still be registered and whose pod has finished.
//   - synchronous: the removal is issued inline before the local cleanup, which
//     is the behaviour this replaced. A runner whose pod is still live pays it,
//     because its pod may be executing a job.
//
// The API server behind it is the controller runtime fake, which costs
// milliseconds per reconcile and sets a floor well above what queueing or
// skipping saves on a single deletion. So read this as the shape rather than
// the size: queued is flat across service latencies and synchronous is not.
// What a deletion pays to queue is BenchmarkRunnerUnregistrationQueuePush, and
// what it saves by skipping is that same figure.
func BenchmarkEphemeralRunnerFinalize(b *testing.B) {
	b.Run("skipped", func(b *testing.B) {
		queue := NewRunnerUnregistrationQueue(logr.Discard(), nil, 0)
		benchmarkFinalize(b, queue, v1alpha1.EphemeralRunnerPhaseSucceeded, nil)
	})

	for _, latency := range serviceLatencies {
		b.Run(fmt.Sprintf("queued/latency=%s", latency), func(b *testing.B) {
			queue := NewRunnerUnregistrationQueue(logr.Discard(), &stubSecretResolver{client: benchmarkActionsClient(latency)}, 0)
			benchmarkFinalize(b, queue, v1alpha1.EphemeralRunnerPhaseRunning, nil)
		})
	}

	for _, latency := range serviceLatencies {
		b.Run(fmt.Sprintf("synchronous/latency=%s", latency), func(b *testing.B) {
			actionsClient := benchmarkActionsClient(latency)
			// The removal ran before any of the local cleanup, so the reconcile
			// carried it. Issued here in front of a reconcile that queues nothing,
			// which is the same two pieces of work in the same order.
			benchmarkFinalize(b, nil, v1alpha1.EphemeralRunnerPhaseRunning, func(ctx context.Context, runnerID int) {
				_ = actionsClient.RemoveRunner(ctx, int64(runnerID))
			})
		})
	}
}

func benchmarkFinalize(b *testing.B, queue *RunnerUnregistrationQueue, phase v1alpha1.EphemeralRunnerPhase, before func(context.Context, int)) {
	scheme := benchmarkScheme(b)
	ctx := context.Background()

	// One untimed pass first. Everything here is measured in fractions of a
	// millisecond against a cold heap, and whichever variant runs first should
	// not be charged for warming the process up.
	warmup, key, _ := newFinalizeBenchmarkReconciler(b, scheme, queue, phase, -1)
	if _, err := warmup.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		b.Fatalf("reconcile: %v", err)
	}

	b.ResetTimer()
	b.StopTimer()
	for n := range b.N {
		reconciler, key, runnerID := newFinalizeBenchmarkReconciler(b, scheme, queue, phase, n)

		b.StartTimer()
		if before != nil {
			before(ctx, runnerID)
		}
		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		b.StopTimer()

		if err != nil {
			b.Fatalf("reconcile: %v", err)
		}
		if err := reconciler.Get(ctx, key, new(v1alpha1.EphemeralRunner)); err == nil {
			b.Fatal("runner was not finalized")
		}
	}
}

// BenchmarkRunnerUnregistrationQueuePush measures what the reconciler pays to
// hand a removal over. It is the whole cost the Actions service imposes on the
// finalizer path now, and it must stay flat: Push is called under no lock the
// reconciler holds, but every deletion goes through it.
func BenchmarkRunnerUnregistrationQueuePush(b *testing.B) {
	q := NewRunnerUnregistrationQueue(logr.Discard(), nil, 0)
	runner := newUnregistrationTestRunner("runner", 0, v1alpha1.EphemeralRunnerPhaseRunning)

	b.ReportAllocs()
	runnerID := 0
	for b.Loop() {
		// A distinct registration every time, as in production. Pushing one
		// runner over and over would measure the duplicate being turned away
		// rather than the cost of queueing.
		runnerID++
		runner.Status.RunnerID = runnerID
		pushTestRunner(q, runner)
	}
	b.StopTimer()

	// Nothing drains it here, so the queue holds every push and the claim on
	// every one of them is never released. Reported so the number above is read
	// as the cost of a push into a queue that deep.
	b.ReportMetric(float64(q.len()), "queued")
}

// BenchmarkRunnerUnregistrationQueueDrain measures taking a burst back off the
// queue, which is what the workers do when a scale set finishes.
//
// Reported per request. A burst arrives all at once and is drained in order, so
// this has to stay flat as the burst grows: taking from the front by resliding
// the tail down would make it climb with the size of the burst, and it climbs
// while holding the lock that Push needs.
func BenchmarkRunnerUnregistrationQueueDrain(b *testing.B) {
	for _, burst := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("burst=%d", burst), func(b *testing.B) {
			q := NewRunnerUnregistrationQueue(logr.Discard(), nil, 0)
			runner := newUnregistrationTestRunner("runner", 42, v1alpha1.EphemeralRunnerPhaseRunning)
			now := time.Now()

			for b.Loop() {
				b.StopTimer()
				for range burst {
					// Queued directly, because what is being measured is taking
					// requests back off, and a claim is only given up by the
					// worker that finishes with one.
					q.enqueue(runnerUnregistration{runner: runner})
				}
				b.StartTimer()

				for range burst {
					if _, _, ok := q.next(now); !ok {
						b.Fatal("queue ran dry before the burst was drained")
					}
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*burst), "ns/request")
		})
	}
}
