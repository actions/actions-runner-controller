package controllers

import (
	"github.com/google/go-github/github"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

func (autoscaler *HorizontalRunnerAutoscalerWebhook) MatchPullRequestEvent(event *github.PullRequestEvent) func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
	return func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
		g := scaleUpTrigger.GitHubEvent

		if g == nil {
			return false
		}

		pr := g.PullRequest

		if pr == nil {
			return false
		}

		if !matchTriggerConditionAgainstEvent(pr.Types, event.Action) {
			return false
		}

		if !matchTriggerConditionAgainstEvent(pr.Branches, event.PullRequest.Base.Ref) {
			return false
		}

		return true
	}
}
