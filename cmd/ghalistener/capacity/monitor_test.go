package capacity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
)

// newTestMonitor creates a Monitor wired to a fake K8s client (with
// working DeleteCollection) and an optional HUD test server. If hudRows
// is nil, no HUD server is created and the monitor falls back to
// proactiveCapacity only.
func newTestMonitor(t *testing.T, cfg Config, hudRows []QueuedJobsForRunner) (*Monitor, *fake.Clientset, *atomic.Int32) {
	t.Helper()
	if cfg.Namespace == "" {
		cfg.Namespace = "test-ns"
	}
	if cfg.ScaleSetName == "" {
		cfg.ScaleSetName = "test-sset"
	}
	cs := newFakeClientset()

	var maxRunnersVal atomic.Int32
	setMax := func(v int) { maxRunnersVal.Store(int32(v)) }

	logger := discardLogger
	listenerID := "test-listener"

	m := &Monitor{
		config: cfg,
		placeholders: NewPlaceholderManager(
			cs, cfg.Namespace, listenerID, cfg, logger,
		),
		clientset:     cs,
		setMaxRunners: setMax,
		logger:        logger.With("component", "capacity-monitor"),
		recorder:      metrics.DiscardCapacity,
	}

	if hudRows != nil {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(hudRows)
		}))
		t.Cleanup(srv.Close)

		m.hudClient = NewHUDClient(srv.URL, "test")
		cfg.HUDAPIToken = "test"
		m.config = cfg
	}

	return m, cs, &maxRunnersVal
}

func countPods(t *testing.T, cs *fake.Clientset, ns string) int {
	t.Helper()
	pods, err := cs.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	return len(pods.Items)
}

func TestReconcile_ZeroQueued_CreatesProactiveCapacity(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  3,
		MaxRunners:         10,
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)

	m.reconcileProvisioning(context.Background())
	m.reconcileReporting(context.Background())

	// 3 pairs = 6 pods.
	assert.Equal(t, 6, countPods(t, cs, "test-ns"))
	// No running pairs (fake client pods start with empty phase), so
	// setMaxRunners(min(0+0, 10)) = 0.
	assert.Equal(t, int32(0), maxVal.Load())
}

func TestReconcile_QueuedJobs_AddsToProactiveCapacity(t *testing.T) {
	hudRows := []QueuedJobsForRunner{
		{RunnerLabel: "linux.2xlarge", NumQueuedJobs: 5},
	}
	cfg := Config{
		ProactiveCapacity:  2,
		MaxRunners:         20,
		ScaleSetLabels:     []string{"linux.2xlarge"},
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, hudRows)

	m.reconcileProvisioning(context.Background())
	m.reconcileReporting(context.Background())

	// desired = proactive(2) + queued(5) = 7 pairs = 14 pods.
	assert.Equal(t, 14, countPods(t, cs, "test-ns"))
	// No running pairs/runners yet, so capacity = 0.
	assert.Equal(t, int32(0), maxVal.Load())
}

func TestReconcile_MaxRunnersCap(t *testing.T) {
	hudRows := []QueuedJobsForRunner{
		{RunnerLabel: "linux.2xlarge", NumQueuedJobs: 50},
	}
	cfg := Config{
		ProactiveCapacity:  5,
		MaxRunners:         10,
		ScaleSetLabels:     []string{"linux.2xlarge"},
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, hudRows)

	m.reconcileProvisioning(context.Background())
	m.reconcileReporting(context.Background())

	// desired = min(5+50, 10) = 10 pairs = 20 pods.
	assert.Equal(t, 20, countPods(t, cs, "test-ns"))
	// No running pairs/runners, so reported capacity = 0 (capped at MaxRunners=10).
	assert.Equal(t, int32(0), maxVal.Load())
}

