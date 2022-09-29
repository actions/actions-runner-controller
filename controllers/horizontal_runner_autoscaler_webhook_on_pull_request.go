package controllers

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/google/go-github/v47/github"
)

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) MatchPullRequestEvent(event *github.PullRequestEvent) func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
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
