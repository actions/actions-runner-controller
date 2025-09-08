# ADR 2022-12-27: Pick the right runner to scale down

**Date**: 2022-12-27

**Status**: Done

## Context

- A custom resource `EphemeralRunnerSet` manage a set of custom resource `EphemeralRunners`
- The `EphemeralRunnerSet` has `Replicas` in its `Spec`, and the responsibility of the `EphemeralRunnerSet_controller` is to reconcile a given `EphemeralRunnerSet` to have
  the same amount of `EphemeralRunners` as the `Spec.Replicas` defined.
- This means the `EphemeralRunnerSet_controller` will scale up the `EphemeralRunnerSet` by creating more `EphemeralRunner` in the case of the `Spec.Replicas` is higher than
  the current amount of `EphemeralRunners`.
- This also means the `EphemeralRunnerSet_controller` will scale down the `EphemeralRunnerSet` by finding some existing `EphemeralRunner` to delete in the case of
  the `Spec.Replicas` is less than the current amount of `EphemeralRunners`.

This ADR is about how can we find the right existing `EphemeralRunner` to delete when we need to scale down.

## Current approach

1. `EphemeralRunnerSet_controller` figure out how many `EphemeralRunner` it needs to delete, ex: need to scale down from 10 to 2 means we need to delete 8 `EphemeralRunner`

2. `EphemeralRunnerSet_controller` find all `EphemeralRunner` that is in the `Running` or `Pending` phase.

   > `Pending` means the `EphemeralRunner` is still probably creating and a runner has not yet configured with the Actions service.
   > `Running` means the `EphemeralRunner` is created and a runner has probably configured with Actions service, the runner may sit there idle,
   > or maybe actively running a workflow job. We don't have a clear answer for it from the ARC side. (Actions service knows it for sure)

3. `EphemeralRunnerSet_controller` make an HTTP DELETE request to the Actions service for each `EphemeralRunner` from the previous step and ask the Actions service to delete the runner via `RunnerId`.
   (The `RunnerId` is generated after the runner registered with the Actions service, and stored on the `EphemeralRunner.Status.RunnerId`)

   > - The HTTP DELETE request looks like the following:
   >   `DELETE https://pipelines.actions.githubusercontent.com/WoxlUxJHrKEzIp4Nz3YmrmLlZBonrmj9xCJ1lrzcJ9ZsD1Tnw7/_apis/distributedtask/pools/0/agents/1024`
   >   The Actions service will return 2 types of responses:
   >
   > 1. 204 (No Content): The runner with Id 1024 has been successfully removed from the service or the runner with Id 1024 doesn't exist.
   > 2. 400 (Bad Request) with JSON body that contains an error message like `JobStillRunningException`: The service can't remove this runner at this point since it has been
   >    assigned to a job request, the client won't be able to remove the runner until the runner finishes its current assigned job request.

4. `EphemeralRunnerSet_controller` will ignore any deletion error from runners that are still running a job, and keep trying deletion until the amount of `204` equals the amount of
   `EphemeralRunner` needs to delete.

## The problem with the current approach

In a busy `AutoScalingRunnerSet`, the scale up and down may happen all the time as jobs are queued up and jobs finished.

We will make way too many HTTP requests to the Actions service and ask it to try to delete a certain runner, and rely on the exception from the service to figure out what to do next.

The runner deletion request is not cheap to the service, for synchronization, the `JobStillRunningException` is raised from the DB call for the request.

So we are wasting resources on both the Actions service (extra load to the database) and the actions-runner-controller (useless outgoing HTTP requests).

In the test ARC that I deployed to Azure, the ARC controller tried to delete RunnerId 12408 for `bbq-beets/ting-test` a total of 35 times within 10 minutes.

## Root cause

The `EphemeralRunnerSet_controller` doesn't know whether a given `EphemeralRunner` is actually running a workflow job or not
(it only knows the runner is configured at the service), so it can't filter out the `EphemeralRunner`.

## Additional context

The legacy ARC's custom resource allows the runner image to leverage the RunnerJobHook feature to update the status of the runner custom resource in K8S (Mark the runner as running workflow run Id XXX).

This brings a good value to users as it can provide some insight about which runner is running which job for all the runners in the cluster and it looks pretty close to what we want to fix the [root cause](#root-cause)

However, the legacy ARC approach means the service account for running the runner pod needs to have elevated permission to update the custom resource,
this would be a big `NO` from a security point of view since we may not trust the code running inside the runner pod.

## Possible Solution

The nature of the k8s controller-runtime means we might reconcile the resource base on stale cache data.

I think our goal for the solution should be:

- Reduce wasteful HTTP requests on a scale-down as much as we can.
- We can accept that we might make 1 or 2 wasteful requests to Actions service, but we can't accept making 5/10+ of them.
- See if we can meet feature parity with what the RunnerJobHook support with compromise any security concerns.

Since the root cause of why the reconciliation can't skip an `EphemeralRunner` is that we don't know whether an `EphemeralRunner` is running a job,
a simple thought is how about we somehow attach some info to the `EphemeralRunner` to indicate it's currently running a job?

How about we send this info from the service to the auto-scaling-listener via the existing HTTP long-poll
and let the listener patch the `EphemeralRunner.Status` to indicate it's running a job?

> The listener is normally in a separate namespace with elevated permission and it's something we can trust.

Changes:

- Introduce a new message type `JobStarted` (in addition to the existing `JobAvailable/JobAssigned/JobCompleted`) on the service side, the message is sent when a runner of the `RunnerScaleSet` get assigned to a job,
  `RequestId`, `RunnerId`, and `RunnerName` will be included in the message.
- Add `RequestId (int)` to `EphemeralRunner.Status`, this will indicate which job the runner is running.
- The `AutoScalingListener` will base on the payload of this new message to patch `EphemeralRunners/RunnerName/Status` with the `RequestId`
- When `EphemeralRunnerSet_controller` try to find `EphemeralRunner` to delete on a scale down, it will skip any `EphemeralRunner` that has `EphemeralRunner.Status.RequestId` set.
- In the future, we can expose more info to this `JobStarted` message and introduce more property under `EphemeralRunner.Status` to reach feature parity with legacy ARC's RunnerJobHook
