package scaler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// fakeAcquirer records the request IDs handed to AcquireJobs.
type fakeAcquirer struct {
	mu       sync.Mutex
	acquired [][]int64
	err      error
}

func (f *fakeAcquirer) AcquireJobs(ctx context.Context, requestIDs []int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired = append(f.acquired, requestIDs)
	if f.err != nil {
		return nil, f.err
	}
	return requestIDs, nil
}

func (f *fakeAcquirer) calls() [][]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquired
}

// scaleServer is a stub API server for the resources Scale patches. It serves
// every ephemeral runner as an idle runner, accepts every patch, and reports
// the concurrency it observed.
type scaleServer struct {
	mu sync.Mutex
	// inFlight is the number of requests being served right now, and peak the
	// highest value inFlight ever reached. A peak above one is the only direct
	// evidence that the workers actually overlapped.
	inFlight int
	peak     int
	// runnerSetPatches holds the body of every EphemeralRunnerSet patch.
	runnerSetPatches []string
	// runnerPatchesBeforeRunnerSet counts the ephemeral runner patches that
	// completed before the first runner set patch arrived.
	runnerPatchesBeforeRunnerSet int
	runnerPatches                int

	// hold blocks every ephemeral runner request until it is released, so a test
	// can pin a scale patch against outstanding job started patches. It starts
	// released and is always released again during cleanup, so a failed
	// assertion cannot strand a request and deadlock the server shutdown.
	hold chan struct{}
}

// block makes every subsequent ephemeral runner request wait for release.
func (s *scaleServer) block() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hold = make(chan struct{})
}

// release unblocks the held requests. It is safe to call more than once.
func (s *scaleServer) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.hold:
	default:
		close(s.hold)
	}
}

// waitForHold blocks the calling request until the server is released.
func (s *scaleServer) waitForHold() {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	<-hold
}

func (s *scaleServer) enter() {
	s.mu.Lock()
	s.inFlight++
	s.peak = max(s.peak, s.inFlight)
	s.mu.Unlock()
}

func (s *scaleServer) leave() {
	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()
}

func newScaleScaler(t *testing.T, client JobAcquirer, config Config, workers int) (*Scaler, *scaleServer) {
	t.Helper()

	server := &scaleServer{hold: make(chan struct{})}
	server.release()

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.enter()
		defer server.leave()

		w.Header().Set("Content-Type", "application/json")
		isRunnerSet := strings.Contains(r.URL.Path, "ephemeralrunnersets")

		if !isRunnerSet {
			server.waitForHold()
		}

		switch {
		case isRunnerSet:
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)

			server.mu.Lock()
			if len(server.runnerSetPatches) == 0 {
				server.runnerPatchesBeforeRunnerSet = server.runnerPatches
			}
			server.runnerSetPatches = append(server.runnerSetPatches, string(body))
			server.mu.Unlock()

			require.NoError(t, json.NewEncoder(w).Encode(&v1alpha1.EphemeralRunnerSet{}))

		case r.Method == http.MethodGet:
			require.NoError(t, json.NewEncoder(w).Encode(newTestEphemeralRunner(runnerNameFromPath(r.URL.Path), v1alpha1.EphemeralRunnerPhaseRunning)))

		case r.Method == http.MethodPatch:
			server.mu.Lock()
			server.runnerPatches++
			server.mu.Unlock()
			require.NoError(t, json.NewEncoder(w).Encode(newTestEphemeralRunner(runnerNameFromPath(r.URL.Path), v1alpha1.EphemeralRunnerPhaseRunning)))

		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(httpServer.Close)
	// Registered last so it runs first: a failed assertion must not leave a
	// request parked inside the handler while Close waits for it.
	t.Cleanup(server.release)

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: httpServer.URL, QPS: -1})
	require.NoError(t, err)

	return &Scaler{
		clientset:     clientset,
		client:        client,
		config:        config,
		targetRunners: -1,
		patchSeq:      -1,
		logger:        discardLogger,
		metrics:       metrics.Discard,
		workers:       workers,
	}, server
}

// runnerNameFromPath extracts the ephemeral runner name from a request path of
// the form .../ephemeralrunners/<name>[/status].
func runnerNameFromPath(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/status"), "/")
	return parts[len(parts)-1]
}

func jobStarted(i int) *scaleset.JobStarted {
	return &scaleset.JobStarted{
		RunnerName: fmt.Sprintf("runner-%d", i),
		JobMessageBase: scaleset.JobMessageBase{
			OwnerName:       "actions",
			RepositoryName:  "actions-runner-controller",
			JobID:           strconv.Itoa(i),
			RunnerRequestID: int64(i),
		},
	}
}

