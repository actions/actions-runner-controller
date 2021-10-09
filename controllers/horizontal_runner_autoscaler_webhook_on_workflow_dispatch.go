package controllers

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/google/go-github/v37/github"
)

// MatchPushEvent()
func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) MatchWorkflowDispatchEvent(event *github.PushEvent) func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
	return func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
		g := scaleUpTrigger.GitHubEvent

		if g == nil {
			return false
		}

		WorkflowDispatch := g.WorkflowDispatch

		if WorkflowDispatch == nil {
			return false
		}

		// event.Ref = Branch that received dispatch
		// event.Ref = refs/heads/branch-name
		if !matchTriggerConditionAgainstEvent(WorkflowDispatch.Branches, event.Ref) {
			return false
		}

		if matchTriggerConditionAgainstEvent(WorkflowDispatch.BranchesIgnore, event.Ref) {
			return false
		}

		return true
	}
}
