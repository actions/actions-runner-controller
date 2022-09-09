package metrics

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	rsName      = "runnerset"
	rsNamespace = "namespace"
)

var (
	runnerSetReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runnerset_spec_replicas",
			Help: "replicas of RunnerSet",
		},
		[]string{rsName, rsNamespace},
	)
)

func SetRunnerSet(rd v1alpha1.RunnerSet) {
	labels := prometheus.Labels{
		rsName:      rd.Name,
		rsNamespace: rd.Namespace,
	}
	if rd.Spec.Replicas != nil {
		runnerSetReplicas.With(labels).Set(float64(*rd.Spec.Replicas))
	}
}
