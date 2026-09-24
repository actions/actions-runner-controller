package actionsgithubcom

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/object"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// stubSecretResolver hands every caller the same client, so a test can drive
// the queue without a Kubernetes API server behind it.
type stubSecretResolver struct {
	client multiclient.Client
	err    error
}

func (s *stubSecretResolver) GetAppConfig(context.Context, object.ActionsGitHubObject) (*appconfig.AppConfig, error) {
	return nil, s.err
}

func (s *stubSecretResolver) GetActionsService(context.Context, object.ActionsGitHubObject) (multiclient.Client, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.client, nil
}

// queued returns everything the queue is still holding, ready requests first.
func (q *RunnerUnregistrationQueue) queued() []runnerUnregistration {
	q.mu.Lock()
	defer q.mu.Unlock()

	queued := make([]runnerUnregistration, 0, len(q.ready)-q.readyHead+len(q.delayed))
	queued = append(queued, q.ready[q.readyHead:]...)
	return append(queued, q.delayed...)
}

// pushTestRunner queues a runner under the ID its own status reports, which is
// the case everywhere except a runner deleted before the controller recorded
// one.
func pushTestRunner(q *RunnerUnregistrationQueue, runner *v1alpha1.EphemeralRunner) {
	q.Push(runner, runner.Status.RunnerID)
}

func newTestUnregistrationQueue(t *testing.T, client multiclient.Client) *RunnerUnregistrationQueue {
	t.Helper()

	q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{client: client}, 0)
	// Retries are timed in tens of seconds in production. A test that waits for
	// one should not be.
	q.retryDelay = 5 * time.Millisecond
	return q
}

// startTestUnregistrationQueue runs the pool for the duration of the test.
func startTestUnregistrationQueue(t *testing.T, q *RunnerUnregistrationQueue) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		require.NoError(t, q.Start(ctx))
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("unregistration workers did not stop after the context was cancelled")
		}
	})
}

func newUnregistrationTestRunner(name string, runnerID int, phase v1alpha1.EphemeralRunnerPhase) *v1alpha1.EphemeralRunner {
	return &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1alpha1.EphemeralRunnerSpec{
			GitHubConfigURL:    "https://github.com/owner/repo",
			GitHubConfigSecret: "config-secret",
		},
		Status: v1alpha1.EphemeralRunnerStatus{
			RunnerID: runnerID,
			Phase:    phase,
		},
	}
}

// TestRunnerSelfDeregistered pins the decision the whole change rests on: a
// runner that exited with code 0 is not asked to be removed from the service.
func TestRunnerSelfDeregistered(t *testing.T) {
	tt := map[string]struct {
		phase v1alpha1.EphemeralRunnerPhase
		want  bool
	}{
		"succeeded runner deregistered itself":    {phase: v1alpha1.EphemeralRunnerPhaseSucceeded, want: true},
		"running runner never got to deregister":  {phase: v1alpha1.EphemeralRunnerPhaseRunning, want: false},
		"pending runner is registered but idle":   {phase: v1alpha1.EphemeralRunnerPhasePending, want: false},
		"failed runner never got to deregister":   {phase: v1alpha1.EphemeralRunnerPhaseFailed, want: false},
		"outdated runner never got to deregister": {phase: v1alpha1.EphemeralRunnerPhaseOutdated, want: false},
		"runner with no phase yet":                {want: false},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			runner := newUnregistrationTestRunner("test-runner", 1, tc.phase)
			assert.Equal(t, tc.want, runnerSelfDeregistered(runner))
		})
	}
}

