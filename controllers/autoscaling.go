package controllers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/go-github/v32/github"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

func (r *HorizontalRunnerAutoscalerReconciler) determineDesiredReplicas(rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler) (*int, error) {
	if hra.Spec.MinReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing minReplicas", hra.Namespace, hra.Name)
	} else if hra.Spec.MaxReplicas == nil {
		return nil, fmt.Errorf("horizontalrunnerautoscaler %s/%s is missing maxReplicas", hra.Namespace, hra.Name)
	}

	var repos [][]string

	orgName := rd.Spec.Template.Spec.Organization
	if orgName == "" {
		return nil, fmt.Errorf("asserting runner deployment spec to detect bug: spec.template.organization should not be empty on this code path")
	}

	metrics := hra.Spec.Metrics
	if len(metrics) == 0 {
		return nil, fmt.Errorf("validating autoscaling metrics: one or more metrics is required")
	} else if tpe := metrics[0].Type; tpe != v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns {
		return nil, fmt.Errorf("validting autoscaling metrics: unsupported metric type %q: only supported value is %s", tpe, v1alpha1.AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns)
	}

	if len(metrics[0].RepositoryNames) == 0 {
		options := &github.RepositoryListByOrgOptions{
			Type: "private",
			Sort: "pushed",
		}
		orgRepos, _, err := r.GitHubClient.Repositories.ListByOrg(context.Background(), orgName, options)
		if err != nil {
			return nil, fmt.Errorf("[ERROR] error fetching a list of repositories for the %s organization with error message: %s", orgName, err)
		}

		if len(orgRepos) < 1 {
			return nil, fmt.Errorf("[ERROR] ListByOrg returned empty slice! Does your PAT have enough access and is it authorized to list the organizational repositories?")
		}

		for _, v := range orgRepos {
			repoName := fmt.Sprint(*v.Name)

			// We kind of already make sure that we don't use these repo's by using the `ListByOrgOptions` field, this is just an extra safeguard.
			if *v.Archived || *v.Disabled {
				continue
			}

			// Some organizations have hundreds to thousands of repositories; we only need the X most recent ones.
			if len(repos) >= 10 {
				log.Printf("[INFO] Reached the limit of repos, performing check on these repositories: %s", repos)
				break
			}
			repos = append(repos, []string{orgName, repoName})
		}
		log.Printf("[INFO] watching the following organizational repositories: %s", repos)
	} else {
		repoID := rd.Spec.Template.Spec.Repository
		if repoID == "" {
			for _, repoName := range metrics[0].RepositoryNames {
				repos = append(repos, []string{orgName, repoName})
			}
		} else {
			repo := strings.Split(repoID, "/")
			repos = append(repos, repo)
		}
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
		list, _, err := r.GitHubClient.Actions.ListRepositoryWorkflowRuns(context.TODO(), user, repoName, nil)
		if err != nil {
			return nil, err
		}

		for _, run := range list.WorkflowRuns {
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
