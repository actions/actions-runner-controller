package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var githubScaleSetControllerSubsystem = "gha_controller"

var labels = []string{
	"name",
	"namespace",
	"repository",
	"organization",
	"enterprise",
}

type CommonLabels struct {
	Name         string
	Namespace    string
	Repository   string
	Organization string
	Enterprise   string
}

func (l *CommonLabels) labels() prometheus.Labels {
	return prometheus.Labels{
		"name":         l.Name,
		"namespace":    l.Namespace,
		"repository":   l.Repository,
		"organization": l.Organization,
		"enterprise":   l.Enterprise,
	}
}

var (
	pendingEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "pending_ephemeral_runners",
			Help:      "Number of ephemeral runners in a pending state.",
		},
		labels,
	)
	runningEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "running_ephemeral_runners",
			Help:      "Number of ephemeral runners in a running state.",
		},
		labels,
	)
	failedEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "failed_ephemeral_runners",
			Help:      "Number of ephemeral runners in a failed state.",
		},
		labels,
	)
	succeededEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "succeeded_ephemeral_runners",
			Help:      "Number of ephemeral runners in a succeeded state.",
		},
		labels,
	)
	outdatedEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "outdated_ephemeral_runners",
			Help:      "Number of ephemeral runners in an outdated state.",
		},
		labels,
	)
	deletingEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "deleting_ephemeral_runners",
			Help:      "Number of ephemeral runners in a deleting state.",
		},
		labels,
	)
	runningListeners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "running_listeners",
			Help:      "Number of listeners in a running state.",
		},
		labels,
	)
)

func RegisterMetrics() {
	metrics.Registry.MustRegister(
		pendingEphemeralRunners,
		runningEphemeralRunners,
		succeededEphemeralRunners,
		failedEphemeralRunners,
		outdatedEphemeralRunners,
		deletingEphemeralRunners,
		runningListeners,
	)
}

// SetEphemeralRunnerCountsByLifecycle sets ephemeral runner counts across all six lifecycle states
// using deterministic bucket assignment with explicit precedence:
//
// Lifecycle Precedence Contract:
//  1. deleting: if runner has DeletionTimestamp set
//  2. explicit phase buckets: if runner.Status.Phase is one of (Running/Succeeded/Failed/Outdated)
//  3. pending (fallback): for empty/unset/other phase values
//
// This ensures each runner maps to exactly one metric bucket (no double-counting).
func SetEphemeralRunnerCountsByLifecycle(commonLabels CommonLabels, pending, running, succeeded, failed, outdated, deleting int) {
	labels := commonLabels.labels()
	pendingEphemeralRunners.With(labels).Set(float64(pending))
	runningEphemeralRunners.With(labels).Set(float64(running))
	succeededEphemeralRunners.With(labels).Set(float64(succeeded))
	failedEphemeralRunners.With(labels).Set(float64(failed))
	outdatedEphemeralRunners.With(labels).Set(float64(outdated))
	deletingEphemeralRunners.With(labels).Set(float64(deleting))
}

func AddRunningListener(commonLabels CommonLabels) {
	runningListeners.With(commonLabels.labels()).Set(1)
}

func SubRunningListener(commonLabels CommonLabels) {
	runningListeners.With(commonLabels.labels()).Set(0)
}
