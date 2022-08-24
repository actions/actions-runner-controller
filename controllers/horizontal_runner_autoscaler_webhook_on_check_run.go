package controllers

import (
	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/actionsglob"
	"github.com/google/go-github/v47/github"
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

		if checkRun := event.CheckRun; checkRun != nil && len(cr.Names) > 0 {
			for _, pat := range cr.Names {
				if r := actionsglob.Match(pat, checkRun.GetName()); r {
					return true
				}
			}

			return false
		}

		if len(scaleUpTrigger.GitHubEvent.CheckRun.Repositories) > 0 {
			for _, repository := range scaleUpTrigger.GitHubEvent.CheckRun.Repositories {
				if repository == *event.Repo.Name {
					return true
				}
			}

			return false
		}

		return true
	}
}
