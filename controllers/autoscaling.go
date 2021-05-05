package controllers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultScaleUpThreshold   = 0.8
	defaultScaleDownThreshold = 0.3
	defaultScaleUpFactor      = 1.3
	defaultScaleDownFactor    = 0.7
)

func getValueAvailableAt(now time.Time, from, to *time.Time, reservedValue int) *int {
	if to != nil && now.After(*to) {
		return nil
	}

	if from != nil && now.Before(*from) {
		return nil
	}

	return &reservedValue
}

func (r *HorizontalRunnerAutoscalerReconciler) fetchSuggestedReplicasFromCache(hra v1alpha1.HorizontalRunnerAutoscaler) *int {
	var entry *v1alpha1.CacheEntry

	for i := range hra.Status.CacheEntries {
		ent := hra.Status.CacheEntries[i]

		if ent.Key != v1alpha1.CacheEntryKeyDesiredReplicas {
			continue
		}

		if !time.Now().Before(ent.ExpirationTime.Time) {
			continue
		}

		entry = &ent

		break
	}

	if entry != nil {
		v := getValueAvailableAt(time.Now(), nil, &entry.ExpirationTime.Time, entry.Value)
		if v != nil {
			return v
		}
	}

	return nil
}

func (r *HorizontalRunnerAutoscalerReconciler) suggestDesiredReplicas(rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler) (*int, error) {
	if hra.Spec.MinReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing minReplicas", hra.Namespace, hra.Name)
	} else if hra.Spec.MaxReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing maxReplicas", hra.Namespace, hra.Name)
	}

	metrics := hra.Spec.Metrics
	numMetrics := len(metrics)
	if numMetrics == 0 {
		if len(hra.Spec.ScaleUpTriggers) == 0 {
			return r.suggestReplicasByQueuedAndInProgressWorkflowRuns(rd, hra, nil)
		}

		return nil, nil
	} else if numMetrics > 2 {
		return nil, fmt.Errorf("Too many autoscaling metrics configured: It must be 0 to 2, but got %d", numMetrics)
	}

	primaryMetric := metrics[0]
	primaryMetricType := primaryMetric.Type

	var (
		suggested *int
		err       error
	)

	switch primaryMetricType {
	case v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns:
		suggested, err = r.suggestReplicasByQueuedAndInProgressWorkflowRuns(rd, hra, &primaryMetric)
	case v1alpha1.AutoscalingMetricTypePercentageRunnersBusy:
		suggested, err = r.suggestReplicasByPercentageRunnersBusy(rd, hra, primaryMetric)
	default:
		return nil, fmt.Errorf("validting autoscaling metrics: unsupported metric type %q", primaryMetric)
	}

	if err != nil {
		return nil, err
	}

	if suggested != nil && *suggested > 0 {
		return suggested, nil
	}

	if len(metrics) == 1 {
		// This is never supposed to happen but anyway-
		// Fall-back to `minReplicas + capacityReservedThroughWebhook`.
		return nil, nil
	}

	// At this point, we are sure that there are exactly 2 Metrics entries.

	fallbackMetric := metrics[1]
	fallbackMetricType := fallbackMetric.Type

	if primaryMetricType != v1alpha1.AutoscalingMetricTypePercentageRunnersBusy ||
		fallbackMetricType != v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns {

		return nil, fmt.Errorf(
			"invalid HRA Spec: Metrics[0] of %s cannot be combined with Metrics[1] of %s: The only allowed combination is 0=PercentageRunnersBusy and 1=TotalNumberOfQueuedAndInProgressWorkflowRuns",
			primaryMetricType, fallbackMetricType,
		)
	}

	return r.suggestReplicasByQueuedAndInProgressWorkflowRuns(rd, hra, &fallbackMetric)
}

