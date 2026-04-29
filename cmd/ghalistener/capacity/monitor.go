package capacity

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
)

const (
	reporterMaxRetries    = 2 // 1s + 2s = 3s, within 5s reporter interval
	provisionerMaxRetries = 3 // 1s + 2s + 4s = 7s, within 30s provisioner interval
)

// Phase / reason / result label values for capacity recorder calls.
// Centralised here (and not in metrics.go) so the call sites in this
// package read as plain Go identifiers instead of bare strings — and
// so a typo at a call site is a compile-time error.
const (
	reconcilePhaseProvisioner = "provisioner"
	reconcilePhaseReporter    = "reporter"

	resultSuccess = "success"
	resultError   = "error"

	deleteReasonOrphan  = "orphan"
	deleteReasonTimeout = "timeout"
	deleteReasonExcess  = "excess"

	skipReasonProvisionerListPairs = "provisioner_list_pairs"
	skipReasonReporterListPairs    = "reporter_list_pairs"
	skipReasonReporterCountRunners = "reporter_count_runners"
	skipReasonHUDAPIFailed         = "hud_api_failed"

	rolePlaceholderRunnerLabel   = "runner"
	rolePlaceholderWorkflowLabel = "workflow"
)

// allPlaceholderRoles enumerates the role label values emitted on the
// gha_capacity_placeholder_pods gauge. Iteration order is fixed for
// deterministic emission of zero-valued series.
var allPlaceholderRoles = []string{
	rolePlaceholderRunnerLabel,
	rolePlaceholderWorkflowLabel,
}

// allPlaceholderPhases enumerates the phase label values emitted on the
// gha_capacity_placeholder_pods gauge. We MUST always emit a value for
// every (role, phase) combination — including zeros — so that gauges
// for empty phases visibly decrement to 0 instead of sticking on the
// previous non-zero value.
var allPlaceholderPhases = []string{
	string(corev1.PodPending),
	string(corev1.PodRunning),
	string(corev1.PodFailed),
	string(corev1.PodSucceeded),
	string(corev1.PodUnknown),
}

// Monitor is the capacity monitor goroutine. It creates placeholder
// pod pairs to pre-warm Kubernetes nodes, queries the HUD API for
// queued job counts, and dynamically adjusts the maxRunners value
// reported to GitHub via the listener.
type Monitor struct {
	config        Config
	placeholders  *PlaceholderManager
	hudClient     *HUDClient
	clientset     kubernetes.Interface
	setMaxRunners func(int)
	logger        *slog.Logger
	slotCounter   atomic.Int64 // monotonic counter for unique slot IDs
	recorder      metrics.CapacityRecorder
}

// Option configures a Monitor at construction time. Use WithRecorder to
// inject a non-discard CapacityRecorder.
type Option func(*Monitor)

// WithRecorder sets the metrics recorder. If WithRecorder is not passed
// to New, the monitor uses metrics.DiscardCapacity (no-op) so callers
// (including tests) need not care about metrics wiring.
func WithRecorder(r metrics.CapacityRecorder) Option {
	return func(m *Monitor) {
		if r != nil {
			m.recorder = r
		}
	}
}

