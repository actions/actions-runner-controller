package capacity

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
	slotCounter   int // monotonic counter for unique slot IDs
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
		hudClient:     NewHUDClient(config.HUDAPIToken),
		clientset:     clientset,
		setMaxRunners: setMaxRunners,
		logger:        logger.With("component", "capacity-monitor"),
	}
}

// Run starts the capacity monitor loop. It blocks until ctx is
// cancelled, then cleans up all placeholder pods before returning.
func (m *Monitor) Run(ctx context.Context) error {
	m.logger.Info("starting capacity monitor",
		"proactiveCapacity", m.config.ProactiveCapacity,
		"maxRunners", m.config.MaxRunners,
		"labels", m.config.ScaleSetLabels,
		"recalculateInterval", m.config.RecalculateInterval,
	)

	// Clean up orphaned placeholders from previous listener instances.
	if err := m.placeholders.CleanupOrphans(ctx); err != nil {
		m.logger.Warn("failed to cleanup orphaned placeholders", "error", err)
	}

	// Initial reconciliation.
	m.reconcile(ctx)

	ticker := time.NewTicker(m.config.RecalculateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("shutting down capacity monitor, cleaning up placeholders")
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.placeholders.CleanupAll(cleanupCtx); err != nil {
				m.logger.Error("failed to cleanup placeholders on shutdown", "error", err)
			}
			return ctx.Err()
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

func (m *Monitor) reconcile(ctx context.Context) {
	// 1. Query HUD API for queued jobs (graceful fallback to 0 on error).
	queuedJobs := 0
	if m.hudClient != nil && m.config.HUDAPIToken != "" {
		var err error
		queuedJobs, err = m.hudClient.GetQueuedJobsForLabels(ctx, m.config.ScaleSetLabels)
		if err != nil {
			m.logger.Warn("HUD API query failed, using 0 queued jobs", "error", err)
			queuedJobs = 0
		}
	}

	// 2. Cleanup timed-out placeholder pairs.
	timedOut, err := m.placeholders.CleanupTimedOut(ctx)
	if err != nil {
		m.logger.Warn("failed to cleanup timed out placeholders", "error", err)
	} else if timedOut > 0 {
		m.logger.Info("cleaned up timed-out placeholder pairs", "count", timedOut)
	}

	// 3. Get current placeholder state.
	pairs, err := m.placeholders.ListPairs(ctx)
	if err != nil {
		m.logger.Error("failed to list placeholder pairs", "error", err)
		return
	}

	runningPairs := 0
	for _, pair := range pairs {
		if pair.BothRunning() {
			runningPairs++
		}
	}
	currentPairs := len(pairs)

	// 4. Count running ephemeral runners (actual jobs on nodes).
	runningRunners := m.countRunningRunners(ctx)

	// 5. Calculate desired placeholder count.
	// MaxRunners == 0 means unlimited (not "zero capacity") — skip the cap.
	desiredPairs := m.config.ProactiveCapacity + queuedJobs
	if m.config.MaxRunners > 0 {
		desiredPairs = min(desiredPairs, m.config.MaxRunners)
	}
	desiredPairs = max(desiredPairs, 0)

	// 6. Adjust: create or delete pairs.
	m.adjustPairs(ctx, pairs, currentPairs, desiredPairs)

	// 7. Report capacity to GitHub.
	// capacity = running runners (actual jobs) + running placeholder pairs
	// (pre-warmed nodes), capped at maxRunners (when set; 0 means unlimited).
	capacity := runningRunners + runningPairs
	if m.config.MaxRunners > 0 {
		capacity = min(capacity, m.config.MaxRunners)
	}
	m.setMaxRunners(capacity)

	m.logger.Info("capacity reconciled",
		"queuedJobs", queuedJobs,
		"desiredPairs", desiredPairs,
		"currentPairs", currentPairs,
		"runningPairs", runningPairs,
		"runningRunners", runningRunners,
		"reportedCapacity", capacity,
	)
}

// countRunningRunners lists EphemeralRunner pods that are actually
// running jobs for this scale set.
func (m *Monitor) countRunningRunners(ctx context.Context) int {
	sel := fmt.Sprintf("actions-ephemeral-runner=True,%s=%s",
		labelScaleSet, m.config.ScaleSetName,
	)
	pods, err := m.clientset.CoreV1().Pods(m.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: sel,
	})
	if err != nil {
		m.logger.Warn("failed to list running runners", "error", err)
		return 0
	}
	return len(pods.Items)
}

func (m *Monitor) adjustPairs(
	ctx context.Context,
	pairs map[string]*PlaceholderPair,
	currentPairs, desiredPairs int,
) {
	if currentPairs < desiredPairs {
		toCreate := desiredPairs - currentPairs
		for i := 0; i < toCreate; i++ {
			m.slotCounter++
			slotID := fmt.Sprintf("%d-%d", time.Now().Unix(), m.slotCounter)
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