func (r *HorizontalRunnerAutoscalerReconciler) suggestReplicasByQueuedAndInProgressWorkflowRuns(rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler, metrics *v1alpha1.MetricSpec) (*int, error) {

	var repos [][]string
	repoID := rd.Spec.Template.Spec.Repository
	if repoID == "" {
		orgName := rd.Spec.Template.Spec.Organization
		if orgName == "" {
			return nil, fmt.Errorf("asserting runner deployment spec to detect bug: spec.template.organization should not be empty on this code path")
		}

		// In case it's an organizational runners deployment without any scaling metrics defined,
		// we assume that the desired replicas should always be `minReplicas + capacityReservedThroughWebhook`.
		// See https://github.com/summerwind/actions-runner-controller/issues/377#issuecomment-793372693
		if metrics == nil {
			return nil, nil
		}

		if len(metrics.RepositoryNames) == 0 {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].repositoryNames is required and must have one more more entries for organizational runner deployment")
		}

		for _, repoName := range metrics.RepositoryNames {
			repos = append(repos, []string{orgName, repoName})
		}
	} else {
		repo := strings.Split(repoID, "/")

		repos = append(repos, repo)
	}

	var total, inProgress, queued, completed, unknown int
	type callback func()
	listWorkflowJobs := func(user string, repoName string, runID int64, fallback_cb callback) {
		if runID == 0 {
			fallback_cb()
			return
		}
		jobs, _, err := r.GitHubClient.Actions.ListWorkflowJobs(context.TODO(), user, repoName, runID, nil)
		if err != nil {
			r.Log.Error(err, "Error listing workflow jobs")
			fallback_cb()
		} else if len(jobs.Jobs) == 0 {
			fallback_cb()
		} else {
			for _, job := range jobs.Jobs {
				switch job.GetStatus() {
				case "completed":
					// We add a case for `completed` so it is not counted in `unknown`.
					// And we do not increment the counter for completed because
					// that counter only refers to workflows. The reason for
					// this is because we do not get a list of jobs for
					// completed workflows in order to keep the number of API
					// calls to a minimum.
				case "in_progress":
					inProgress++
				case "queued":
					queued++
				default:
					unknown++
				}
			}
		}
	}

	for _, repo := range repos {
		user, repoName := repo[0], repo[1]
		workflowRuns, err := r.GitHubClient.ListRepositoryWorkflowRuns(context.TODO(), user, repoName)
		if err != nil {
			return nil, err
		}

		for _, run := range workflowRuns {
			total++

			// In May 2020, there are only 3 statuses.
			// Follow the below links for more details:
			// - https://developer.github.com/v3/actions/workflow-runs/#list-repository-workflow-runs
			// - https://developer.github.com/v3/checks/runs/#create-a-check-run
			switch run.GetStatus() {
			case "completed":
				completed++
			case "in_progress":
				listWorkflowJobs(user, repoName, run.GetID(), func() { inProgress++ })
			case "queued":
				listWorkflowJobs(user, repoName, run.GetID(), func() { queued++ })
			default:
				unknown++
			}
		}
	}

	necessaryReplicas := queued + inProgress

	r.Log.V(1).Info(
		fmt.Sprintf("Suggested desired replicas of %d by TotalNumberOfQueuedAndInProgressWorkflowRuns", necessaryReplicas),
		"workflow_runs_completed", completed,
		"workflow_runs_in_progress", inProgress,
		"workflow_runs_queued", queued,
		"workflow_runs_unknown", unknown,
		"namespace", hra.Namespace,
		"runner_deployment", rd.Name,
		"horizontal_runner_autoscaler", hra.Name,
	)

	return &necessaryReplicas, nil
}

