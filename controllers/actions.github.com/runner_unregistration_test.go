package actionsgithubcom

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/object"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestRunnerMayBeRegistered(t *testing.T) {
	tt := map[string]struct {
		runnerID int
		phase    v1alpha1.EphemeralRunnerPhase
		want     bool
	}{
		"succeeded runner deregistered itself": {
			runnerID: 1,
			phase:    v1alpha1.EphemeralRunnerPhaseSucceeded,
			want:     false,
		},
		"runner without an ID was never registered": {
			runnerID: 0,
			phase:    v1alpha1.EphemeralRunnerPhaseRunning,
			want:     false,
		},
		"succeeded runner without an ID": {
			runnerID: 0,
			phase:    v1alpha1.EphemeralRunnerPhaseSucceeded,
			want:     false,
		},
		"running runner never got to deregister": {
			runnerID: 1,
			phase:    v1alpha1.EphemeralRunnerPhaseRunning,
			want:     true,
		},
		"pending runner is registered but idle": {
			runnerID: 1,
			phase:    v1alpha1.EphemeralRunnerPhasePending,
			want:     true,
		},
		"failed runner never got to deregister": {
			runnerID: 1,
			phase:    v1alpha1.EphemeralRunnerPhaseFailed,
			want:     true,
		},
		"outdated runner never got to deregister": {
			runnerID: 1,
			phase:    v1alpha1.EphemeralRunnerPhaseOutdated,
			want:     true,
		},
		"runner with no phase yet": {
			runnerID: 1,
			want:     true,
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			runner := newUnregistrationTestRunner("test-runner", tc.runnerID, tc.phase)
			assert.Equal(t, tc.want, runnerMayBeRegistered(runner))
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

	q.Push(newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

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
			q.Push(newUnregistrationTestRunner(fmt.Sprintf("test-runner-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning))
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

	q.Push(newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

	select {
	case <-succeeded:
	case <-time.After(10 * time.Second):
		t.Fatal("runner removal was not retried until it succeeded")
	}

	assert.Eventually(t, func() bool { return q.len() == 0 }, 10*time.Second, 10*time.Millisecond)
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

			q.Push(newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

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

	q.Push(newUnregistrationTestRunner("test-runner", 42, v1alpha1.EphemeralRunnerPhaseRunning))

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
		q.Push(newUnregistrationTestRunner("first", 1, v1alpha1.EphemeralRunnerPhaseRunning))
		q.Push(newUnregistrationTestRunner("second", 2, v1alpha1.EphemeralRunnerPhaseRunning))

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
			q.Push(newUnregistrationTestRunner(fmt.Sprintf("runner-%d", i), i+1, v1alpha1.EphemeralRunnerPhaseRunning))
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

	t.Run("a delayed request does not hold up the ones behind it", func(t *testing.T) {
		q := NewRunnerUnregistrationQueue(log.Log, &stubSecretResolver{}, 0)
		q.pushAfter(runnerUnregistration{
			runner: newUnregistrationTestRunner("delayed", 1, v1alpha1.EphemeralRunnerPhaseRunning),
		}, time.Hour)
		q.Push(newUnregistrationTestRunner("ready", 2, v1alpha1.EphemeralRunnerPhaseRunning))

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
		q.Push(newUnregistrationTestRunner("ready", 2, v1alpha1.EphemeralRunnerPhaseRunning))

		later := now.Add(2 * time.Minute)
		for _, want := range []string{"ready", "retried"} {
			request, _, ok := q.next(later)
			require.True(t, ok)
			assert.Equal(t, want, request.runner.Name)
		}

		// A promoted request loses its delay, so it is not held back again.
		assert.Equal(t, 0, q.len())
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
	q.Push(runner)
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
