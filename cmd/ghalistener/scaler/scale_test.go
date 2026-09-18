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
	// onAcquire runs before the call is recorded, so a test can observe what had
	// already happened by the time the scaler reached the acquire step.
	onAcquire func()
}

func (f *fakeAcquirer) AcquireJobs(ctx context.Context, requestIDs []int64) ([]int64, error) {
	if f.onAcquire != nil {
		f.onAcquire()
	}

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

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: httpServer.URL, QPS: -1})
	require.NoError(t, err)

	w := &Scaler{
		// The split matters in production, where the two clients carry separate
		// rate limits. The tests only care about ordering, so one unthrottled
		// client backs both.
		scaleClientset: clientset,
		jobClientset:   clientset,
		client:         client,
		config:         config,
		targetRunners:  -1,
		patchSeq:       -1,
		logger:         discardLogger,
		metrics:        metrics.Discard,
		workers:        workers,
		jobs:           newJobQueue(),
	}
	w.startJobWorkers()

	// Registered after the server so it runs first: the workers have to be gone
	// before the server they are calling goes away.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = w.Close(ctx)
	})
	// Registered last so it runs first of all: a failed assertion must not leave
	// a request parked inside the handler while the workers are being drained.
	t.Cleanup(server.release)

	return w, server
}

// drain waits for every queued job started event to be patched. Scale
// deliberately does not wait for them, so any assertion about job patches has
// to ask for the drain explicitly.
func drain(t *testing.T, w *Scaler) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, w.Close(ctx))
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
	drain(t, w)

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
	drain(t, w)

	server.mu.Lock()
	peak := server.peak
	server.mu.Unlock()

	assert.LessOrEqual(t, peak, workers, "the pool bound is a real limit")
}

// TestScale_PublishesDesiredCountFirst pins the ordering that matters for
// scale up latency: the replica patch is the one that creates runners, so it
// goes out before any of the job event bookkeeping patches.
//
// It is asserted for a scale down as well as a scale up. Nothing requires the
// job started patches to land first: the runner set controller only deletes
// idle runners under Spec.PatchID == 0, which setDesiredWorkerState never emits
// together with a falling target.
func TestScale_PublishesDesiredCountFirst(t *testing.T) {
	tests := []struct {
		name     string
		settleAt int
		assigned int
	}{
		{name: "scale up", settleAt: 1, assigned: 5},
		{name: "steady", settleAt: 5, assigned: 5},
		{name: "scale down", settleAt: 5, assigned: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), defaultWorkers)

			require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
				MessageID:  1,
				Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: tt.settleAt},
			}))
			require.Equal(t, tt.settleAt, w.targetRunners)

			server.mu.Lock()
			server.runnerSetPatches = nil
			server.runnerPatches = 0
			server.runnerPatchesBeforeRunnerSet = 0
			server.mu.Unlock()

			require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
				MessageID:          2,
				Statistics:         &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: tt.assigned},
				JobStartedMessages: []*scaleset.JobStarted{jobStarted(0), jobStarted(1)},
			}))

			// Safe to assert before the drain: the events are not queued until
			// the scale patch has already returned, so no job patch can precede
			// it however the workers are scheduled.
			server.mu.Lock()
			require.Len(t, server.runnerSetPatches, 1)
			assert.Equal(t, 0, server.runnerPatchesBeforeRunnerSet,
				"the desired count is published before any job event patch")
			server.mu.Unlock()

			drain(t, w)

			server.mu.Lock()
			defer server.mu.Unlock()
			assert.Equal(t, tt.assigned, w.targetRunners)
			assert.Equal(t, 2, server.runnerPatches)
		})
	}
}

// TestScale_DoesNotWaitForJobStartedPatches is the property the background queue
// exists for. The desired count is what new jobs wait on; the job started
// patches are bookkeeping, and at two API calls each a full batch of them used
// to sit between one scale decision and the next.
//
// The server holds every ephemeral runner request for the whole test, so a
// scaler that still patched them inline could not return at all.
func TestScale_DoesNotWaitForJobStartedPatches(t *testing.T) {
	const jobs = 20

	w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), defaultWorkers)

	// Never released by the test itself; cleanup releases it before draining.
	server.block()

	msg := &scaleset.RunnerScaleSetMessage{
		MessageID:  1,
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: jobs},
	}
	for i := range jobs {
		msg.JobStartedMessages = append(msg.JobStartedMessages, jobStarted(i))
	}

	done := make(chan error, 1)
	go func() { done <- w.Scale(t.Context(), msg) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Scale blocked on job started patches that cannot complete")
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	assert.Len(t, server.runnerSetPatches, 1,
		"the desired count is published even while every job patch is stuck")
	assert.Equal(t, 0, server.runnerPatches)
}

