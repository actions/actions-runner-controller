package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hraName      = "hra_name"
	hraNamespace = "hra_namespace"
)

var (
	horizontalRunnerAutoscalerMetrics = []prometheus.Collector{
		horizontalRunnerAutoscalerMinReplicas,
		horizontalRunnerAutoscalerMaxReplicas,
		horizontalRunnerAutoscalerDesiredReplicas,
	}
)

var (
	horizontalRunnerAutoscalerMinReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontal_runner_autoscaler_min_replicas",
			Help: "minReplicas of HorizontalRunnerAutoscaler",
		},
		[]string{hraName, hraNamespace},
	)
	horizontalRunnerAutoscalerMaxReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontal_runner_autoscaler_max_replicas",
			Help: "maxReplicas of HorizontalRunnerAutoscaler",
		},
		[]string{hraName, hraNamespace},
	)
	horizontalRunnerAutoscalerDesiredReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontal_runner_autoscaler_desired_replicas",
			Help: "desiredReplicas of HorizontalRunnerAutoscaler",
		},
		[]string{hraName, hraNamespace},
	)
)

func SetHorizontalRunnerAutoscalerSpec(o metav1.ObjectMeta, spec v1alpha1.HorizontalRunnerAutoscalerSpec) {
	labels := prometheus.Labels{
		hraName:      o.Name,
		hraNamespace: o.Namespace,
	}
	if spec.MaxReplicas != nil {
		horizontalRunnerAutoscalerMaxReplicas.With(labels).Set(float64(*spec.MaxReplicas))
	}
	if spec.MinReplicas != nil {
		horizontalRunnerAutoscalerMinReplicas.With(labels).Set(float64(*spec.MinReplicas))
	}
}

func SetHorizontalRunnerAutoscalerStatus(o metav1.ObjectMeta, status v1alpha1.HorizontalRunnerAutoscalerStatus) {
	labels := prometheus.Labels{
		hraName:      o.Name,
		hraNamespace: o.Namespace,
	}
	if status.DesiredReplicas != nil {
		horizontalRunnerAutoscalerDesiredReplicas.With(labels).Set(float64(*status.DesiredReplicas))
	}
}
