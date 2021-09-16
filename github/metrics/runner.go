// Package metrics provides monitoring of the GitHub related metrics.
//
// This depends on the metrics exporter of kubebuilder.
// See https://book.kubebuilder.io/reference/metrics.html for details.
package metrics

import (
	"sort"
	"strings"

	"github.com/google/go-github/v37/github"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func init() {
	metrics.Registry.MustRegister(
		metricRunnerCount,
	)
}

type runnerStatus string

const (
	active  runnerStatus = "active"
	idle    runnerStatus = "idle"
	offline runnerStatus = "offline"
)

var (
	metricRunnerCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_count",
			Help: "Number of runners currently registered on GitHub",
		},
		[]string{"repository", "organization", "enterprise", "labels", "status"},
	)
)

func SetRunnerStatus(runners []*github.Runner, enterprise, org, repo string) {
	metricRunnerCount.Reset()

	for _, runner := range runners {
		metricLabels := prometheus.Labels{
			"enterprise":   enterprise,
			"organization": org,
			"repository":   repo,
			"labels":       serializeLabels(runner.Labels),
			"status":       string(determineStatus(*runner)),
		}

		metricRunnerCount.With(metricLabels).Inc()
	}
}

func determineStatus(runner github.Runner) runnerStatus {
	if runner.GetStatus() == "offline" {
		return offline
	}

	if runner.GetBusy() {
		return active
	}

	return idle
}

func serializeLabels(runnerLabels []*github.RunnerLabels) string {
	var labels []string

	for _, runnerLabel := range runnerLabels {
		labels = append(labels, runnerLabel.GetName())
	}

	sort.Strings(labels)
	return strings.Join(labels, ",")
}
