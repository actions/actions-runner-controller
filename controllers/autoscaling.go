package controllers

import (
	"context"
	"errors"
	"fmt"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"strings"
)

func (r *HorizontalRunnerAutoscalerReconciler) determineDesiredReplicas(rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler) (*int, error) {
	if hra.Spec.MinReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing minReplicas", hra.Namespace, hra.Name)
	} else if hra.Spec.MaxReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing maxReplicas", hra.Namespace, hra.Name)
	}

	var repos [][]string

	repoID := rd.Spec.Template.Spec.Repository
	if repoID == "" {
		orgName := rd.Spec.Template.Spec.Organization
		if orgName == "" {
			return nil, fmt.Errorf("asserting runner deployment spec to detect bug: spec.template.organization should not be empty on this code path")
		}

		metrics := hra.Spec.Metrics

		if len(metrics) == 0 {
			return nil, fmt.Errorf("validating autoscaling metrics: one or more metrics is required")
		} else if tpe := metrics[0].Type; tpe != v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns {
			return nil, fmt.Errorf("validting autoscaling metrics: unsupported metric type %q: only supported value is %s", tpe, v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns)
		} else if len(metrics[0].RepositoryNames) == 0 {
			return nil, errors.New("validating autoscaling metrics: spec.autoscaling.metrics[].repositoryNames is required and must have one more more entries for organizational runner deployment")
		}

		for _, repoName := range metrics[0].RepositoryNames {
			repos = append(repos, []string{orgName, repoName})
		}
	} else {
		repo := strings.Split(repoID, "/")

		repos = append(repos, repo)
	}

	var total, inProgress, queued, completed, unknown int

	for _, repo := range repos {
		user, repoName := repo[0], repo[1]
		list, _, err := r.GitHubClient.Actions.ListRepositoryWorkflowRuns(context.TODO(), user, repoName, nil)
		if err != nil {
			return nil, err
		}

		for _, r := range list.WorkflowRuns {
			total++

			// In May 2020, there are only 3 statuses.
			// Follow the below links for more details:
			// - https://developer.github.com/v3/actions/workflow-runs/#list-repository-workflow-runs
			// - https://developer.github.com/v3/checks/runs/#create-a-check-run
			switch r.GetStatus() {
			case "completed":
				completed++
			case "in_progress":
				inProgress++
			case "queued":
				queued++
			default:
				unknown++
			}
		}
	}

	minReplicas := *hra.Spec.MinReplicas
	maxReplicas := *hra.Spec.MaxReplicas
	necessaryReplicas := queued + inProgress

	var desiredReplicas int

	if necessaryReplicas < minReplicas {
		desiredReplicas = minReplicas
	} else if necessaryReplicas > maxReplicas {
		desiredReplicas = maxReplicas
	} else {
		desiredReplicas = necessaryReplicas
	}

	rd.Status.Replicas = &desiredReplicas
	replicas := desiredReplicas

	r.Log.V(1).Info(
		"Calculated desired replicas",
		"computed_replicas_desired", desiredReplicas,
		"spec_replicas_min", minReplicas,
		"spec_replicas_max", maxReplicas,
		"workflow_runs_completed", completed,
		"workflow_runs_in_progress", inProgress,
		"workflow_runs_queued", queued,
		"workflow_runs_unknown", unknown,
	)

	return &replicas, nil
}
