package actionssummerwindnet

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	prometheus_metrics "github.com/actions/actions-runner-controller/controllers/actions.summerwind.net/metrics"
	arcgithub "github.com/actions/actions-runner-controller/github"
	"github.com/google/go-github/v52/github"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultScaleUpThreshold   = 0.8
	defaultScaleDownThreshold = 0.3
	defaultScaleUpFactor      = 1.3
	defaultScaleDownFactor    = 0.7
)

func (r *HorizontalRunnerAutoscalerReconciler) suggestDesiredReplicas(ghc *arcgithub.Client, st scaleTarget, hra v1alpha1.HorizontalRunnerAutoscaler) (*int, error) {
	if hra.Spec.MinReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing minReplicas", hra.Namespace, hra.Name)
	} else if hra.Spec.MaxReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing maxReplicas", hra.Namespace, hra.Name)
	}

	metrics := hra.Spec.Metrics
	numMetrics := len(metrics)
	if numMetrics == 0 {
		// We don't default to anything since ARC 0.23.0
		// See https://github.com/actions/actions-runner-controller/issues/728
		return nil, nil
	} else if numMetrics > 2 {
		return nil, fmt.Errorf("too many autoscaling metrics configured: It must be 0 to 2, but got %d", numMetrics)
	}

	primaryMetric := metrics[0]
	primaryMetricType := primaryMetric.Type

	var (
		suggested *int
		err       error
	)

	switch primaryMetricType {
	case v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns:
		suggested, err = r.suggestReplicasByQueuedAndInProgressWorkflowRuns(ghc, st, hra, &primaryMetric)
	case v1alpha1.AutoscalingMetricTypePercentageRunnersBusy:
		suggested, err = r.suggestReplicasByPercentageRunnersBusy(ghc, st, hra, primaryMetric)
	default:
		return nil, fmt.Errorf("validating autoscaling metrics: unsupported metric type %q", primaryMetric)
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

	return r.suggestReplicasByQueuedAndInProgressWorkflowRuns(ghc, st, hra, &fallbackMetric)
}