func TestReconcile_ScaleDown_PrefersPendingDeletion(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  5,
		MaxRunners:         20,
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)
	ctx := context.Background()

	// First reconcile creates 5 pairs.
	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)
	assert.Equal(t, 10, countPods(t, cs, "test-ns"))

	// Make 2 pairs Running, leave 3 Pending.
	pairs, err := m.placeholders.ListPairs(ctx)
	require.NoError(t, err)
	runningCount := 0
	for slotID := range pairs {
		if runningCount < 2 {
			setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodRunning)
			runningCount++
		} else {
			setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodPending)
		}
	}

	// Scale down to 2 pairs.
	m.config.ProactiveCapacity = 2
	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// Should have deleted EXACTLY 3 pairs (the 3 Pending ones), not more
	// (regression test for adjustPairs double-delete counter bug).
	assert.Equal(t, 4, countPods(t, cs, "test-ns"),
		"must delete exactly 3 pairs (the Pending ones), leaving the 2 Running pairs")

	// Verify the remaining pods are the Running ones.
	remaining, err := m.placeholders.ListPairs(ctx)
	require.NoError(t, err)
	assert.Len(t, remaining, 2, "exactly 2 pairs remain")
	for _, pair := range remaining {
		assert.True(t, pair.BothRunning(), "remaining pairs should be running")
	}
	// 2 running pairs, 0 running runners, capped at MaxRunners=20.
	assert.Equal(t, int32(2), maxVal.Load())
}

// Regression test for the adjustPairs double-delete bug: when scaling
// from 5 to 2 with all pairs Pending, ensure exactly 3 are deleted —
// not over-deleted (the bug double-counted slots in pass 2 when pass 1
// had already deleted them).
func TestAdjustPairs_NoDoubleDelete_AllPending(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  5,
		MaxRunners:         20,
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, _ := newTestMonitor(t, cfg, nil)
	ctx := context.Background()

	// Create 5 pairs, all Pending.
	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)
	pairs, err := m.placeholders.ListPairs(ctx)
	require.NoError(t, err)
	for slotID := range pairs {
		setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodPending)
	}
	assert.Equal(t, 10, countPods(t, cs, "test-ns"))

	// Scale down to 2 pairs.
	m.config.ProactiveCapacity = 2
	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// EXACTLY 3 pairs should be deleted (6 pods removed -> 4 remaining).
	// If the double-delete bug returned, fewer (or wrong) pairs would
	// remain because pass 2 would skip slots already deleted by pass 1
	// without correctly accounting for them.
	assert.Equal(t, 4, countPods(t, cs, "test-ns"),
		"exactly 3 pairs deleted, 2 remain")
}

func TestReconcile_SetMaxRunners_CapAtMaxRunners(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  2,
		MaxRunners:         5,
		ScaleSetName:       "test-sset",
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)
	ctx := context.Background()

	// Create some "real" ephemeral runner pods.
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "runner-" + string(rune('a'+i)),
				Namespace: "test-ns",
				Labels: map[string]string{
					"actions-ephemeral-runner": "True",
					labelScaleSet:              "test-sset",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "runner", Image: "runner:latest"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
		_, err := cs.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// Make placeholder pairs Running.
	pairs, _ := m.placeholders.ListPairs(ctx)
	for slotID := range pairs {
		setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodRunning)
	}

	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// capacity = min(runningRunners(3) + runningPairs(2), maxRunners(5)) = 5.
	assert.Equal(t, int32(5), maxVal.Load())
}

func TestReconcile_HUDAPIFailure_FallsBackToProactiveOnly(t *testing.T) {
	// HUD server returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := Config{
		ProactiveCapacity:  3,
		MaxRunners:         20,
		ScaleSetLabels:     []string{"linux.2xlarge"},
		HUDAPIToken:        "test",
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)

	m.hudClient = NewHUDClient(srv.URL, "test")

	m.reconcileProvisioning(context.Background())
	m.reconcileReporting(context.Background())

	// Falls back to proactiveCapacity only: 3 pairs = 6 pods.
	assert.Equal(t, 6, countPods(t, cs, "test-ns"))
	// No running pairs, so capacity = 0 (still capped at MaxRunners=20).
	assert.Equal(t, int32(0), maxVal.Load())
}

func TestReconcile_IdempotentWhenAtDesired(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  2,
		MaxRunners:         10,
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)

	m.reconcileProvisioning(context.Background())
	m.reconcileReporting(context.Background())
	assert.Equal(t, 4, countPods(t, cs, "test-ns"))
	assert.Equal(t, int32(0), maxVal.Load(),
		"no running pairs after first reconcile -> capacity 0")

	// Second reconcile should not change anything.
	m.reconcileProvisioning(context.Background())
	m.reconcileReporting(context.Background())
	assert.Equal(t, 4, countPods(t, cs, "test-ns"))
	assert.Equal(t, int32(0), maxVal.Load(),
		"second reconcile preserves capacity 0")
}

