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
		failedEphemeralRunners,
		runningListeners,
	)
}

func SetEphemeralRunnerCountsByStatus(commonLabels CommonLabels, pending, running, failed int) {
	pendingEphemeralRunners.With(commonLabels.labels()).Set(float64(pending))
	runningEphemeralRunners.With(commonLabels.labels()).Set(float64(running))
	failedEphemeralRunners.With(commonLabels.labels()).Set(float64(failed))
}

func AddRunningListener(commonLabels CommonLabels) {
	runningListeners.With(commonLabels.labels()).Set(1)
}

func SubRunningListener(commonLabels CommonLabels) {
	runningListeners.With(commonLabels.labels()).Set(0)
}
