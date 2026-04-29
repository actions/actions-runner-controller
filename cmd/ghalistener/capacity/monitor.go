package capacity

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	reporterMaxRetries    = 2 // 1s + 2s = 3s, within 5s reporter interval
	provisionerMaxRetries = 3 // 1s + 2s + 4s = 7s, within 30s provisioner interval
)

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
}

// New creates a new capacity Monitor. The setMaxRunners callback is
// typically listener.SetMaxRunners.
func New(
	config Config,
	clientset kubernetes.Interface,
	setMaxRunners func(int),
	logger *slog.Logger,
) *Monitor {
	config.Validate()
	listenerID, _ := os.Hostname()
	return &Monitor{
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
	}
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
		var e error
		count, e = m.hudClient.GetQueuedJobsForLabels(ctx, m.config.ScaleSetLabels)
		return e
	})
	return count, err
}

// Run starts the capacity monitor loop. It blocks until ctx is
// cancelled, then cleans up all placeholder pods before returning.
func (m *Monitor) Run(ctx context.Context) error {
	m.logger.Info("starting capacity monitor",
		"proactiveCapacity", m.config.ProactiveCapacity,
		"maxRunners", m.config.MaxRunners,
		"labels", m.config.ScaleSetLabels,
		"recalculateInterval", m.config.RecalculateInterval,
		"reportInterval", m.config.ReportInterval,
	)

	// Clean up orphaned placeholders from previous listener instances.
	if err := m.placeholders.CleanupOrphans(ctx); err != nil {
		m.logger.Warn("failed to cleanup orphaned placeholders", "error", err)
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
	// 1. Query HUD API with retry (graceful fallback to 0).
	queuedJobs := 0
	if m.hudClient != nil && m.config.HUDAPIToken != "" {
		var err error
		queuedJobs, err = m.queryHUDWithRetry(ctx)
		if err != nil {
			m.logger.Warn("HUD API failed after retries, using 0 queued jobs", "error", err)
			queuedJobs = 0
		}
	}

	// 2. Cleanup timed-out placeholder pairs (best-effort, no retry).
	timedOut, err := m.placeholders.CleanupTimedOut(ctx)
	if err != nil {
		m.logger.Warn("failed to cleanup timed out placeholders", "error", err)
	} else if timedOut > 0 {
		m.logger.Info("cleaned up timed-out placeholder pairs", "count", timedOut)
	}

	// 3. List current pairs with retry.
	pairs, err := m.listPairsWithRetry(ctx, provisionerMaxRetries)
	if err != nil {
		m.logger.Error("failed to list pairs after retries, skipping provisioning cycle", "error", err)
		return
	}
	currentPairs := len(pairs)

	// 4. Calculate desired placeholder count.
	desiredPairs := m.config.ProactiveCapacity + queuedJobs
	if m.config.MaxRunners > 0 {
		desiredPairs = min(desiredPairs, m.config.MaxRunners)
	}
	desiredPairs = max(desiredPairs, 0)

	// 5. Adjust: create or delete pairs.
	m.adjustPairs(ctx, pairs, currentPairs, desiredPairs)

	m.logger.Info("provisioning reconciled",
		"queuedJobs", queuedJobs,
		"desiredPairs", desiredPairs,
		"currentPairs", currentPairs,
	)
}

func (m *Monitor) reconcileReporting(ctx context.Context) {
	// 1. List pairs with retry. On failure, keep previous capacity unchanged.
	pairs, err := m.listPairsWithRetry(ctx, reporterMaxRetries)
	if err != nil {
		m.logger.Warn("failed to list pairs, keeping previous capacity", "error", err)
		return
	}

	runningPairs := 0
	for _, pair := range pairs {
		if pair.BothRunning() {
			runningPairs++
		}
	}

	// 2. Count running runners with retry. On failure, keep previous capacity.
	runningRunners, err := m.countRunningRunnersWithRetry(ctx, reporterMaxRetries)
	if err != nil {
		m.logger.Warn("failed to count runners, keeping previous capacity", "error", err)
		return
	}

	// 3. Report capacity to GitHub.
	capacity := runningRunners + runningPairs
	if m.config.MaxRunners > 0 {
		capacity = min(capacity, m.config.MaxRunners)
	}
	m.setMaxRunners(capacity)

	m.logger.Info("capacity reported",
		"runningPairs", runningPairs,
		"runningRunners", runningRunners,
		"reportedCapacity", capacity,
	)
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
			}
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
				continue
			}
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
			continue
		}
		deletedSlots[slotID] = struct{}{}
		deleted++
	}
}