func (r *HorizontalRunnerAutoscalerReconciler) suggestReplicasByPercentageRunnersBusy(rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler, metrics v1alpha1.MetricSpec) (*int, error) {
	ctx := context.Background()
	scaleUpThreshold := defaultScaleUpThreshold
	scaleDownThreshold := defaultScaleDownThreshold
	scaleUpFactor := defaultScaleUpFactor
	scaleDownFactor := defaultScaleDownFactor

	if metrics.ScaleUpThreshold != "" {
		sut, err := strconv.ParseFloat(metrics.ScaleUpThreshold, 64)
		if err != nil {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].scaleUpThreshold cannot be parsed into a float64")
		}
		scaleUpThreshold = sut
	}
	if metrics.ScaleDownThreshold != "" {
		sdt, err := strconv.ParseFloat(metrics.ScaleDownThreshold, 64)
		if err != nil {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].scaleDownThreshold cannot be parsed into a float64")
		}

		scaleDownThreshold = sdt
	}

	scaleUpAdjustment := metrics.ScaleUpAdjustment
	if scaleUpAdjustment != 0 {
		if metrics.ScaleUpAdjustment < 0 {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].scaleUpAdjustment cannot be lower than 0")
		}

		if metrics.ScaleUpFactor != "" {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[]: scaleUpAdjustment and scaleUpFactor cannot be specified together")
		}
	} else if metrics.ScaleUpFactor != "" {
		suf, err := strconv.ParseFloat(metrics.ScaleUpFactor, 64)
		if err != nil {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].scaleUpFactor cannot be parsed into a float64")
		}
		scaleUpFactor = suf
	}

	scaleDownAdjustment := metrics.ScaleDownAdjustment
	if scaleDownAdjustment != 0 {
		if metrics.ScaleDownAdjustment < 0 {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].scaleDownAdjustment cannot be lower than 0")
		}

		if metrics.ScaleDownFactor != "" {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[]: scaleDownAdjustment and scaleDownFactor cannot be specified together")
		}
	} else if metrics.ScaleDownFactor != "" {
		sdf, err := strconv.ParseFloat(metrics.ScaleDownFactor, 64)
		if err != nil {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].scaleDownFactor cannot be parsed into a float64")
		}
		scaleDownFactor = sdf
	}

	// return the list of runners in namespace. Horizontal Runner Autoscaler should only be responsible for scaling resources in its own ns.
	var runnerList v1alpha1.RunnerList

	var opts []client.ListOption

	opts = append(opts, client.InNamespace(rd.Namespace))

	selector, err := metav1.LabelSelectorAsSelector(getSelector(&rd))
	if err != nil {
		return nil, err
	}

	opts = append(opts, client.MatchingLabelsSelector{Selector: selector})

	r.Log.V(2).Info("Finding runners with selector", "ns", rd.Namespace)

	if err := r.List(
		ctx,
		&runnerList,
		opts...,
	); err != nil {
		if !kerrors.IsNotFound(err) {
			return nil, err
		}
	}

	runnerMap := make(map[string]struct{})
	for _, items := range runnerList.Items {
		runnerMap[items.Name] = struct{}{}
	}

	var (
		enterprise   = rd.Spec.Template.Spec.Enterprise
		organization = rd.Spec.Template.Spec.Organization
		repository   = rd.Spec.Template.Spec.Repository
	)

	// ListRunners will return all runners managed by GitHub - not restricted to ns
	runners, err := r.GitHubClient.ListRunners(
		ctx,
		enterprise,
		organization,
		repository)
	if err != nil {
		return nil, err
	}

	var desiredReplicasBefore int

	if v := rd.Spec.Replicas; v == nil {
		desiredReplicasBefore = 1
	} else {
		desiredReplicasBefore = *v
	}

	var (
		numRunners           int
		numRunnersRegistered int
		numRunnersBusy       int
	)

	numRunners = len(runnerList.Items)

	for _, runner := range runners {
		if _, ok := runnerMap[*runner.Name]; ok {
			numRunnersRegistered++

			if runner.GetBusy() {
				numRunnersBusy++
			}
		}
	}

	var desiredReplicas int
	fractionBusy := float64(numRunnersBusy) / float64(desiredReplicasBefore)
	if fractionBusy >= scaleUpThreshold {
		if scaleUpAdjustment > 0 {
			desiredReplicas = desiredReplicasBefore + scaleUpAdjustment
		} else {
			desiredReplicas = int(math.Ceil(float64(desiredReplicasBefore) * scaleUpFactor))
		}
	} else if fractionBusy < scaleDownThreshold {
		if scaleDownAdjustment > 0 {
			desiredReplicas = desiredReplicasBefore - scaleDownAdjustment
		} else {
			desiredReplicas = int(float64(desiredReplicasBefore) * scaleDownFactor)
		}
	} else {
		desiredReplicas = *rd.Spec.Replicas
	}

	// NOTES for operators:
	//
	// - num_runners can be as twice as large as replicas_desired_before while
	//   the runnerdeployment controller is replacing RunnerReplicaSet for runner update.

	r.Log.V(1).Info(
		fmt.Sprintf("Suggested desired replicas of %d by PercentageRunnersBusy", desiredReplicas),
		"replicas_desired_before", desiredReplicasBefore,
		"replicas_desired", desiredReplicas,
		"num_runners", numRunners,
		"num_runners_registered", numRunnersRegistered,
		"num_runners_busy", numRunnersBusy,
		"namespace", hra.Namespace,
		"runner_deployment", rd.Name,
		"horizontal_runner_autoscaler", hra.Name,
		"enterprise", enterprise,
		"organization", organization,
		"repository", repository,
	)

	return &desiredReplicas, nil
}
