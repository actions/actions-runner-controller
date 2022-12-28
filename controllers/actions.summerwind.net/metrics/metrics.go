// Package metrics provides the metrics of custom resources such as HRA.
//
// This depends on the metrics exporter of kubebuilder.
// See https://book.kubebuilder.io/reference/metrics.html for details.
package metrics

import (
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func init() {
	metrics.Registry.MustRegister(runnerDeploymentMetrics...)
	metrics.Registry.MustRegister(horizontalRunnerAutoscalerMetrics...)
}