// New creates a new capacity Monitor. The setMaxRunners callback is
// typically listener.SetMaxRunners. Returns an error if the supplied
// Config fails validation (e.g. a required env var is missing).
//
// The variadic opts argument keeps the signature backward-compatible:
// existing callers (and tests) can omit options and get the discard
// recorder by default.
func New(
	config Config,
	clientset kubernetes.Interface,
	setMaxRunners func(int),
	logger *slog.Logger,
	opts ...Option,
) (*Monitor, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid capacity monitor config: %w", err)
	}
	listenerID, _ := os.Hostname()
	m := &Monitor{
		config: config,
		placeholders: NewPlaceholderManager(
			clientset,
			config.Namespace,
			listenerID,
			config,
			logger,
		),
		hudClient:     NewHUDClient(config.HUDAPIURL, config.HUDAPIToken),
		clientset:     clientset,
		setMaxRunners: setMaxRunners,
		logger:        logger.With("component", "capacity-monitor"),
		recorder:      metrics.DiscardCapacity,
	}
	for _, opt := range opts {
		opt(m)
	}
	// Static gauges set once at construction so they appear in Prometheus
	// scrapes even before the first reconcile cycle runs.
	m.recorder.SetProactiveCapacity(m.config.ProactiveCapacity)
	m.recorder.SetHUDEnabled(m.hudClient != nil && m.config.HUDAPIToken != "")
	// Seed the reconcile-last-success gauges to listener-startup time so any
	// `time() - metric` wedge alert has a sane floor (small at startup,
	// growing only if reconciles stop succeeding). Otherwise the gauges
	// would read 0 (the Unix epoch) until the first reconcile completes,
	// causing wedge alerts to fire spuriously on every listener restart.
	now := time.Now()
	m.recorder.SetReconcileLastSuccess(reconcilePhaseProvisioner, now)
	m.recorder.SetReconcileLastSuccess(reconcilePhaseReporter, now)
	return m, nil
}

// retryWithBackoff retries fn with exponential backoff (1s, 2s, 4s, ...).
// Returns the last error if all attempts fail. Respects context cancellation.
func retryWithBackoff(ctx context.Context, logger *slog.Logger, operation string, maxRetries int, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			logger.Warn("retrying after failure",
				"operation", operation,
				"attempt", attempt+1,
				"backoff", backoff,
				"previousError", lastErr,
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		if err := fn(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (m *Monitor) listPairsWithRetry(ctx context.Context, maxRetries int) (map[string]*PlaceholderPair, error) {
	var pairs map[string]*PlaceholderPair
	err := retryWithBackoff(ctx, m.logger, "list-pairs", maxRetries, func() error {
		var e error
		pairs, e = m.placeholders.ListPairs(ctx)
		return e
	})
	return pairs, err
}

func (m *Monitor) countRunningRunnersWithRetry(ctx context.Context, maxRetries int) (int, error) {
	var count int
	err := retryWithBackoff(ctx, m.logger, "count-runners", maxRetries, func() error {
		sel := fmt.Sprintf("actions-ephemeral-runner=True,%s=%s",
			labelScaleSet, m.config.ScaleSetName,
		)
		pods, e := m.clientset.CoreV1().Pods(m.config.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: sel,
			FieldSelector: "status.phase=Running",
		})
		if e != nil {
			return e
		}
		count = len(pods.Items)
		return nil
	})
	return count, err
}

func (m *Monitor) queryHUDWithRetry(ctx context.Context) (int, error) {
	var count int
	err := retryWithBackoff(ctx, m.logger, "hud-api", provisionerMaxRetries, func() error {
		// Per-attempt instrumentation: each HTTP attempt is one
		// observation in the histogram and one increment on the counter.
		// retryWithBackoff calls this closure once per attempt, so wrapping
		// it here gives us latency + count for every attempt (including
		// the failed ones) — which is what the dashboard wants.
		start := time.Now()
		var e error
		count, e = m.hudClient.GetQueuedJobsForLabels(ctx, m.config.ScaleSetLabels)
		result := resultSuccess
		if e != nil {
			result = resultError
		}
		m.recorder.ObserveHUDRequest(result, time.Since(start))
		m.recorder.IncHUDRequests(result)
		return e
	})
	return count, err
}

// Run starts the capacity monitor loop. It blocks until ctx is
// cancelled, then cleans up all placeholder pods before returning.
func (m *Monitor) Run(ctx context.Context) error {
	// Log both resolved fleet values so a placeholder landing on the wrong
	// pool can be diagnosed unambiguously. Workflow placeholders use
	// NodeFleet (per-scale-set), runner placeholders use RunnerNodeFleet
	// (cluster-wide).
	m.logger.Info("starting capacity monitor",
		"proactiveCapacity", m.config.ProactiveCapacity,
		"maxRunners", m.config.MaxRunners,
		"labels", m.config.ScaleSetLabels,
		"workflowNodeFleet", m.config.NodeFleet,
		"runnerNodeFleet", m.config.RunnerNodeFleet,
		"recalculateInterval", m.config.RecalculateInterval,
		"reportInterval", m.config.ReportInterval,
	)

	// Clean up orphaned placeholders from previous listener instances.
	// CleanupOrphans uses DeleteCollection (atomic from caller's view): the
	// returned count reflects pods successfully deleted; the error path
	// covers either a pod-DeleteCollection failure (count == 0) or a
	// ConfigMap-DeleteCollection failure (count is the pod-orphan count).
	orphansDeleted, err := m.placeholders.CleanupOrphans(ctx)
	if err != nil {
		m.logger.Warn("failed to cleanup orphaned placeholders", "error", err)
		m.recorder.IncPairDeletes(deleteReasonOrphan, resultError)
	}
	for i := 0; i < orphansDeleted; i++ {
		m.recorder.IncPairDeletes(deleteReasonOrphan, resultSuccess)
	}

	// Initial reconciliation: provision first, then report.
	m.reconcileProvisioning(ctx)
	m.reconcileReporting(ctx)

	// Reporter has its own cancel for ordered shutdown:
	// reporter stops before provisioner cleans up placeholders,
	// preventing a flash of reportedCapacity=0.
	reporterCtx, cancelReporter := context.WithCancel(ctx)

	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		m.runReporter(reporterCtx)
	}()

	// Run provisioner in foreground (blocks until ctx cancelled).
	m.runProvisioner(ctx)

	// Ordered shutdown: stop reporter first, then cleanup.
	cancelReporter()
	<-reporterDone

	m.logger.Info("shutting down capacity monitor, cleaning up placeholders")
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.placeholders.CleanupAll(cleanupCtx); err != nil {
		m.logger.Error("failed to cleanup placeholders on shutdown", "error", err)
	}

	return ctx.Err()
}

func (m *Monitor) runProvisioner(ctx context.Context) {
	ticker := time.NewTicker(m.config.RecalculateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reconcileProvisioning(ctx)
		}
	}
}