func TestRegisteredRunnerID(t *testing.T) {
	tt := map[string]struct {
		runnerID      int
		secret        map[string][]byte
		secretErr     error
		actionsClient multiclient.Client
		actionsErr    error
		want          int
		wantErr       bool
	}{
		"runner reports its own ID": {
			runnerID: 1,
			want:     1,
		},
		"negative status ID is not a registration": {
			runnerID: -1,
			wantErr:  true,
		},
		"the status is preferred over the secret": {
			runnerID: 1,
			secret:   map[string][]byte{"runnerId": []byte("7")},
			want:     1,
		},
		// Registration happens before the status records the ID, so a runner
		// deleted in between is registered under an ID only the secret knows.
		"runner deleted before its ID was recorded": {
			secret: map[string][]byte{"runnerId": []byte("7")},
			want:   7,
		},
		"runner deleted before its JIT secret was created": {
			actionsClient: fake.NewClient(fake.WithGetRunnerByName(
				&scaleset.RunnerReference{ID: 7, RunnerScaleSetID: 1},
				nil,
			)),
			want: 7,
		},
		"runner without an ID, a secret, or a matching registration was never registered": {
			actionsClient: fake.NewClient(fake.WithGetRunnerByName(nil, nil)),
			want:          0,
		},
		"runner whose matching registration cannot be looked up": {
			actionsClient: fake.NewClient(fake.WithGetRunnerByName(
				nil,
				errors.New("Actions service is unavailable"),
			)),
			wantErr: true,
		},
		"runner whose matching registration belongs to another scale set": {
			actionsClient: fake.NewClient(fake.WithGetRunnerByName(
				&scaleset.RunnerReference{ID: 7, RunnerScaleSetID: 2},
				nil,
			)),
			wantErr: true,
		},
		"matching registration has a zero ID": {
			actionsClient: fake.NewClient(fake.WithGetRunnerByName(
				&scaleset.RunnerReference{RunnerScaleSetID: 1},
				nil,
			)),
			wantErr: true,
		},
		"matching registration has a negative ID": {
			actionsClient: fake.NewClient(fake.WithGetRunnerByName(
				&scaleset.RunnerReference{ID: -1, RunnerScaleSetID: 1},
				nil,
			)),
			wantErr: true,
		},
		"runner whose Actions client cannot be resolved": {
			actionsErr: errors.New("configuration cannot be read"),
			wantErr:    true,
		},
		"runner whose secret cannot name a registration": {
			secret:  map[string][]byte{"runnerId": []byte("not-a-number")},
			wantErr: true,
		},
		"runner whose secret has no ID": {
			secret:  map[string][]byte{},
			wantErr: true,
		},
		"runner whose secret has a zero ID": {
			secret:  map[string][]byte{"runnerId": []byte("0")},
			wantErr: true,
		},
		"runner whose secret has a negative ID": {
			secret:  map[string][]byte{"runnerId": []byte("-1")},
			wantErr: true,
		},
		// An unreadable secret is not an answer. Reporting 0 would drop the
		// finalizer and lose the last record of a registration that may exist.
		"runner whose secret cannot be read": {
			secret:    map[string][]byte{"runnerId": []byte("7")},
			secretErr: apierrors.NewServiceUnavailable("etcd is unhappy"),
			wantErr:   true,
		},
		"the status is answered without reading the secret at all": {
			runnerID:  1,
			secretErr: apierrors.NewServiceUnavailable("etcd is unhappy"),
			want:      1,
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			runner := newUnregistrationTestRunner("test-runner", tc.runnerID, v1alpha1.EphemeralRunnerPhaseRunning)
			runner.Spec.RunnerScaleSetID = 1

			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))

			builder := ctrlfake.NewClientBuilder().WithScheme(scheme)
			if tc.secret != nil {
				builder = builder.WithObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: runner.Name, Namespace: runner.Namespace},
					Data:       tc.secret,
				})
			}
			if tc.secretErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return tc.secretErr
					},
				})
			}

			reconciler := &EphemeralRunnerReconciler{
				Client: builder.Build(),
				ResourceBuilder: ResourceBuilder{
					SecretResolver: &stubSecretResolver{client: tc.actionsClient, err: tc.actionsErr},
				},
			}
			runnerID, err := reconciler.registeredRunnerID(t.Context(), runner, logr.Discard())
			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, runnerID)
		})
	}
}

