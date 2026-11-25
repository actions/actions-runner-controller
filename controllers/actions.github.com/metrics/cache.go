package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var cacheLabels = []string{"namespace", "scale_set"}

// Results reported by CacheRestore.
const (
	// CacheRestoreHit is a runner whose volume was restored from a snapshot.
	CacheRestoreHit = "hit"
	// CacheRestoreCold is a runner that started empty because there was no snapshot yet.
	CacheRestoreCold = "cold"
	// CacheRestoreExpired is a runner that started empty because the newest snapshot outlived the retention.
	CacheRestoreExpired = "expired"
	// CacheRestoreTimeout is a runner that gave up waiting for a restore and started empty.
	CacheRestoreTimeout = "timeout"
	// CacheRestoreUnavailable is a runner that got an emptyDir because the cluster has no VolumeSnapshot API.
	CacheRestoreUnavailable = "unavailable"
)

// Results reported by CacheCommit.
const (
	// CacheCommitRequested is a finished runner whose volume was snapshotted.
	CacheCommitRequested = "requested"
	// CacheCommitDenied is a finished runner the commit policy did not allow to save its volume.
	CacheCommitDenied = "denied"
	// CacheCommitFailed is a finished runner whose snapshot could not be requested.
	CacheCommitFailed = "failed"
)

// Reasons reported by CacheSnapshotDeleted.
const (
	CacheSnapshotSuperseded = "superseded"
	CacheSnapshotExpired    = "expired"
	CacheSnapshotFailed     = "failed"
)

var (
	cacheRestores = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "cache_restores_total",
			Help:      "Number of runner cache volumes created, by how they were initialised.",
		},
		append([]string{"result"}, cacheLabels...),
	)
	cacheCommits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "cache_commits_total",
			Help:      "Number of finished runners considered for saving their cache volume, by outcome.",
		},
		append([]string{"result"}, cacheLabels...),
	)
	cacheSnapshotsDeleted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "cache_snapshots_deleted_total",
			Help:      "Number of cache snapshots deleted by garbage collection, by reason.",
		},
		append([]string{"reason"}, cacheLabels...),
	)
	cacheSnapshotAge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "cache_snapshot_age_seconds",
			Help:      "Age of the newest ready cache snapshot of a scale set. Absent while it has none.",
		},
		cacheLabels,
	)
	cacheSnapshots = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "cache_snapshots",
			Help:      "Number of cache snapshots of a scale set, by state (ready or pending).",
		},
		append([]string{"state"}, cacheLabels...),
	)
)

func registerCacheMetrics() {
	metrics.Registry.MustRegister(cacheRestores, cacheCommits, cacheSnapshotsDeleted, cacheSnapshotAge, cacheSnapshots)
}

// CacheRestore counts a runner cache volume that was created.
func CacheRestore(namespace, scaleSet, result string) {
	cacheRestores.WithLabelValues(result, namespace, scaleSet).Inc()
}

// CacheCommit counts a finished runner that was considered for saving its cache volume.
func CacheCommit(namespace, scaleSet, result string) {
	cacheCommits.WithLabelValues(result, namespace, scaleSet).Inc()
}

// CacheSnapshotDeleted counts a cache snapshot that garbage collection deleted.
func CacheSnapshotDeleted(namespace, scaleSet, reason string) {
	cacheSnapshotsDeleted.WithLabelValues(reason, namespace, scaleSet).Inc()
}

// CacheSnapshotState reports the snapshots of a scale set. A negative newestAge means there is
// no ready snapshot, which removes the age series instead of reporting a misleading value.
func CacheSnapshotState(namespace, scaleSet string, ready, pending int, newestAgeSeconds float64) {
	cacheSnapshots.WithLabelValues("ready", namespace, scaleSet).Set(float64(ready))
	cacheSnapshots.WithLabelValues("pending", namespace, scaleSet).Set(float64(pending))
	if newestAgeSeconds < 0 {
		cacheSnapshotAge.DeleteLabelValues(namespace, scaleSet)
		return
	}
	cacheSnapshotAge.WithLabelValues(namespace, scaleSet).Set(newestAgeSeconds)
}

// CacheSnapshotForget removes the snapshot gauges of a scale set that has no cache volume any more.
func CacheSnapshotForget(namespace, scaleSet string) {
	cacheSnapshots.DeleteLabelValues("ready", namespace, scaleSet)
	cacheSnapshots.DeleteLabelValues("pending", namespace, scaleSet)
	cacheSnapshotAge.DeleteLabelValues(namespace, scaleSet)
}

// CacheRestoreCounter returns one series of the cache restore counter, for tests.
func CacheRestoreCounter(result, namespace, scaleSet string) prometheus.Counter {
	return cacheRestores.WithLabelValues(result, namespace, scaleSet)
}

// CacheCommitCounter returns one series of the cache commit counter, for tests.
func CacheCommitCounter(result, namespace, scaleSet string) prometheus.Counter {
	return cacheCommits.WithLabelValues(result, namespace, scaleSet)
}