func (m *Monitor) runReporter(ctx context.Context) {
	ticker := time.NewTicker(m.config.ReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reconcileReporting(ctx)
		}
	}
}

func (m *Monitor) reconcileProvisioning(ctx context.Context) {
	// Always observe the duration — even if the cycle errors out — so we
	// can spot runaway latency before it wedges the loop.
	start := time.Now()
	defer func() {
		m.recorder.ObserveReconcileDuration(reconcilePhaseProvisioner, time.Since(start))
	}()

	// 1. Query HUD API with retry (graceful fallback to 0).
	queuedJobs := 0
	if m.hudClient != nil && m.config.HUDAPIToken != "" {
		var err error
		queuedJobs, err = m.queryHUDWithRetry(ctx)
		if err != nil {
			m.logger.Warn("HUD API failed after retries, using 0 queued jobs", "error", err)
			m.recorder.IncReconcileSkips(skipReasonHUDAPIFailed)
			queuedJobs = 0
		}
	}
	// Set even on the failure path — queuedJobs is 0 in that case, which
	// is the right value to report. Calling SetQueuedJobs unconditionally
	// also keeps the gauge fresh when HUD is intermittently broken.
	m.recorder.SetQueuedJobs(queuedJobs)

	// 2. Cleanup timed-out placeholder pairs (best-effort, no retry).
	// CleanupTimedOut returns per-pair counts so we can emit accurate
	// success/error metrics. The error is only non-nil for the initial
	// list step — per-pair delete failures are surfaced via timedOutFailed.
	timedOutSuccess, timedOutFailed, err := m.placeholders.CleanupTimedOut(ctx)
	if err != nil {
		m.logger.Warn("failed to list pairs for timed-out cleanup", "error", err)
		m.recorder.IncPairDeletes(deleteReasonTimeout, resultError)
	} else if timedOutSuccess > 0 || timedOutFailed > 0 {
		m.logger.Info("cleaned up timed-out placeholder pairs",
			"success", timedOutSuccess, "failed", timedOutFailed)
	}
	for i := 0; i < timedOutSuccess; i++ {
		m.recorder.IncPairDeletes(deleteReasonTimeout, resultSuccess)
	}
	for i := 0; i < timedOutFailed; i++ {
		m.recorder.IncPairDeletes(deleteReasonTimeout, resultError)
	}

	// 3. List current pairs with retry.
	pairs, err := m.listPairsWithRetry(ctx, provisionerMaxRetries)
	if err != nil {
		m.logger.Error("failed to list pairs after retries, skipping provisioning cycle", "error", err)
		m.recorder.IncReconcileSkips(skipReasonProvisionerListPairs)
		return
	}
	currentPairs := len(pairs)
	m.recorder.SetPairs(currentPairs)
	m.emitPlaceholderPodPhases(pairs)

	// 4. Calculate desired placeholder count.
	desiredPairs := m.config.ProactiveCapacity + queuedJobs
	if m.config.MaxRunners > 0 {
		desiredPairs = min(desiredPairs, m.config.MaxRunners)
	}
	desiredPairs = max(desiredPairs, 0)
	m.recorder.SetDesiredPairs(desiredPairs)

	// 5. Adjust: create or delete pairs.
	m.adjustPairs(ctx, pairs, currentPairs, desiredPairs)

	m.logger.Info("provisioning reconciled",
		"queuedJobs", queuedJobs,
		"desiredPairs", desiredPairs,
		"currentPairs", currentPairs,
	)
	// Mark success only at the end of a fully completed cycle. Early-exit
	// paths above (list-pairs error) do NOT mark success — the whole point
	// of this metric is to detect when reconciles stop succeeding.
	m.recorder.SetReconcileLastSuccess(reconcilePhaseProvisioner, time.Now())
}

