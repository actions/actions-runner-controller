package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

const (
	rdName      = "rd_name"
	rdNamespace = "rd_namespace"
)

var (
	runnerDeploymentMetrics = []prometheus.Collector{
		runnerDeploymentReplicas,
	}
)

var (
	runnerDeploymentReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runner_deployment_replicas",
			Help: "replicas of RunnerDeployment",
		},
		[]string{rdName, rdNamespace},
	)
)

func SetRunnerDeployment(rd v1alpha1.RunnerDeployment) {
	labels := prometheus.Labels{
		rdName:      rd.Name,
		rdNamespace: rd.Namespace,
	}
	if rd.Spec.Replicas != nil {
		runnerDeploymentReplicas.With(labels).Set(float64(*rd.Spec.Replicas))
	}
}
