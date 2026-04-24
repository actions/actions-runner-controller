package capacity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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
	}

	if hudRows != nil {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(hudRows)
		}))
		t.Cleanup(srv.Close)

		hc := &HUDClient{
			token:  "test",
			client: &http.Client{Timeout: 5 * time.Second},
		}
		hc.client.Transport = &rewriteTransport{
			base:   http.DefaultTransport,
			target: srv.URL,
		}
		m.hudClient = hc
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

	m.reconcile(context.Background())

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

	m.reconcile(context.Background())

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

	m.reconcile(context.Background())

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
	m.reconcile(ctx)
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
	m.reconcile(ctx)

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
	m.reconcile(ctx)
	pairs, err := m.placeholders.ListPairs(ctx)
	require.NoError(t, err)
	for slotID := range pairs {
		setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodPending)
	}
	assert.Equal(t, 10, countPods(t, cs, "test-ns"))

	// Scale down to 2 pairs.
	m.config.ProactiveCapacity = 2
	m.reconcile(ctx)

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
		}
		_, err := cs.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	m.reconcile(ctx)

	// Make placeholder pairs Running.
	pairs, _ := m.placeholders.ListPairs(ctx)
	for slotID := range pairs {
		setPodsPhase(t, cs, ctx, "test-ns", slotID, corev1.PodRunning)
	}

	m.reconcile(ctx)

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

	// Wire up the failing HUD server.
	hc := &HUDClient{
		token:  "test",
		client: &http.Client{Timeout: 5 * time.Second},
	}
	hc.client.Transport = &rewriteTransport{
		base:   http.DefaultTransport,
		target: srv.URL,
	}
	m.hudClient = hc

	m.reconcile(context.Background())

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

	m.reconcile(context.Background())
	assert.Equal(t, 4, countPods(t, cs, "test-ns"))
	assert.Equal(t, int32(0), maxVal.Load(),
		"no running pairs after first reconcile -> capacity 0")

	// Second reconcile should not change anything.
	m.reconcile(context.Background())
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

	m.reconcile(ctx)

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
		}
		_, err := cs.CoreV1().Pods("test-ns").Create(ctx, pod, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	m.reconcile(ctx)

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