// MaxRunners == 0 means "unlimited" (not "zero capacity"). The monitor
// must create placeholders normally and report capacity = running
// runners + running pairs without capping at 0.
// Regression test for the MaxRunners==0 deadlock bug.
func TestReconcile_MaxRunnersZero_IsUnlimited(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  3,
		MaxRunners:         0, // unlimited
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)
	ctx := context.Background()

	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// 3 pairs = 6 pods (NOT capped at 0).
	assert.Equal(t, 6, countPods(t, cs, "test-ns"),
		"MaxRunners=0 must NOT cap placeholders at 0")
	// No running pairs yet -> capacity = 0 (not because of cap).
	assert.Equal(t, int32(0), maxVal.Load())

	// Make all pairs Running and add some real runner pods.
	pairs, _ := m.placeholders.ListPairs(ctx)
	for slotID := range pairs {
		setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodRunning)
	}
	for i := 0; i < 7; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "runner-" + string(rune('a'+i)),
				Namespace: "test-ns",
				Labels: map[string]string{
					"actions-ephemeral-runner": "True",
					labelScaleSet:              "test-sset",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "r", Image: "x"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}
		_, err := cs.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// capacity = runningRunners(7) + runningPairs(3) = 10, NOT capped.
	assert.Equal(t, int32(10), maxVal.Load(),
		"MaxRunners=0 must NOT cap reported capacity")
	// Still 3 pairs (proactive=3, no queued, no MaxRunners cap).
	pairs, _ = m.placeholders.ListPairs(ctx)
	assert.Len(t, pairs, 3)
}

func TestRunLoop_CancellationCleansUp(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:   1,
		MaxRunners:          5,
		RecalculateInterval: 100 * time.Millisecond,
		ReportInterval:      50 * time.Millisecond,
		PlaceholderTimeout:  5 * time.Minute,
	}
	m, cs, _ := newTestMonitor(t, cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := m.Run(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	// After Run returns, all placeholders should be cleaned up.
	pods, listErr := cs.CoreV1().Pods("test-ns").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Empty(t, pods.Items, "all placeholders cleaned up on shutdown")
}

func TestReporter_IndependentOfProvisioner(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  3,
		MaxRunners:         10,
		PlaceholderTimeout: 5 * time.Minute,
	}
	m, cs, maxVal := newTestMonitor(t, cfg, nil)
	ctx := context.Background()

	// Provisioner creates 3 pairs (all start Pending in fake client).
	m.reconcileProvisioning(ctx)
	assert.Equal(t, 6, countPods(t, cs, "test-ns"))

	// Reporter runs — no Running pairs yet, capacity stays 0.
	m.reconcileReporting(ctx)
	assert.Equal(t, int32(0), maxVal.Load())

	// Simulate pods becoming Running (e.g., Karpenter provisioned nodes).
	pairs, err := m.placeholders.ListPairs(ctx)
	require.NoError(t, err)
	for slotID := range pairs {
		setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodRunning)
	}

	// Reporter picks up Running pairs WITHOUT provisioner running again.
	m.reconcileReporting(ctx)
	assert.Equal(t, int32(3), maxVal.Load(),
		"reporter independently detects Running pairs")
}