func TestQueueUnregistration(t *testing.T) {
	tt := map[string]struct {
		patchErr   error
		wantErr    bool
		wantQueued bool
	}{
		"finalizer was removed with the runner": {
			patchErr:   apierrors.NewNotFound(schema.GroupResource{Resource: "ephemeralrunners"}, "test-runner"),
			wantQueued: true,
		},
		"finalizer patch failed": {
			patchErr: errors.New("etcd is unhappy"),
			wantErr:  true,
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			runner := newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning)
			runner.Finalizers = []string{ephemeralRunnerActionsFinalizerName}
			q := NewRunnerUnregistrationQueue(logr.Discard(), nil, 0)

			c := ctrlfake.NewClientBuilder().
				WithScheme(runtime.NewScheme()).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
						return tc.patchErr
					},
				}).
				Build()
			reconciler := &EphemeralRunnerReconciler{Client: c, UnregistrationQueue: q}

			err := reconciler.queueUnregistration(t.Context(), runner, logr.Discard())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			queued := q.queued()
			if tc.wantQueued {
				require.Len(t, queued, 1)
				assert.Equal(t, 42, queued[0].runnerID)
			} else {
				assert.Empty(t, queued)
			}
		})
	}
}

func TestNewRunnerUnregistrationQueueWorkerCount(t *testing.T) {
	tt := map[string]struct {
		workers int
		want    int
	}{
		"unset falls back to the floor":    {workers: 0, want: 4},
		"below the floor is raised":        {workers: 2, want: 4},
		"at the floor is kept":             {workers: 4, want: 4},
		"above the floor is kept":          {workers: 100, want: 100},
		"negative falls back to the floor": {workers: -1, want: 4},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, tc.workers)
			assert.Equal(t, tc.want, q.workers)
		})
	}
}

