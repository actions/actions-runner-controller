package metrics

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	rdName      = "runnerdeployment"
	rdNamespace = "namespace"
)

var (
	runnerDeploymentMetrics = []prometheus.Collector{
		runnerDeploymentReplicas,
	}
)

var (
	runnerDeploymentReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runnerdeployment_spec_replicas",
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
