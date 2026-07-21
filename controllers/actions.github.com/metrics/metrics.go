package metrics

import (
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
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
	succeededEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "succeeded_ephemeral_runners",
			Help:      "Number of ephemeral runners in a succeeded state.",
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
	outdatedEphemeralRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: githubScaleSetControllerSubsystem,
			Name:      "outdated_ephemeral_runners",
			Help:      "Number of ephemeral runners in an outdated state.",
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
		runningListeners,
	)
}

func AddEphemeralRunner(commonLabels CommonLabels, phase v1alpha1.EphemeralRunnerPhase) {
	updateEphemeralRunner(commonLabels, phase, 1)
}

func SubEphemeralRunner(commonLabels CommonLabels, phase v1alpha1.EphemeralRunnerPhase) {
	updateEphemeralRunner(commonLabels, phase, -1)
}

func updateEphemeralRunner(commonLabels CommonLabels, phase v1alpha1.EphemeralRunnerPhase, delta float64) {
	switch phase {
	case v1alpha1.EphemeralRunnerPhasePending:
		pendingEphemeralRunners.With(commonLabels.labels()).Add(delta)
	case v1alpha1.EphemeralRunnerPhaseRunning:
		runningEphemeralRunners.With(commonLabels.labels()).Add(delta)
	case v1alpha1.EphemeralRunnerPhaseSucceeded:
		succeededEphemeralRunners.With(commonLabels.labels()).Add(delta)
	case v1alpha1.EphemeralRunnerPhaseFailed:
		failedEphemeralRunners.With(commonLabels.labels()).Add(delta)
	case v1alpha1.EphemeralRunnerPhaseOutdated:
		outdatedEphemeralRunners.With(commonLabels.labels()).Add(delta)
	}
}

func AddRunningListener(commonLabels CommonLabels) {
	runningListeners.With(commonLabels.labels()).Set(1)
}

func SubRunningListener(commonLabels CommonLabels) {
	runningListeners.With(commonLabels.labels()).Set(0)
}
