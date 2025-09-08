# ADR 2022-10-27: Lifetime of RunnerScaleSet on Service

**Date**: 2022-10-27

**Status**: Done

## Context

We have created the RunnerScaleSet object and APIs around it on the GitHub Actions service for better support of any self-hosted runner auto-scale solution, like [actions-runner-controller](https://github.com/actions-runner-controller/actions-runner-controller).

The `RunnerScaleSet` object will represent a set of homogeneous self-hosted runners to the Actions service job routing system.

A `RunnerScaleSet` client (ARC) needs to communicate with the Actions service via HTTP long-poll in a certain protocol to get a workflow job successfully landed on one of its homogeneous self-hosted runners.

In this ADR, we discuss the following within the context of actions-runner-controller's new scaling mode:

- Who and how to create a RunnerScaleSet on the service?
- Who and how to delete a RunnerScaleSet on the service?
- What will happen to all the runners and jobs when the deletion happens?

## RunnerScaleSet creation

- `AutoScalingRunnerSet` custom resource controller will create the `RunnerScaleSet` object in the Actions service on any `AutoScalingRunnerSet` resource deployment.
- The creation is via REST API on Actions service `POST _apis/runtime/runnerscalesets`
- The creation needs to use the runner registration token (admin).
- `RunnerScaleSet.Name` == `AutoScalingRunnerSet.metadata.Name`
- The created `RunnerScaleSet` will only have 1 label and it's the `RunnerScaleSet`'s name
- `AutoScalingRunnerSet` controller will store the `RunnerScaleSet.Id` as an annotation on the k8s resource for future lookup.

## RunnerScaleSet modification

- When the user patch existing `AutoScalingRunnerSet`'s RunnerScaleSet related properly, ex: `runnerGroupName`, `runnerWorkDir`, the controller needs to make an HTTP PATCH call to the `_apis/runtime/runnerscalesets/2` endpoint in order to update the object on the service.
- We will put the deployed `AutoScalingRunnerSet` resource in an error state when the user tries to patch the resource with a different `githubConfigUrl`
  > Basically, you can't move a deployed `AutoScalingRunnerSet` across GitHub entity, repoA->repoB, repoA->OrgC, etc.
  > We evaluated blocking the change before instead of erroring at runtime and that we decided not to go down this route because it forces us to re-introduce admission webhooks (require cert-manager).

## RunnerScaleSet deletion

- `AutoScalingRunnerSet` custom resource controller will delete the `RunnerScaleSet` object in the Actions service on any `AutoScalingRunnerSet` resource deletion.
  > `AutoScalingRunnerSet` deletion will contain several steps:
  >
  > - Stop the listener app so no more new jobs coming and no more scaling up/down.
  > - Request scale down to 0
  > - Force stop all runners
  > - Wait for the scale down to 0
  > - Delete the `RunnerScaleSet` object from service via REST API
- The deletion is via REST API on Actions service `DELETE _apis/runtime/runnerscalesets/1`
- The deletion needs to use the runner registration token (admin).

The user's `RunnerScaleSet` will be deleted from the service by `DormantRunnerScaleSetCleanupJob` if the particular `AutoScalingRunnerSet` has not connected to the service for the past 7 days. We have a similar rule for self-hosted runners.

## Jobs and Runners on deletion

- `RunnerScaleSet` deletion will be blocked if there is any job assigned to a runner within the `RunnerScaleSet`, which has to scale down to 0 before deletion.
- Any job that has been assigned to the `RunnerScaleSet` but hasn't been assigned to a runner within the `RunnerScaleSet` will get thrown back to the queue and wait for assignment again.
- Any offline runners within the `RunnerScaleSet` will be deleted from the service side.
