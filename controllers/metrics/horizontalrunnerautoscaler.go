package metrics

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
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
		horizontalRunnerAutoscalerReplicasDesiredBefore,
		horizontalRunnerAutoscalerReplicasDesired,
		horizontalRunnerAutoscalerNumRunners,
		horizontalRunnerAutoscalerNumRunnersRegistered,
		horizontalRunnerAutoscalerNumRunnersBusy,
		horizontalRunnerAutoscalerNumTerminatingBusy,
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
	horizontalRunnerAutoscalerReplicasDesiredBefore = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_replicas_desired_before",
			Help: "replicas_desired_before of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerReplicasDesired = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_replicas_desired",
			Help: "replicas_desired of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerNumRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_num_runners",
			Help: "num_runners of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerNumRunnersRegistered = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_num_runners_registered",
			Help: "num_runners_registered of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerNumRunnersBusy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_num_runners_busy",
			Help: "num_runners_busy of PercentageRunnersBusy",
		},
		[]string{hraName, hraNamespace, stEnterprise, stOrganization, stRepository, stKind, stName},
	)
	horizontalRunnerAutoscalerNumTerminatingBusy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "horizontalrunnerautoscaler_num_terminating_busy",
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
	desiredReplicasBefore int,
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
	horizontalRunnerAutoscalerReplicasDesiredBefore.With(labels).Set(float64(desiredReplicasBefore))
	horizontalRunnerAutoscalerReplicasDesired.With(labels).Set(float64(desiredReplicas))
	horizontalRunnerAutoscalerNumRunners.With(labels).Set(float64(numRunners))
	horizontalRunnerAutoscalerNumRunnersRegistered.With(labels).Set(float64(numRunnersRegistered))
	horizontalRunnerAutoscalerNumRunnersBusy.With(labels).Set(float64(numRunnersBusy))
	horizontalRunnerAutoscalerNumTerminatingBusy.With(labels).Set(float64(numTerminatingBusy))
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
