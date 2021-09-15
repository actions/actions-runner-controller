// Package metrics provides monitoring of the GitHub related metrics.
//
// This depends on the metrics exporter of kubebuilder.
// See https://book.kubebuilder.io/reference/metrics.html for details.
package metrics

import (
	"github.com/google/go-github/v37/github"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func init() {
	metrics.Registry.MustRegister(
		metricRunnerActiveCount,
		metricRunnerIdleCount,
		metricRunnerOfflineCount,
	)
}

var (
	metricRunnerActiveCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_active_runner_count",
			Help: "Number of currently active runner registered on GitHub",
		},
		[]string{"repository", "organization", "enterprise"},
	)
	metricRunnerIdleCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_idle_runner_count",
			Help: "Number of idling runner registered on GitHub",
		},
		[]string{"repository", "organization", "enterprise"},
	)
	metricRunnerOfflineCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_offline_runner_count",
			Help: "Number of offline runner registered on GitHub",
		},
		[]string{"repository", "organization", "enterprise"},
	)
)

func SetRunnerStatus(runners []*github.Runner, enterprise, org, repo string) {
	var countActive, countIdle, countOffline float64

	for _, runner := range runners {
		status := runner.GetStatus()
		if status == "offline" {
			countOffline++
			continue
		}

		isBusy := runner.GetBusy()
		if isBusy {
			countActive++
			continue
		}

		countIdle++
	}

	labels := prometheus.Labels{
		"enterprise":   enterprise,
		"organization": org,
		"repository":   repo,
	}

	metricRunnerActiveCount.With(labels).Set(countActive)
	metricRunnerIdleCount.With(labels).Set(countIdle)
	metricRunnerOfflineCount.With(labels).Set(countOffline)
}
