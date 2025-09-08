package metrics

import (
	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hraName        = "horizontalrunnerautoscaler"
	hraNamespace   = "namespace"
	stEnterprise   = "enterprise"
	stOrganization = "organization"
	stRepository   = "repository"
	stKind         = "kind"
	stName         = "name"
)

var (
	horizontalRunnerAutoscalerMetrics = []prometheus.Collector{
		horizontalRunnerAutoscalerMinReplicas,
		horizontalRunnerAutoscalerMaxReplicas,
		horizontalRunnerAutoscalerDesiredReplicas,
		horizontalRunnerAutoscalerReplicasDesired,
		horizontalRunnerAutoscalerRunners,
		horizontalRunnerAutoscalerRunnersRegistered,
		horizontalRunnerAutoscalerRunnersBusy,
		horizontalRunnerAutoscalerTerminatingBusy,
		horizontalRunnerAutoscalerNecessaryReplicas,
		horizontalRunnerAutoscalerWorkflowRunsCompleted,
		horizontalRunnerAutoscalerWorkflowRunsInProgress,
		horizontalRunnerAutoscalerWorkflowRunsQueued,
		horizontalRunnerAutoscalerWorkflowRunsUnknown,
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
	// PercentageRunnersBusy
	horizontalRunnerAutoscalerReplicasDesired = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_replicas_desired",
			Help: "replicas_desired of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_runners",
			Help: "num_runners of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerRunnersRegistered = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_runners_registered",
			Help: "num_runners_registered of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerRunnersBusy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_runners_busy",
			Help: "num_runners_busy of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerTerminatingBusy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_terminating_busy",
			Help: "num_terminating_busy of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	// QueuedAndInProgressWorkflowRuns
	horizontalRunnerAutoscalerNecessaryReplicas = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_necessary_replicas",
			Help: "necessary_replicas of QueuedAndInProgressWorkflowRuns",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerWorkflowRunsCompleted = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_workflow_runs_completed",
			Help: "workflow_runs_completed of QueuedAndInProgressWorkflowRuns",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerWorkflowRunsInProgress = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_workflow_runs_in_progress",
			Help: "workflow_runs_in_progress of QueuedAndInProgressWorkflowRuns",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerWorkflowRunsQueued = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_workflow_runs_queued",
			Help: "workflow_runs_queued of QueuedAndInProgressWorkflowRuns",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerWorkflowRunsUnknown = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_workflow_runs_unknown",
			Help: "workflow_runs_unknown of QueuedAndInProgressWorkflowRuns",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
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

func SetHorizontalRunnerAutoscalerPercentageRunnersBusy(
	o metav1.ObjectMeta,
	enterprise string,
	organization string,
	repository string,
	kind string,
	name string,
	desiredReplicas int,
	numRunners int,
	numRunnersRegistered int,
	numRunnersBusy int,
	numTerminatingBusy int,
) {
	labels := prometheus.Labels{
		hraName:        o.Name,
		hraNamespace:   o.Namespace,
		stEnterprise:   enterprise,
		stOrganization: organization,
		stRepository:   repository,
		stKind:         kind,
		stName:         name,
	}
	horizontalRunnerAutoscalerReplicasDesired.With(labels).Set(float64(desiredReplicas))
	horizontalRunnerAutoscalerRunners.With(labels).Set(float64(numRunners))
	horizontalRunnerAutoscalerRunnersRegistered.With(labels).Set(float64(numRunnersRegistered))
	horizontalRunnerAutoscalerRunnersBusy.With(labels).Set(float64(numRunnersBusy))
	horizontalRunnerAutoscalerTerminatingBusy.With(labels).Set(float64(numTerminatingBusy))
}

func SetHorizontalRunnerAutoscalerQueuedAndInProgressWorkflowRuns(
	o metav1.ObjectMeta,
	enterprise string,
	organization string,
	repository string,
	kind string,
	name string,
	necessaryReplicas int,
	workflowRunsCompleted int,
	workflowRunsInProgress int,
	workflowRunsQueued int,
	workflowRunsUnknown int,
) {
	labels := prometheus.Labels{
		hraName:        o.Name,
		hraNamespace:   o.Namespace,
		stEnterprise:   enterprise,
		stOrganization: organization,
		stRepository:   repository,
		stKind:         kind,
		stName:         name,
	}
	horizontalRunnerAutoscalerNecessaryReplicas.With(labels).Set(float64(necessaryReplicas))
	horizontalRunnerAutoscalerWorkflowRunsCompleted.With(labels).Set(float64(workflowRunsCompleted))
	horizontalRunnerAutoscalerWorkflowRunsInProgress.With(labels).Set(float64(workflowRunsInProgress))
	horizontalRunnerAutoscalerWorkflowRunsQueued.With(labels).Set(float64(workflowRunsQueued))
	horizontalRunnerAutoscalerWorkflowRunsUnknown.With(labels).Set(float64(workflowRunsUnknown))
}
