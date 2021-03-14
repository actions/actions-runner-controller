package controllers

import (
	"github.com/google/go-github/v33/github"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/pkg/actionsglob"
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

		return true
	}
}
