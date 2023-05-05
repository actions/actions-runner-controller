package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var githubScaleSetControllerSubsystem = "github_runner_scale_set_controller"

var labels = []string{
	"name",
	"namespace",
	"repo",
	"owner",
	"configUrl",
}

type CommonLabels struct {
	Name      string
	Namespace string
	Repo      string
	Owner     string
	ConfigURL string
}

func (l *CommonLabels) labels() prometheus.Labels {
	return prometheus.Labels{
		"name":      l.Name,
		"namespace": l.Namespace,
		"repo":      l.Repo,
		"owner":     l.Owner,
		"configUrl": l.ConfigURL,
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
)

func RegisterMetrics() {
	metrics.Registry.MustRegister(
		pendingEphemeralRunners,
		runningEphemeralRunners,
		failedEphemeralRunners,
	)
}

func SetEphemeralRunnerCountsByStatus(commonLabels CommonLabels, pending, running, failed int) {
	pendingEphemeralRunners.With(commonLabels.labels()).Set(float64(pending))
	runningEphemeralRunners.With(commonLabels.labels()).Set(float64(running))
	failedEphemeralRunners.With(commonLabels.labels()).Set(float64(failed))
}
