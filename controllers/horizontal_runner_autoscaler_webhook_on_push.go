package controllers

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/google/go-github/v37/github"
)

// MatchPushEvent()
func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) MatchPushEvent(event *github.PushEvent) func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
	return func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
		g := scaleUpTrigger.GitHubEvent

		if g == nil {
			return false
		}

		push := g.Push

		if push == nil {
			return false
		}

		// event.Ref = refs/heads/branch-name
		if !matchTriggerConditionAgainstEvent(push.Branches, event.Ref) {
			return false
		}

		if matchTriggerConditionAgainstEvent(push.BranchesIgnore, event.Ref) {
			return false
		}

		return true
	}
}