func TestRunnerUnregistrationQueueRemovesRunner(t *testing.T) {
	removed := make(chan int64, 1)
	client := fake.NewClient(fake.WithRemoveRunnerFunc(func(_ context.Context, runnerID int64) error {
		removed <- runnerID
		return nil
	}))

	q := newTestUnregistrationQueue(t, client)
	startTestUnregistrationQueue(t, q)

	pushTestRunner(q, newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

	select {
	case runnerID := <-removed:
		assert.Equal(t, int64(42), runnerID)
	case <-time.After(10 * time.Second):
		t.Fatal("runner was not removed from the service")
	}

	assert.Eventually(t, func() bool { return q.len() == 0 }, 10*time.Second, 10*time.Millisecond)
}

func TestRunnerUnregistrationQueuePushIsIndependentOfTheService(t *testing.T) {
	// A service call that never returns must not be able to hold up a push, which
	// is the property the reconciler depends on.
	release := make(chan struct{})
	client := fake.NewClient(fake.WithRemoveRunnerFunc(func(ctx context.Context, _ int64) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}))

	q := newTestUnregistrationQueue(t, client)
	startTestUnregistrationQueue(t, q)
	t.Cleanup(func() { close(release) })

	const runners = 200
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range runners {
			pushTestRunner(q, newUnregistrationTestRunner(fmt.Sprintf("test-runner-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning))
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pushing was blocked by the workers")
	}

	// Every worker is stuck in the call above, so everything but the requests
	// they took is still queued.
	assert.GreaterOrEqual(t, q.len(), runners-q.workers)
}

// TestRunnerUnregistrationQueueDoesNotParkBehindDelayedWork pins the property
// that makes one shared pool safe to use for both ready and delayed requests: a
// worker parked on a retry is parked on an upper bound, not a commitment.
//
// Without it a handful of runners refused with JobStillRunning would hold the
// whole pool for the length of the retry delay, and everything pushed behind
// them would sit in the queue waiting on work that has nothing to do with it.
func TestRunnerUnregistrationQueueDoesNotParkBehindDelayedWork(t *testing.T) {
	const ready = 1000

	removed := make(chan int64, ready)
	client := fake.NewClient(fake.WithRemoveRunnerFunc(func(_ context.Context, runnerID int64) error {
		if runnerID < ready {
			// Never succeeds, so these stay in the delayed list for the whole
			// test and every worker sees them.
			return fmt.Errorf("removing runner: %w", scaleset.JobStillRunningError)
		}
		removed <- runnerID
		return nil
	}))

	q := newTestUnregistrationQueue(t, client)
	// Long enough that a worker which committed to it would miss the deadline
	// below by two orders of magnitude.
	q.retryDelay = 30 * time.Second
	startTestUnregistrationQueue(t, q)

	// Enough refusals to park every worker twice over.
	for i := range q.workers * 2 {
		pushTestRunner(q, newUnregistrationTestRunner(fmt.Sprintf("still-running-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning))
	}
	require.Eventually(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.delayed) == q.workers*2
	}, 10*time.Second, time.Millisecond, "the refused runners never settled into the delayed list")

	for i := range ready {
		runnerID := ready + i
		pushTestRunner(q, newUnregistrationTestRunner(fmt.Sprintf("ready-%d", runnerID), runnerID, v1alpha1.EphemeralRunnerPhaseRunning))
	}

	for range ready {
		select {
		case <-removed:
		case <-time.After(20 * time.Second):
			t.Fatal("ready removals were held up behind runners waiting out a retry")
		}
	}
}

func TestRunnerUnregistrationQueueRetriesWhileTheJobIsStillRunning(t *testing.T) {
	var mu sync.Mutex
	var calls int
	succeeded := make(chan struct{})

	client := fake.NewClient(fake.WithRemoveRunnerFunc(func(_ context.Context, _ int64) error {
		mu.Lock()
		defer mu.Unlock()

		calls++
		if calls < 3 {
			return fmt.Errorf("removing runner: %w", scaleset.JobStillRunningError)
		}
		close(succeeded)
		return nil
	}))

	q := newTestUnregistrationQueue(t, client)
	startTestUnregistrationQueue(t, q)

	pushTestRunner(q, newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

	select {
	case <-succeeded:
	case <-time.After(10 * time.Second):
		t.Fatal("runner removal was not retried until it succeeded")
	}

	assert.Eventually(t, func() bool { return q.len() == 0 }, 10*time.Second, 10*time.Millisecond)
}

func TestRunnerUnregistrationQueueDeduplicates(t *testing.T) {
	runner := newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning)

	t.Run("a registration already queued is not queued again", func(t *testing.T) {
		q := newTestUnregistrationQueue(t, fake.NewClient())

		for range 5 {
			pushTestRunner(q, runner)
		}

		assert.Equal(t, 1, q.len())
	})

	t.Run("a registration being removed right now is not queued again", func(t *testing.T) {
		// The window the claim covers is wider than the queue itself: a request
		// a worker has already taken is no longer queued, but the service has
		// not answered yet, so asking it again is the duplicate call.
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int64

		client := fake.NewClient(fake.WithRemoveRunnerFunc(func(ctx context.Context, _ int64) error {
			if calls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}))

		q := newTestUnregistrationQueue(t, client)
		startTestUnregistrationQueue(t, q)
		t.Cleanup(func() { close(release) })

		pushTestRunner(q, runner)
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("the removal was never attempted")
		}

		pushTestRunner(q, runner)
		assert.Zero(t, q.len(), "the runner was queued again while it was being removed")
	})

	t.Run("a retrying registration is not queued alongside itself", func(t *testing.T) {
		// This is the case that costs something. Two copies of a request for a
		// runner that is still executing a job would sit in the delayed list
		// retrying in lockstep for as long as the job runs.
		var calls atomic.Int64
		client := fake.NewClient(fake.WithRemoveRunnerFunc(func(_ context.Context, _ int64) error {
			calls.Add(1)
			return fmt.Errorf("removing runner: %w", scaleset.JobStillRunningError)
		}))

		q := newTestUnregistrationQueue(t, client)
		startTestUnregistrationQueue(t, q)

		pushTestRunner(q, runner)
		assert.Eventually(t, func() bool { return calls.Load() > 0 }, 10*time.Second, time.Millisecond)

		for range 5 {
			pushTestRunner(q, runner)
		}

		assert.LessOrEqual(t, q.len(), 1, "the retrying runner was queued more than once")
	})

	t.Run("the same runner is queued again once the removal is done with", func(t *testing.T) {
		// The claim is not a memory of everything ever removed. A runner that
		// comes back around, with a request that was dropped on a failure, is
		// taken on again.
		client := fake.NewClient(fake.WithRemoveRunnerFunc(func(_ context.Context, _ int64) error {
			return errors.New("the service is unhappy")
		}))

		q := newTestUnregistrationQueue(t, client)
		startTestUnregistrationQueue(t, q)

		pushTestRunner(q, runner)
		assert.Eventually(t, func() bool { return q.len() == 0 }, 10*time.Second, time.Millisecond)

		// Asked of push rather than of the queue length, because a worker is
		// free to drain the second request before the check runs.
		assert.Eventually(t, func() bool {
			return q.push(runnerUnregistration{runner: runner, runnerID: runner.Status.RunnerID})
		}, 10*time.Second, time.Millisecond, "the runner could not be queued again after its removal failed")
	})

	t.Run("a different registration of the same runner is queued", func(t *testing.T) {
		// A runner deleted before the controller recorded its ID is queued under
		// the ID recovered from its jitconfig secret, which is a different
		// registration from whatever its status reports.
		q := newTestUnregistrationQueue(t, fake.NewClient())

		q.Push(runner, 42)
		q.Push(runner, 43)

		assert.Equal(t, 2, q.len())
	})

	t.Run("runner IDs are only unique within their own GitHub scope", func(t *testing.T) {
		// One controller serves scale sets in different orgs, and the service
		// hands out runner IDs per scope, so the same ID can name two unrelated
		// registrations. Dropping one of them would leak it.
		q := newTestUnregistrationQueue(t, fake.NewClient())

		pushTestRunner(q, newUnregistrationTestRunner("runner-in-one-scale-set", 42, v1alpha1.EphemeralRunnerPhaseRunning))
		pushTestRunner(q, newUnregistrationTestRunner("runner-in-another", 42, v1alpha1.EphemeralRunnerPhaseRunning))

		assert.Equal(t, 2, q.len())
	})
}

func TestRunnerUnregistrationQueueDropsFailedRemovals(t *testing.T) {
	tt := map[string]error{
		"runner is already gone":  fmt.Errorf("removing runner: %w", scaleset.RunnerNotFoundError),
		"scale set is gone":       fmt.Errorf("removing runner: %w", scaleset.NotFoundError),
		"service is unavailable":  errors.New("boom"),
		"credentials are invalid": fmt.Errorf("removing runner: %w", scaleset.UnauthorizedError),
	}

	for name, removeErr := range tt {
		t.Run(name, func(t *testing.T) {
			called := make(chan struct{}, 1)
			client := fake.NewClient(fake.WithRemoveRunnerFunc(func(_ context.Context, _ int64) error {
				select {
				case called <- struct{}{}:
				default:
				}
				return removeErr
			}))

			q := newTestUnregistrationQueue(t, client)
			startTestUnregistrationQueue(t, q)

			pushTestRunner(q, newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

			select {
			case <-called:
			case <-time.After(10 * time.Second):
				t.Fatal("the service was never called")
			}

			// Only a runner that is still executing a job is retried. Everything
			// else is left to the service.
			assert.Eventually(t, func() bool { return q.len() == 0 }, 10*time.Second, 10*time.Millisecond)
			assert.Never(t, func() bool { return q.len() > 0 }, 100*time.Millisecond, 10*time.Millisecond)
		})
	}
}

func TestRunnerUnregistrationQueueSurvivesAnUnresolvableRunner(t *testing.T) {
	q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{err: errors.New("no such secret")}, 0)
	startTestUnregistrationQueue(t, q)

	pushTestRunner(q, newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

	assert.Eventually(t, func() bool { return q.len() == 0 }, 10*time.Second, 10*time.Millisecond)
}

func TestRunnerUnregistrationQueueNext(t *testing.T) {
	now := time.Now()

	t.Run("reports the idle wait when empty", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)

		_, wait, ok := q.next(now)
		assert.False(t, ok)
		assert.Equal(t, unregistrationMaxIdleWait, wait)
	})

	t.Run("takes requests in order", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		pushTestRunner(q, newUnregistrationTestRunner("first", 1, v1alpha1.EphemeralRunnerPhaseRunning))
		pushTestRunner(q, newUnregistrationTestRunner("second", 2, v1alpha1.EphemeralRunnerPhaseRunning))

		request, _, ok := q.next(now)
		require.True(t, ok)
		assert.Equal(t, "first", request.runner.Name)

		request, _, ok = q.next(now)
		require.True(t, ok)
		assert.Equal(t, "second", request.runner.Name)

		_, _, ok = q.next(now)
		assert.False(t, ok)
	})

	t.Run("drained requests are not kept alive by the queue", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		for i := range 10 {
			pushTestRunner(q, newUnregistrationTestRunner(fmt.Sprintf("runner-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning))
		}
		for range 10 {
			_, _, ok := q.next(now)
			require.True(t, ok)
		}

		assert.Equal(t, 0, q.len())

		q.mu.Lock()
		defer q.mu.Unlock()
		assert.Equal(t, 0, q.readyHead, "the ready list is reset once it is drained")
		for _, request := range q.ready[:cap(q.ready)] {
			assert.Nil(t, request.runner, "a taken request still references its runner")
		}
	})

	t.Run("interleaved requests compact consumed ready storage", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		const depth = 8
		for i := range depth {
			pushTestRunner(q, newUnregistrationTestRunner(fmt.Sprintf("runner-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning))
		}

		for i := range 1_000 {
			pushTestRunner(q, newUnregistrationTestRunner(fmt.Sprintf("new-runner-%d", i), depth+i+1, v1alpha1.EphemeralRunnerPhaseRunning))
			_, _, ok := q.next(now)
			require.True(t, ok)

			q.mu.Lock()
			assert.LessOrEqual(t, len(q.ready), 2*depth, "ready storage grew beyond the live queue depth")
			assert.LessOrEqual(t, cap(q.ready), 3*depth, "ready backing storage grew beyond the live queue depth")
			assert.Equal(t, depth, len(q.ready)-q.readyHead)
			q.mu.Unlock()
		}
	})

	t.Run("a delayed request does not hold up the ones behind it", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		q.pushAfter(runnerUnregistration{
			runner: newUnregistrationTestRunner("delayed", 1, v1alpha1.EphemeralRunnerPhaseRunning),
		}, time.Hour)
		pushTestRunner(q, newUnregistrationTestRunner("ready", 2, v1alpha1.EphemeralRunnerPhaseRunning))

		request, _, ok := q.next(now)
		require.True(t, ok)
		assert.Equal(t, "ready", request.runner.Name)

		// The delayed one is still there, and reports how long it has left.
		_, wait, ok := q.next(now)
		assert.False(t, ok)
		assert.Equal(t, unregistrationMaxIdleWait, wait, "a wait longer than the cap is reported as the cap")
		assert.Equal(t, 1, q.len())
	})

	t.Run("a promoted request goes behind the requests already ready", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		q.pushAfter(runnerUnregistration{
			runner: newUnregistrationTestRunner("retried", 1, v1alpha1.EphemeralRunnerPhaseRunning),
		}, time.Minute)
		pushTestRunner(q, newUnregistrationTestRunner("ready", 2, v1alpha1.EphemeralRunnerPhaseRunning))

		later := now.Add(2 * time.Minute)
		for _, want := range []string{"ready", "retried"} {
			request, _, ok := q.next(later)
			require.True(t, ok)
			assert.Equal(t, want, request.runner.Name)
		}

		// A promoted request loses its delay, so it is not held back again.
		assert.Equal(t, 0, q.len())
	})

	t.Run("promoting a burst keeps the ones still waiting", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		// Interleaved, so promoting the due ones has to compact around the rest
		// rather than just cut a prefix off.
		for i := range 10 {
			delay := time.Minute
			if i%2 == 0 {
				delay = time.Hour
			}
			q.pushAfter(runnerUnregistration{
				runner: newUnregistrationTestRunner(fmt.Sprintf("runner-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning),
			}, delay)
		}

		later := now.Add(2 * time.Minute)
		for i := 1; i < 10; i += 2 {
			request, _, ok := q.next(later)
			require.True(t, ok)
			assert.Equal(t, fmt.Sprintf("runner-%d", i), request.runner.Name)
		}

		_, _, ok := q.next(later)
		assert.False(t, ok, "the requests due in an hour were promoted early")
		assert.Equal(t, 5, q.len())

		q.mu.Lock()
		defer q.mu.Unlock()
		for _, request := range q.delayed[len(q.delayed):cap(q.delayed)] {
			assert.Nil(t, request.runner, "a promoted request is still referenced by the delayed list")
		}
	})

	t.Run("waits only until the earliest request is ready", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		q.pushAfter(runnerUnregistration{
			runner: newUnregistrationTestRunner("later", 1, v1alpha1.EphemeralRunnerPhaseRunning),
		}, unregistrationMaxIdleWait/2)
		q.pushAfter(runnerUnregistration{
			runner: newUnregistrationTestRunner("sooner", 2, v1alpha1.EphemeralRunnerPhaseRunning),
		}, unregistrationMaxIdleWait/4)

		_, wait, ok := q.next(time.Now())
		assert.False(t, ok)
		assert.Greater(t, wait, time.Duration(0))
		assert.LessOrEqual(t, wait, unregistrationMaxIdleWait/4)
	})

	t.Run("takes a request once it is ready", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		q.pushAfter(runnerUnregistration{
			runner: newUnregistrationTestRunner("delayed", 1, v1alpha1.EphemeralRunnerPhaseRunning),
		}, time.Minute)

		_, _, ok := q.next(now)
		assert.False(t, ok)

		request, _, ok := q.next(now.Add(2 * time.Minute))
		require.True(t, ok)
		assert.Equal(t, "delayed", request.runner.Name)
	})
}

func TestRunnerUnregistrationQueuePushCopiesTheRunner(t *testing.T) {
	q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)

	runner := newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning)
	pushTestRunner(q, runner)
	runner.Status.RunnerID = 0
	runner.Name = "mutated"

	request, _, ok := q.next(time.Now())
	require.True(t, ok)
	assert.Equal(t, "test-runner", request.runner.Name)
	assert.Equal(t, 42, request.runner.Status.RunnerID)
}

func TestRunnerUnregistrationQueueStartStopsWithItsContext(t *testing.T) {
	q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{client: fake.NewClient()}, 0)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- q.Start(ctx) }()

	// Anything left behind at shutdown is dropped rather than drained.
	q.pushAfter(runnerUnregistration{
		runner: newUnregistrationTestRunner("delayed", 1, v1alpha1.EphemeralRunnerPhaseRunning),
	}, time.Hour)

	cancel()

	select {
	case err := <-stopped:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("workers did not stop after the context was cancelled")
	}
}