func (r *HorizontalRunnerAutoscalerReconciler) suggestReplicasByQueuedAndInProgressWorkflowRuns(ghc *arcgithub.Client, st scaleTarget, hra v1alpha1.HorizontalRunnerAutoscaler, metrics *v1alpha1.MetricSpec) (*int, error) {
	var repos [][]string
	repoID := st.repo
	if repoID == "" {
		orgName := st.org
		if orgName == "" {
			return nil, fmt.Errorf("asserting runner deployment spec to detect bug: spec.template.organization should not be empty on this code path")
		}

		// In case it's an organizational runners deployment without any scaling metrics defined,
		// we assume that the desired replicas should always be `minReplicas + capacityReservedThroughWebhook`.
		// See https://github.com/actions/actions-runner-controller/issues/377#issuecomment-793372693
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
	listWorkflowJobs := func(user string, repoName string, runID int64) {
		if runID == 0 {
			// should not happen in reality
			r.Log.Info("Detected run with no runID of 0, ignoring the case and not scaling.", "repo_name", repoName, "run_id", runID)
			return
		}
		opt := github.ListWorkflowJobsOptions{ListOptions: github.ListOptions{PerPage: 50}}
		var allJobs []*github.WorkflowJob
		for {
			jobs, resp, err := ghc.Actions.ListWorkflowJobs(context.TODO(), user, repoName, runID, &opt)
			if err != nil {
				r.Log.Error(err, "Error listing workflow jobs")
				return // err
			}
			allJobs = append(allJobs, jobs.Jobs...)
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		if len(allJobs) == 0 {
			// GitHub API can return run with empty job array - should be ignored
			r.Log.Info("Detected run with no jobs, ignoring the case and not scaling.", "repo_name", repoName, "run_id", runID)
		} else {
		JOB:
			for _, job := range allJobs {
				runnerLabels := make(map[string]struct{}, len(st.labels))
				for _, l := range st.labels {
					runnerLabels[l] = struct{}{}
				}

				if len(job.Labels) == 0 {
					// This shouldn't usually happen
					r.Log.Info("Detected job with no labels, which is not supported by ARC. Skipping anyway.", "labels", job.Labels, "run_id", job.GetRunID(), "job_id", job.GetID())
					continue JOB
				}

				for _, l := range job.Labels {
					if l == "self-hosted" {
						continue
					}

					if _, ok := runnerLabels[l]; !ok {
						continue JOB
					}
				}

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
		workflowRuns, err := ghc.ListRepositoryWorkflowRuns(context.TODO(), user, repoName)
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
				listWorkflowJobs(user, repoName, run.GetID())
			case "queued":
				listWorkflowJobs(user, repoName, run.GetID())
			default:
				unknown++
			}
		}
	}

	necessaryReplicas := queued + inProgress

	prometheus_metrics.SetHorizontalRunnerAutoscalerQueuedAndInProgressWorkflowRuns(
		hra.ObjectMeta,
		st.enterprise,
		st.org,
		st.repo,
		st.kind,
		st.st,
		necessaryReplicas,
		completed,
		inProgress,
		queued,
		unknown,
	)

	r.Log.V(1).Info(
		fmt.Sprintf("Suggested desired replicas of %d by TotalNumberOfQueuedAndInProgressWorkflowRuns", necessaryReplicas),
		"workflow_runs_completed", completed,
		"workflow_runs_in_progress", inProgress,
		"workflow_runs_queued", queued,
		"workflow_runs_unknown", unknown,
		"namespace", hra.Namespace,
		"kind", st.kind,
		"name", st.st,
		"horizontal_runner_autoscaler", hra.Name,
	)

	return &necessaryReplicas, nil
}

func (r *HorizontalRunnerAutoscalerReconciler) suggestReplicasByPercentageRunnersBusy(ghc *arcgithub.Client, st scaleTarget, hra v1alpha1.HorizontalRunnerAutoscaler, metrics v1alpha1.MetricSpec) (*int, error) {
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

	runnerMap, err := st.getRunnerMap()
	if err != nil {
		return nil, err
	}

	var (
		enterprise   = st.enterprise
		organization = st.org
		repository   = st.repo
	)

	// ListRunners will return all runners managed by GitHub - not restricted to ns
	runners, err := ghc.ListRunners(
		ctx,
		enterprise,
		organization,
		repository)
	if err != nil {
		return nil, err
	}

	var desiredReplicasBefore int

	if v := st.replicas; v == nil {
		desiredReplicasBefore = 1
	} else {
		desiredReplicasBefore = *v
	}

	var (
		numRunners           int
		numRunnersRegistered int
		numRunnersBusy       int
		numTerminatingBusy   int
	)

	numRunners = len(runnerMap)

	busyTerminatingRunnerPods := map[string]struct{}{}

	kindLabel := LabelKeyRunnerDeploymentName
	if hra.Spec.ScaleTargetRef.Kind == "RunnerSet" {
		kindLabel = LabelKeyRunnerSetName
	}

	var runnerPodList corev1.PodList
	if err := r.List(ctx, &runnerPodList, client.InNamespace(hra.Namespace), client.MatchingLabels(map[string]string{
		kindLabel: hra.Spec.ScaleTargetRef.Name,
	})); err != nil {
		return nil, err
	}

	for _, p := range runnerPodList.Items {
		if p.Annotations[AnnotationKeyUnregistrationFailureMessage] != "" {
			busyTerminatingRunnerPods[p.Name] = struct{}{}
		}
	}

	for _, runner := range runners {
		if _, ok := runnerMap[*runner.Name]; ok {
			numRunnersRegistered++

			if runner.GetBusy() {
				numRunnersBusy++
			} else if _, ok := busyTerminatingRunnerPods[*runner.Name]; ok {
				numTerminatingBusy++
			}

			delete(busyTerminatingRunnerPods, *runner.Name)
		}
	}

	// Remaining busyTerminatingRunnerPods are runners that were not on the ListRunners API response yet
	for range busyTerminatingRunnerPods {
		numTerminatingBusy++
	}

	var desiredReplicas int
	fractionBusy := float64(numRunnersBusy+numTerminatingBusy) / float64(desiredReplicasBefore)
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
		desiredReplicas = *st.replicas
	}

	// NOTES for operators:
	//
	// - num_runners can be as twice as large as replicas_desired_before while
	//   the runnerdeployment controller is replacing RunnerReplicaSet for runner update.
	prometheus_metrics.SetHorizontalRunnerAutoscalerPercentageRunnersBusy(
		hra.ObjectMeta,
		st.enterprise,
		st.org,
		st.repo,
		st.kind,
		st.st,
		desiredReplicas,
		numRunners,
		numRunnersRegistered,
		numRunnersBusy,
		numTerminatingBusy,
	)

	r.Log.V(1).Info(
		fmt.Sprintf("Suggested desired replicas of %d by PercentageRunnersBusy", desiredReplicas),
		"replicas_desired_before", desiredReplicasBefore,
		"replicas_desired", desiredReplicas,
		"num_runners", numRunners,
		"num_runners_registered", numRunnersRegistered,
		"num_runners_busy", numRunnersBusy,
		"num_terminating_busy", numTerminatingBusy,
		"namespace", hra.Namespace,
		"kind", st.kind,
		"name", st.st,
		"horizontal_runner_autoscaler", hra.Name,
		"enterprise", enterprise,
		"organization", organization,
		"repository", repository,
	)

	return &desiredReplicas, nil
}