func defaultConfig() Config {
	return Config{
		EphemeralRunnerSetNamespace: "test-ns",
		EphemeralRunnerSetName:      "test-ers",
		MinRunners:                  0,
		MaxRunners:                  100,
	}
}

// TestScale_AcquiresAvailableJobs pins the responsibility the listener handed
// over: it no longer acquires on the scaler's behalf, so a job that Scale does
// not pass to AcquireJobs is simply never assigned.
func TestScale_AcquiresAvailableJobs(t *testing.T) {
	acquirer := &fakeAcquirer{}
	w, _ := newScaleScaler(t, acquirer, defaultConfig(), defaultWorkers)

	err := w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		MessageID: 1,
		JobAvailableMessages: []*scaleset.JobAvailable{
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 1}},
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 2}},
		},
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 2},
	})
	require.NoError(t, err)

	require.Equal(t, [][]int64{{1, 2}}, acquirer.calls())
}

func TestScale_NoAcquireCallWithoutAvailableJobs(t *testing.T) {
	acquirer := &fakeAcquirer{}
	w, _ := newScaleScaler(t, acquirer, defaultConfig(), defaultWorkers)

	require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		MessageID:  1,
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 0},
	}))

	assert.Empty(t, acquirer.calls())
}

// TestScale_HandlesJobStartedConcurrently asserts the point of the worker pool:
// the per-event patches overlap instead of being issued one at a time.
func TestScale_HandlesJobStartedConcurrently(t *testing.T) {
	const jobs = 8

	w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), jobs)

	// Hold every ephemeral runner request until all of them have arrived, so the
	// assertion below measures real overlap rather than a lucky interleaving.
	// The release is bounded so a scaler that serialises the batch fails the
	// assertion instead of hanging the test.
	server.block()
	go func() {
		deadline := time.After(5 * time.Second)
		for {
			server.mu.Lock()
			inFlight := server.inFlight
			server.mu.Unlock()
			if inFlight >= jobs {
				server.release()
				return
			}
			select {
			case <-deadline:
				server.release()
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	msg := &scaleset.RunnerScaleSetMessage{
		MessageID:  1,
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: jobs},
	}
	for i := range jobs {
		msg.JobStartedMessages = append(msg.JobStartedMessages, jobStarted(i))
	}

	require.NoError(t, w.Scale(t.Context(), msg))

	server.mu.Lock()
	peak := server.peak
	patches := server.runnerPatches
	server.mu.Unlock()

	assert.Equal(t, jobs, patches, "every job started event is patched")
	assert.Greater(t, peak, 1, "job started patches are issued concurrently")
}

// TestScale_WorkersBoundConcurrency pins that the worker count is a real limit,
// not a hint: the scaler must not fan a large batch out into an unbounded
// number of simultaneous API calls.
func TestScale_WorkersBoundConcurrency(t *testing.T) {
	const workers = 2

	w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), workers)

	msg := &scaleset.RunnerScaleSetMessage{
		MessageID:  1,
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 20},
	}
	for i := range 20 {
		msg.JobStartedMessages = append(msg.JobStartedMessages, jobStarted(i))
	}

	require.NoError(t, w.Scale(t.Context(), msg))

	server.mu.Lock()
	peak := server.peak
	server.mu.Unlock()

	// workers event slots plus the scaling worker, which runs alongside them
	// because this message scales up.
	assert.LessOrEqual(t, peak, workers+1)
}

// TestScale_ScaleDownWaitsForJobStarted pins the ordering the runner set
// controller depends on. It skips a runner during scale down only when that
// runner already carries a job request ID, so a patch that lowers the replica
// count must not be published while job started patches are still outstanding.
func TestScale_ScaleDownWaitsForJobStarted(t *testing.T) {
	w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), defaultWorkers)

	// Establish a target of 4 so the next message scales down.
	require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		MessageID:  1,
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 4},
	}))
	require.Equal(t, 4, w.targetRunners)

	server.mu.Lock()
	server.runnerSetPatches = nil
	server.mu.Unlock()

	msg := &scaleset.RunnerScaleSetMessage{
		MessageID:          2,
		Statistics:         &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 1},
		JobStartedMessages: []*scaleset.JobStarted{jobStarted(0), jobStarted(1)},
	}

	require.NoError(t, w.Scale(t.Context(), msg))

	server.mu.Lock()
	defer server.mu.Unlock()

	assert.Equal(t, 1, w.targetRunners)
	require.Len(t, server.runnerSetPatches, 1)
	assert.Equal(t, 2, server.runnerPatchesBeforeRunnerSet,
		"the scale down patch is published only after every job started patch landed")
}

