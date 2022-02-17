package controllers

import (
	"context"
	"fmt"

	"github.com/actions-runner-controller/actions-runner-controller/github"
)

// unregisterRunner unregisters the runner from GitHub Actions by name.
//
// This function returns:
// - (true, nil) when it has successfully unregistered the runner.
// - (false, nil) when the runner has been already unregistered.
// - (false, err) when it postponed unregistration due to the runner being busy, or it tried to unregister the runner but failed due to
//   an error returned by GitHub API.
func unregisterRunner(ctx context.Context, client *github.Client, enterprise, org, repo, name string) (bool, error) {
	runners, err := client.ListRunners(ctx, enterprise, org, repo)
	if err != nil {
		return false, err
	}

	id := int64(0)
	for _, runner := range runners {
		if runner.GetName() == name {
			// Note that sometimes a runner can stuck "busy" even though it is already "offline".
			// But we assume that it's not actually offline and still running a job.
			if runner.GetBusy() {
				return false, fmt.Errorf("runner is busy")
			}
			id = runner.GetID()
			break
		}
	}

	if id == int64(0) {
		return false, nil
	}

	// Trying to remove a busy runner can result in errors like the following:
	//    failed to remove runner: DELETE https://api.github.com/repos/actions-runner-controller/mumoshu-actions-test/actions/runners/47: 422 Bad request - Runner \"example-runnerset-0\" is still running a job\" []
	//
	// TODO: Probably we can just remove the runner by ID without seeing if the runner is busy, by treating it as busy when a remove-runner call failed with 422?
	if err := client.RemoveRunner(ctx, enterprise, org, repo, id); err != nil {
		return false, err
	}

	return true, nil
}
