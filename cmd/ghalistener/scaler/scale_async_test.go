package scaler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func newAsyncScaler(t *testing.T, queueSize int, jobQPS float32, handler http.HandlerFunc) *Scaler {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	scaleClient, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL, QPS: -1})
	require.NoError(t, err)
	jobClient, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL, QPS: jobQPS, Burst: 1})
	require.NoError(t, err)

	w := &Scaler{
		scaleClientset: scaleClient,
		jobClientset:   jobClient,
		config: Config{
			EphemeralRunnerSetNamespace: "default",
			EphemeralRunnerSetName:      "set",
			MaxRunners:                  100,
		},
		targetRunners: -1,
		patchSeq:      -1,
		logger:        discardLogger,
		recorder:      metrics.Discard,
		messageClient: acquiringClient{acquire: func(_ context.Context, ids []int64) ([]int64, error) {
			return ids, nil
		}},
		jobs: make(chan *scaleset.JobStarted, queueSize),
	}
	w.startJobWorkers()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.Close(ctx); err != nil {
			t.Errorf("failed to close scaler: %v", err)
		}
	})
	return w
}

func writeJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

func TestScalePrioritizesScalingAndAcquisition(t *testing.T) {
	var scales atomic.Int32
	var acquireAt int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	w := newAsyncScaler(t, 2, 0.1, func(out http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ephemeralrunnersets/") {
			scales.Add(1)
			writeJSON(out)
			return
		}
		started <- struct{}{}
		select {
		case <-release:
			writeJSON(out)
		case <-r.Context().Done():
		}
	})
	t.Cleanup(releaseOnce)
	w.messageClient = acquiringClient{acquire: func(_ context.Context, ids []int64) ([]int64, error) {
		acquireAt = scales.Load()
		return ids, nil
	}}

	first := &scaleset.RunnerScaleSetMessage{
		Statistics:           &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 1},
		JobAvailableMessages: []*scaleset.JobAvailable{{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 1}}},
		JobStartedMessages:   []*scaleset.JobStarted{{RunnerName: "runner"}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Scale(ctx, first) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		releaseOnce()
		t.Fatalf("Scale waited for a job status patch: %v", <-done)
	}
	require.EqualValues(t, 1, acquireAt)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job status patch did not start")
	}

	go func() {
		done <- w.Scale(ctx, &scaleset.RunnerScaleSetMessage{
			Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 2},
		})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		releaseOnce()
		t.Fatalf("a blocked job patch delayed the next scale decision: %v", <-done)
	}
	require.EqualValues(t, 2, scales.Load())
	releaseOnce()
}

func TestScaleAppliesBackpressureWhenJobQueueIsFull(t *testing.T) {
	var patches atomic.Int32
	started := make(chan struct{}, jobWorkerCount)
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	w := newAsyncScaler(t, 1, -1, func(out http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ephemeralrunnersets/") {
			writeJSON(out)
			return
		}
		patches.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			writeJSON(out)
		case <-r.Context().Done():
		}
	})
	t.Cleanup(releaseOnce)

	message := &scaleset.RunnerScaleSetMessage{
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: jobWorkerCount + 1},
	}
	for i := range jobWorkerCount + 1 {
		message.JobStartedMessages = append(message.JobStartedMessages, &scaleset.JobStarted{
			RunnerName: fmt.Sprintf("runner-%d", i),
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.Scale(ctx, message))
	for range jobWorkerCount {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not start their patches")
		}
	}
	require.Len(t, w.jobs, 1)

	acquired := make(chan struct{}, 1)
	w.messageClient = acquiringClient{acquire: func(_ context.Context, ids []int64) ([]int64, error) {
		acquired <- struct{}{}
		return ids, nil
	}}
	blockedCtx, cancelBlocked := context.WithCancel(t.Context())
	defer cancelBlocked()
	done := make(chan error, 1)
	go func() {
		done <- w.Scale(blockedCtx, &scaleset.RunnerScaleSetMessage{
			Statistics:           &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: jobWorkerCount + 2},
			JobAvailableMessages: []*scaleset.JobAvailable{{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 2}}},
			JobStartedMessages:   []*scaleset.JobStarted{{RunnerName: "blocked"}},
		})
	}()
	select {
	case <-acquired:
	case <-ctx.Done():
		t.Fatal("job acquisition was blocked behind the full queue")
	}
	select {
	case err := <-done:
		t.Fatalf("Scale returned before queue space was available: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancelBlocked()
	require.ErrorIs(t, <-done, context.Canceled)

	releaseOnce()
	require.NoError(t, w.Close(ctx))
	require.EqualValues(t, jobWorkerCount+1, patches.Load())
}

func TestJobWorkersRetryBeforeDraining(t *testing.T) {
	var attempts atomic.Int32
	w := newAsyncScaler(t, 1, -1, func(out http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ephemeralrunnersets/") {
			writeJSON(out)
			return
		}
		if attempts.Add(1) == 1 {
			http.Error(out, "temporary failure", http.StatusInternalServerError)
			return
		}
		writeJSON(out)
	})
	require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		Statistics:         new(scaleset.RunnerScaleSetStatistic),
		JobStartedMessages: []*scaleset.JobStarted{{RunnerName: "runner"}},
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.Close(ctx))
	require.EqualValues(t, 2, attempts.Load())
}

func TestCloseCancelsBlockedJobPatches(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	w := newAsyncScaler(t, 1, -1, func(out http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ephemeralrunnersets/") {
			writeJSON(out)
			return
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(releaseOnce)
	require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		Statistics:         new(scaleset.RunnerScaleSetStatistic),
		JobStartedMessages: []*scaleset.JobStarted{{RunnerName: "runner"}},
	}))
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job status patch did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, w.Close(ctx), context.DeadlineExceeded)
	releaseOnce()
}
