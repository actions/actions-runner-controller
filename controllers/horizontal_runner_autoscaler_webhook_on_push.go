package controllers

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/google/go-github/v47/github"
)

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) MatchPushEvent(event *github.PushEvent) func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
	return func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
		g := scaleUpTrigger.GitHubEvent

		if g == nil {
			return false
		}

		push := g.Push

		return push != nil
	}
}