func TestRetryWithBackoff_SucceedsOnRetry(t *testing.T) {
	attempts := 0
	err := retryWithBackoff(context.Background(), discardLogger, "test-op", 3, func() error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("transient error")
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestRetryWithBackoff_ExhaustsRetries(t *testing.T) {
	attempts := 0
	err := retryWithBackoff(context.Background(), discardLogger, "test-op", 2, func() error {
		attempts++
		return fmt.Errorf("persistent error")
	})
	assert.Error(t, err)
	assert.Equal(t, 3, attempts) // initial + 2 retries
	assert.Contains(t, err.Error(), "persistent error")
}

func TestRetryWithBackoff_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := retryWithBackoff(ctx, discardLogger, "test-op", 5, func() error {
		return fmt.Errorf("should not matter")
	})
	// First attempt runs (no backoff wait), fails, then backoff select sees ctx.Done()
	assert.ErrorIs(t, err, context.Canceled)
}

// ---- capacity recorder wiring tests ----
//
// These tests assert that the monitor calls the right CapacityRecorder
// methods at the right times. They do NOT exhaustively test every
// metric — the metrics package has its own tests for that. The goal here
// is to catch wiring drift: if the call site moves or is dropped, these
// tests fail.

// fakeCapacityRecorder is a CapacityRecorder that records every call so
// tests can assert which methods were invoked and with what arguments.
type fakeCapacityRecorder struct {
	mu sync.Mutex

	proactiveCapacity    int
	hudEnabled           bool
	queuedJobs           int
	desiredPairs         int
	pairs                int
	runningPairs         int
	advertisedMaxRunners int

	// Map (role, phase) -> latest count.
	placeholderPods map[string]map[string]int

	// Map phase -> last successful time.
	lastSuccess map[string]time.Time

	// Counts of method invocations for assertions.
	setProactiveCapacityCalls    int
	setHUDEnabledCalls           int
	setQueuedJobsCalls           int
	setDesiredPairsCalls         int
	setPairsCalls                int
	setRunningPairsCalls         int
	setPlaceholderPodsCalls      int
	setAdvertisedMaxRunnersCalls int
	setReconcileLastSuccessCalls map[string]int
	observeReconcileCalls        map[string]int
	observeHUDRequestCalls       map[string]int
	incHUDRequestsCalls          map[string]int
	incPairCreatesCalls          map[string]int
	incPairDeletesCalls          map[string]int // key: reason+":"+result
	incReconcileSkipsCalls       map[string]int
}

func newFakeCapacityRecorder() *fakeCapacityRecorder {
	return &fakeCapacityRecorder{
		placeholderPods:              make(map[string]map[string]int),
		lastSuccess:                  make(map[string]time.Time),
		setReconcileLastSuccessCalls: make(map[string]int),
		observeReconcileCalls:        make(map[string]int),
		observeHUDRequestCalls:       make(map[string]int),
		incHUDRequestsCalls:          make(map[string]int),
		incPairCreatesCalls:          make(map[string]int),
		incPairDeletesCalls:          make(map[string]int),
		incReconcileSkipsCalls:       make(map[string]int),
	}
}

func (f *fakeCapacityRecorder) SetProactiveCapacity(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proactiveCapacity = v
	f.setProactiveCapacityCalls++
}
func (f *fakeCapacityRecorder) SetHUDEnabled(b bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hudEnabled = b
	f.setHUDEnabledCalls++
}
func (f *fakeCapacityRecorder) SetQueuedJobs(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queuedJobs = v
	f.setQueuedJobsCalls++
}
func (f *fakeCapacityRecorder) SetDesiredPairs(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desiredPairs = v
	f.setDesiredPairsCalls++
}
func (f *fakeCapacityRecorder) SetPairs(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairs = v
	f.setPairsCalls++
}
func (f *fakeCapacityRecorder) SetRunningPairs(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runningPairs = v
	f.setRunningPairsCalls++
}
func (f *fakeCapacityRecorder) SetPlaceholderPods(role, phase string, v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.placeholderPods[role] == nil {
		f.placeholderPods[role] = make(map[string]int)
	}
	f.placeholderPods[role][phase] = v
	f.setPlaceholderPodsCalls++
}
func (f *fakeCapacityRecorder) SetAdvertisedMaxRunners(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advertisedMaxRunners = v
	f.setAdvertisedMaxRunnersCalls++
}
func (f *fakeCapacityRecorder) SetReconcileLastSuccess(phase string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSuccess[phase] = t
	f.setReconcileLastSuccessCalls[phase]++
}
func (f *fakeCapacityRecorder) ObserveReconcileDuration(phase string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observeReconcileCalls[phase]++
}
func (f *fakeCapacityRecorder) ObserveHUDRequest(result string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observeHUDRequestCalls[result]++
}
func (f *fakeCapacityRecorder) IncHUDRequests(result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incHUDRequestsCalls[result]++
}
func (f *fakeCapacityRecorder) IncPairCreates(result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incPairCreatesCalls[result]++
}
func (f *fakeCapacityRecorder) IncPairDeletes(reason, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incPairDeletesCalls[reason+":"+result]++
}
func (f *fakeCapacityRecorder) IncReconcileSkips(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incReconcileSkipsCalls[reason]++
}

var _ metrics.CapacityRecorder = (*fakeCapacityRecorder)(nil)

// newTestMonitorWithRecorder is like newTestMonitor but injects a custom
// recorder so the test can assert metric calls. The fixture mirrors the
// shape of newTestMonitor exactly — only the recorder changes.
func newTestMonitorWithRecorder(
	t *testing.T,
	cfg Config,
	rec metrics.CapacityRecorder,
) (*Monitor, *fake.Clientset, *atomic.Int32) {
	t.Helper()
	m, cs, val := newTestMonitor(t, cfg, nil)
	m.recorder = rec
	return m, cs, val
}

// TestProvisioner_RecorderWiring exercises one full provisioner cycle and
// asserts the gauges + last-success-timestamp landed.
func TestProvisioner_RecorderWiring(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  3,
		MaxRunners:         10,
		PlaceholderTimeout: 5 * time.Minute,
	}
	rec := newFakeCapacityRecorder()
	m, _, _ := newTestMonitorWithRecorder(t, cfg, rec)

	m.reconcileProvisioning(context.Background())

	rec.mu.Lock()
	defer rec.mu.Unlock()

	assert.Equal(t, 3, rec.desiredPairs, "desired pairs should be set to ProactiveCapacity")
	assert.GreaterOrEqual(t, rec.setDesiredPairsCalls, 1)

	assert.Equal(t, 0, rec.pairs, "no pairs existed before this cycle")
	assert.GreaterOrEqual(t, rec.setPairsCalls, 1)

	assert.Equal(t, 1, rec.setReconcileLastSuccessCalls[reconcilePhaseProvisioner],
		"provisioner success timestamp must be recorded once on the success path")
	assert.False(t, rec.lastSuccess[reconcilePhaseProvisioner].IsZero())

	// Duration histogram is observed on every cycle (success or fail).
	assert.Equal(t, 1, rec.observeReconcileCalls[reconcilePhaseProvisioner])

	// Created 3 pairs successfully.
	assert.Equal(t, 3, rec.incPairCreatesCalls[resultSuccess])
}