func (m *Monitor) reconcileReporting(ctx context.Context) {
	// Always observe the duration — even if the cycle errors out — so we
	// can spot runaway latency before it wedges the loop.
	start := time.Now()
	defer func() {
		m.recorder.ObserveReconcileDuration(reconcilePhaseReporter, time.Since(start))
	}()

	// 1. List pairs with retry. On failure, keep previous capacity unchanged.
	pairs, err := m.listPairsWithRetry(ctx, reporterMaxRetries)
	if err != nil {
		m.logger.Warn("failed to list pairs, keeping previous capacity", "error", err)
		m.recorder.IncReconcileSkips(skipReasonReporterListPairs)
		return
	}

	runningPairs := 0
	for _, pair := range pairs {
		if pair.BothRunning() {
			runningPairs++
		}
	}
	m.recorder.SetRunningPairs(runningPairs)

	// 2. Count running runners with retry. On failure, keep previous capacity.
	runningRunners, err := m.countRunningRunnersWithRetry(ctx, reporterMaxRetries)
	if err != nil {
		m.logger.Warn("failed to count runners, keeping previous capacity", "error", err)
		m.recorder.IncReconcileSkips(skipReasonReporterCountRunners)
		return
	}

	// 3. Report capacity to GitHub.
	capacity := runningRunners + runningPairs
	if m.config.MaxRunners > 0 {
		capacity = min(capacity, m.config.MaxRunners)
	}
	// Set the gauge BEFORE invoking setMaxRunners — so the metric reflects
	// the value we tried to send, not what the listener might filter.
	m.recorder.SetAdvertisedMaxRunners(capacity)
	m.setMaxRunners(capacity)

	m.logger.Info("capacity reported",
		"runningPairs", runningPairs,
		"runningRunners", runningRunners,
		"reportedCapacity", capacity,
	)
	// Mark success only at the end of a fully completed cycle. Early-exit
	// paths above (list-pairs error, count-runners error) do NOT mark
	// success — wedge detection depends on this.
	m.recorder.SetReconcileLastSuccess(reconcilePhaseReporter, time.Now())
}