// TestScale_AcquiresAfterPublishingDesiredCount pins the other half of the
// reordering. Acquiring is a round trip to the Actions service, and the scale
// decision does not depend on its result: it is derived from the statistics the
// service already put in the message. Publishing first therefore costs nothing
// and keeps that round trip off the path new runners wait on.
func TestScale_AcquiresAfterPublishingDesiredCount(t *testing.T) {
	acquirer := &fakeAcquirer{}
	w, server := newScaleScaler(t, acquirer, defaultConfig(), defaultWorkers)

	var runnerSetPatchesAtAcquire int
	acquirer.onAcquire = func() {
		server.mu.Lock()
		defer server.mu.Unlock()
		runnerSetPatchesAtAcquire = len(server.runnerSetPatches)
	}

	require.NoError(t, w.Scale(t.Context(), &scaleset.RunnerScaleSetMessage{
		MessageID:            1,
		JobAvailableMessages: []*scaleset.JobAvailable{{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 1}}},
		Statistics:           &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 1},
	}))

	assert.Equal(t, 1, runnerSetPatchesAtAcquire,
		"the desired count is published before the acquire round trip")
}

// TestScale_CloseDrainsQueuedJobStartedEvents covers the cost of not waiting.
// The listener acks a message as soon as Scale returns, so by the time these
// patches run the service already believes they were recorded and will never
// redeliver them. Shutting down has to finish them rather than drop them.
func TestScale_CloseDrainsQueuedJobStartedEvents(t *testing.T) {
	const jobs = 16

	// Two workers against sixteen events, so the queue is guaranteed to still
	// hold work when Close is called.
	w, server := newScaleScaler(t, &fakeAcquirer{}, defaultConfig(), 2)

	msg := &scaleset.RunnerScaleSetMessage{
		MessageID:  1,
		Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: jobs},
	}
	for i := range jobs {
		msg.JobStartedMessages = append(msg.JobStartedMessages, jobStarted(i))
	}
	require.NoError(t, w.Scale(t.Context(), msg))

	drain(t, w)

	server.mu.Lock()
	defer server.mu.Unlock()
	assert.Equal(t, jobs, server.runnerPatches,
		"a clean shutdown patches everything it accepted from an acked message")
}

// TestScale_ScaleDownNeverPublishesPatchIDZero is the invariant the ordering
// above relies on. Patch ID 0 is the only one the runner set controller acts on
// to delete idle runners, so a falling target must never carry it -- otherwise
// a runner whose job started patch has not landed yet would look idle and be
// eligible for deletion.
func TestScale_ScaleDownNeverPublishesPatchIDZero(t *testing.T) {
	for _, minRunners := range []int{0, 1, 2} {
		config := defaultConfig()
		config.MinRunners = minRunners

		w, _ := newScaleScaler(t, &fakeAcquirer{}, config, defaultWorkers)

		previous := -1
		for _, assigned := range []int{0, 3, 3, 1, 0, 0, 4, 2, 0} {
			patchID := w.setDesiredWorkerState(assigned)
			if previous >= 0 && w.targetRunners < previous {
				assert.NotEqual(t, 0, patchID,
					"minRunners=%d target %d->%d", minRunners, previous, w.targetRunners)
			}
			previous = w.targetRunners
		}
	}
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
	// The desired count is published ahead of the acquire, so it survives the
	// failure. That is safe, and deliberate: it is derived from the statistics
	// the service put in the message rather than from anything the acquire
	// returns, and republishing it when the message is redelivered is a no-op.
	assert.Len(t, server.runnerSetPatches, 1,
		"the desired count is published before the acquire, so it stands even when the acquire fails")
}

func TestEffectiveScaleRateLimiterConfig(t *testing.T) {
	qps, burst := 3, 7

	tests := []struct {
		name      string
		config    *v1alpha1.ScalerConfig
		wantQPS   int
		wantBurst int
	}{
		{name: "nil config", config: nil, wantQPS: defaultScaleQPS, wantBurst: defaultScaleBurst},
		{name: "unset", config: &v1alpha1.ScalerConfig{}, wantQPS: defaultScaleQPS, wantBurst: defaultScaleBurst},
		{
			name:      "configured",
			config:    &v1alpha1.ScalerConfig{ScaleQPS: &qps, ScaleBurst: &burst},
			wantQPS:   qps,
			wantBurst: burst,
		},
		{
			name:      "zero falls back",
			config:    &v1alpha1.ScalerConfig{ScaleQPS: new(int), ScaleBurst: new(int)},
			wantQPS:   defaultScaleQPS,
			wantBurst: defaultScaleBurst,
		},
		{
			name:      "independent of the job client budget",
			config:    &v1alpha1.ScalerConfig{QPS: &qps, Burst: &burst},
			wantQPS:   defaultScaleQPS,
			wantBurst: defaultScaleBurst,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotQPS, gotBurst := effectiveScaleRateLimiterConfig(tt.config, discardLogger)
			assert.Equal(t, tt.wantQPS, gotQPS)
			assert.Equal(t, tt.wantBurst, gotBurst)
		})
	}
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