// TestReporter_RecorderWiring exercises one reporter cycle and asserts
// the advertised-max-runners gauge + last-success-timestamp landed.
func TestReporter_RecorderWiring(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  2,
		MaxRunners:         10,
		PlaceholderTimeout: 5 * time.Minute,
	}
	rec := newFakeCapacityRecorder()
	m, _, _ := newTestMonitorWithRecorder(t, cfg, rec)

	m.reconcileReporting(context.Background())

	rec.mu.Lock()
	defer rec.mu.Unlock()

	assert.Equal(t, 0, rec.advertisedMaxRunners,
		"no running pairs/runners yet, capacity is 0")
	assert.GreaterOrEqual(t, rec.setAdvertisedMaxRunnersCalls, 1)

	assert.Equal(t, 1, rec.setReconcileLastSuccessCalls[reconcilePhaseReporter],
		"reporter success timestamp must be recorded once on the success path")
	assert.False(t, rec.lastSuccess[reconcilePhaseReporter].IsZero())

	assert.Equal(t, 1, rec.observeReconcileCalls[reconcilePhaseReporter])
}

// TestProvisioner_ListPairsError_RecordsSkip simulates a list-pairs error
// in the provisioner and asserts the skip counter is incremented and
// the success timestamp is NOT advanced.
func TestProvisioner_ListPairsError_RecordsSkip(t *testing.T) {
	cfg := Config{
		ProactiveCapacity:  1,
		MaxRunners:         10,
		PlaceholderTimeout: 5 * time.Minute,
	}
	rec := newFakeCapacityRecorder()
	m, cs, _ := newTestMonitorWithRecorder(t, cfg, rec)

	// Make every Pods().List() call fail. retryWithBackoff will burn
	// through all attempts, then the provisioner takes the skip path.
	cs.PrependReactor("list", "pods",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("synthetic list pods error")
		},
	)

	// Use a context with a short deadline so the retry backoffs (1s, 2s, 4s)
	// are aborted quickly via ctx.Done() instead of running for 7+ seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	m.reconcileProvisioning(ctx)

	rec.mu.Lock()
	defer rec.mu.Unlock()

	assert.Equal(t, 1, rec.incReconcileSkipsCalls[skipReasonProvisionerListPairs],
		"list-pairs error must record a provisioner_list_pairs skip")
	assert.Equal(t, 0, rec.setReconcileLastSuccessCalls[reconcilePhaseProvisioner],
		"success timestamp must NOT be set on the skip path")
	// Duration is still observed even on the skip path.
	assert.Equal(t, 1, rec.observeReconcileCalls[reconcilePhaseProvisioner])
}
