package controllers

import (
	"github.com/google/go-github/v33/github"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

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

		return true
	}
}