// TestScale_ScaleUpRunsAlongsideJobStarted is the counterpart: a patch that
// cannot delete anything is published without waiting for the event workers.
func TestScale_ScaleUpRunsAlongsideJobStarted(t *testing.T) {
	w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), defaultWorkers)

	// Block the ephemeral runner requests so the scale patch can only land first
	// if it genuinely does not wait for them.
	server.block()
	defer server.release()

	done := make(chan error, 1)
	go func() {
		done <- w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
			MessageID:          1,
			Statistics:         &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 5},
			JobStartedMessages: []*scaleset.JobStarted{jobStarted(0), jobStarted(1)},
		})
	}()

	require.Eventually(t, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.runnerSetPatches) == 1
	}, 10*time.Second, time.Millisecond, "scale up patch is published without waiting for job started patches")

	server.release()
	require.NoError(t, <-done)

	server.mu.Lock()
	defer server.mu.Unlock()
	assert.Equal(t, 0, server.runnerPatchesBeforeRunnerSet)
	assert.Equal(t, 2, server.runnerPatches)
}

// TestScale_NilMessage covers the long poll timing out. The listener stopped
// caching statistics, so the scaler has to keep converging from the last ones
// it saw, and must not patch at all before it has seen any.
func TestScale_NilMessage(t *testing.T) {
	t.Run("without prior statistics", func(t *testing.T) {
		w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), defaultWorkers)

		require.NoError(t, w.Scale(t.Context(), nil))

		server.mu.Lock()
		defer server.mu.Unlock()
		assert.Empty(t, server.runnerSetPatches)
	})

	t.Run("republishes the last statistics", func(t *testing.T) {
		w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), defaultWorkers)

		require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
			MessageID:  1,
			Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3},
		}))
		require.NoError(t, w.Scale(t.Context(), nil))

		server.mu.Lock()
		defer server.mu.Unlock()
		require.Len(t, server.runnerSetPatches, 2)
		assert.Equal(t, 3, w.targetRunners)
	})
}

// TestScale_AcquireFailureIsNotAcked pins that a failed acquire aborts the
// whole message. The listener acks only on a nil return, so returning the error
// is what gets the jobs redelivered rather than dropped.
func TestScale_AcquireFailureIsNotAcked(t *testing.T) {
	acquirer := &fakeAcquirer{err: assert.AnError}
	w, server := newScaleScaler(t, acquirer, defaultConfig(), defaultWorkers)

	err := w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		MessageID:            1,
		JobAvailableMessages: []*scaleset.JobAvailable{{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 1}}},
		Statistics:           &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 1},
	})
	require.Error(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	assert.Empty(t, server.runnerSetPatches, "nothing is published when the jobs were never acquired")
}

func TestEffectiveWorkerCount(t *testing.T) {
	workers := 4

	tests := []struct {
		name   string
		config *v1alpha1.ScalerConfig
		want   int
	}{
		{name: "nil config", config: nil, want: defaultWorkers},
		{name: "unset", config: &v1alpha1.ScalerConfig{}, want: defaultWorkers},
		{name: "configured", config: &v1alpha1.ScalerConfig{Workers: &workers}, want: workers},
		{name: "zero falls back", config: &v1alpha1.ScalerConfig{Workers: new(int)}, want: defaultWorkers},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, effectiveWorkerCount(tt.config, discardLogger))
		})
	}
}

// TestWithMetrics_NilRecorder pins that a listener started without a metrics
// server still gets a usable recorder. main leaves the exporter nil in that
// case, and the scaler now records the metrics the listener used to, so a nil
// slipping through would panic on the first job event.
func TestWithMetrics_NilRecorder(t *testing.T) {
	var exporter metrics.ServerExporter // nil, as main leaves it when metrics are off

	w := &Scaler{}
	WithMetrics(exporter)(w)
	require.NoError(t, w.applyDefaults())

	require.Equal(t, metrics.Discard, w.metrics)
	assert.NotPanics(t, func() {
		w.metrics.RecordJobStarted(&scaleset.JobStarted{})
		w.metrics.RecordStatistics(&scaleset.RunnerScaleSetStatistic{})
		w.metrics.RecordDesiredRunners(1)
	})
}
