package controllers

import (
	"github.com/google/go-github/v33/github"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) MatchCheckRunEvent(event *github.CheckRunEvent) func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
	return func(scaleUpTrigger v1alpha1.ScaleUpTrigger) bool {
		g := scaleUpTrigger.GitHubEvent

		if g == nil {
			return false
		}

		cr := g.CheckRun

		if cr == nil {
			return false
		}

		if !matchTriggerConditionAgainstEvent(cr.Types, event.Action) {
			return false
		}

		if cr.Status != "" && (event.CheckRun == nil || event.CheckRun.Status == nil || *event.CheckRun.Status != cr.Status) {
			return false
		}

		return true
	}
}
