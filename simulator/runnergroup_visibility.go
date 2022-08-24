package simulator

import (
	"context"
	"fmt"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/go-logr/logr"
)

type Simulator struct {
	Client *github.Client
	Log    logr.Logger
}

func (c *Simulator) GetRunnerGroupsVisibleToRepository(ctx context.Context, org, repo string, managed *VisibleRunnerGroups) (*VisibleRunnerGroups, error) {
	visible := NewVisibleRunnerGroups()

	if org == "" {
		panic(fmt.Sprintf("BUG: owner should not be empty in this context. repo=%v", repo))
	}

	runnerGroups, err := c.Client.ListOrganizationRunnerGroupsForRepository(ctx, org, repo)
	if err != nil {
		return visible, err
	}

	if c.Log.V(3).Enabled() {
		c.Log.V(3).Info("ListOrganizationRunnerGroupsForRepository succeeded", "runerGroups", runnerGroups)
	}

	for _, runnerGroup := range runnerGroups {
		ref := NewRunnerGroupFromGitHub(runnerGroup)

		if !managed.Includes(ref) {
			continue
		}

		visible.Add(ref)
	}

	return visible, nil
}

func (c *Simulator) hasRepoAccessToOrganizationRunnerGroup(ctx context.Context, org string, runnerGroupId int64, repo string) (bool, error) {
	repos, err := c.Client.ListRunnerGroupRepositoryAccesses(ctx, org, runnerGroupId)
	if err != nil {
		return false, err
	}

	for _, githubRepo := range repos {
		if githubRepo.GetFullName() == repo {
			return true, nil
		}
	}

	return false, nil
}