func (m *Monitor) adjustPairs(
	ctx context.Context,
	pairs map[string]*PlaceholderPair,
	currentPairs, desiredPairs int,
) {
	if currentPairs < desiredPairs {
		toCreate := desiredPairs - currentPairs
		for i := 0; i < toCreate; i++ {
			slotID := fmt.Sprintf("%d-%d", time.Now().Unix(), m.slotCounter.Add(1))
			if err := m.placeholders.CreatePair(ctx, slotID); err != nil {
				m.logger.Error("failed to create placeholder pair",
					"slotID", slotID, "error", err)
				m.recorder.IncPairCreates(resultError)
				continue
			}
			m.recorder.IncPairCreates(resultSuccess)
		}
		return
	}

	if currentPairs <= desiredPairs {
		return
	}

	toDelete := currentPairs - desiredPairs
	deleted := 0
	// Track slotIDs deleted in pass 1 so pass 2 doesn't re-delete them.
	deletedSlots := make(map[string]struct{})

	// First pass: prefer deleting non-running (Pending) pairs.
	for slotID, pair := range pairs {
		if deleted >= toDelete {
			break
		}
		if !pair.BothRunning() {
			if err := m.placeholders.DeletePair(ctx, slotID); err != nil {
				m.logger.Error("failed to delete placeholder pair",
					"slotID", slotID, "error", err)
				m.recorder.IncPairDeletes(deleteReasonExcess, resultError)
				continue
			}
			m.recorder.IncPairDeletes(deleteReasonExcess, resultSuccess)
			deletedSlots[slotID] = struct{}{}
			deleted++
		}
	}

	// Second pass: delete running pairs if we still need to reduce.
	for slotID := range pairs {
		if deleted >= toDelete {
			break
		}
		if _, alreadyDeleted := deletedSlots[slotID]; alreadyDeleted {
			continue
		}
		if err := m.placeholders.DeletePair(ctx, slotID); err != nil {
			m.logger.Error("failed to delete placeholder pair",
				"slotID", slotID, "error", err)
			m.recorder.IncPairDeletes(deleteReasonExcess, resultError)
			continue
		}
		m.recorder.IncPairDeletes(deleteReasonExcess, resultSuccess)
		deletedSlots[slotID] = struct{}{}
		deleted++
	}
}

// emitPlaceholderPodPhases iterates the supplied pairs map and emits
// gha_capacity_placeholder_pods{role,phase} for ALL (role × phase)
// combinations — including zeros — so the gauges decrement when a phase
// empties out (otherwise old non-zero values stick forever).
func (m *Monitor) emitPlaceholderPodPhases(pairs map[string]*PlaceholderPair) {
	// Pre-seed zeros for every (role, phase) combination so missing
	// combinations decrement instead of sticking on the previous value.
	counts := make(map[string]map[string]int, len(allPlaceholderRoles))
	for _, role := range allPlaceholderRoles {
		counts[role] = make(map[string]int, len(allPlaceholderPhases))
		for _, phase := range allPlaceholderPhases {
			counts[role][phase] = 0
		}
	}
	for _, pair := range pairs {
		incPlaceholderPhase(counts, rolePlaceholderRunnerLabel, pair.RunnerPod)
		incPlaceholderPhase(counts, rolePlaceholderWorkflowLabel, pair.WorkflowPod)
	}
	for _, role := range allPlaceholderRoles {
		for _, phase := range allPlaceholderPhases {
			m.recorder.SetPlaceholderPods(role, phase, counts[role][phase])
		}
	}
}

// incPlaceholderPhase increments the count for a single pod's role and
// phase, falling back to "unknown" when the pod has no phase set yet.
// nil pods (one half of the pair missing) are ignored.
func incPlaceholderPhase(counts map[string]map[string]int, role string, pod *corev1.Pod) {
	if pod == nil {
		return
	}
	phase := string(pod.Status.Phase)
	if phase == "" {
		// Empty phase shows up briefly between Create and the API server
		// recording status. Treat it as unknown so the gauge stays in the
		// declared phase enum.
		phase = string(corev1.PodUnknown)
	}
	if _, ok := counts[role][phase]; !ok {
		// Defensive: a pod with a phase outside the declared enum (should
		// never happen in practice) gets bucketed into "unknown" rather
		// than silently dropped — preserves total invariant.
		phase = string(corev1.PodUnknown)
	}
	counts[role][phase]++
}
