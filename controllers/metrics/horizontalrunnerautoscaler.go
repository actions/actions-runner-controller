package metrics

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hraName      = "horizontalrunnerautoscaler"
	hraNamespace = "namespace"
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
			Name: "horizontalrunnerautoscaler_spec_min_replicas",
			Help: "minReplicas of HorizontalRunnerAutoscaler",
		},
		[]string{hraName, hraNamespace},
	)
	horizontalRunnerAutoscalerMaxReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_spec_max_replicas",
			Help: "maxReplicas of HorizontalRunnerAutoscaler",
		},
		[]string{hraName, hraNamespace},
	)
	horizontalRunnerAutoscalerDesiredReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_status_desired_replicas",
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
