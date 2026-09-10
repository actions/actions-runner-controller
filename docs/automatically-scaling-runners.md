# Automatically scaling runners

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Overview

> If you are using controller version < [v0.22.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.22.0) and you are not using GHES, and so you can't set your rate limit budget, it is recommended that you use 100 replicas or fewer to prevent being rate limited.

A `RunnerDeployment` or `RunnerSet` can scale the number of runners between `minReplicas` and `maxReplicas` fields driven by either pull based scaling metrics or via a webhook event. Whether the autoscaling is driven from a webhook event or pull based metrics it is implemented by backing a `RunnerDeployment` or `RunnerSet` kind with a `HorizontalRunnerAutoscaler` kind.

**_Important!!! If you opt to configure autoscaling, ensure you remove the `replicas:` attribute in the `RunnerDeployment` / `RunnerSet` kinds that are configured for autoscaling [#206](https://github.com/actions/actions-runner-controller/issues/206#issuecomment-748601907)_**

## Anti-Flapping Configuration

For both pull driven or webhook driven scaling an anti-flapping implementation is included, by default a runner won't be scaled down within 10 minutes of it having been scaled up.

This anti-flap configuration also has the final say on if a runner can be scaled down or not regardless of the chosen scaling method.

This delay is configurable via 2 methods:

1. By setting a new default via the controller's `--default-scale-down-delay` flag
2. By setting the attribute `scaleDownDelaySecondsAfterScaleOut:` in a `HorizontalRunnerAutoscaler` kind's `spec:`.

Below is a complete basic example of one of the pull driven scaling metrics.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runner-deployment
spec:
  template:
    spec:
      repository: example/myrepo
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  # Runners in the targeted RunnerDeployment won't be scaled down
  # for 5 minutes instead of the default 10 minutes now
  scaleDownDelaySecondsAfterScaleOut: 300
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: PercentageRunnersBusy
    scaleUpThreshold: '0.75'
    scaleDownThreshold: '0.25'
    scaleUpFactor: '2'
    scaleDownFactor: '0.5'
```

## Pull Driven Scaling

> To configure webhook driven scaling see the [Webhook Driven Scaling](#webhook-driven-scaling) section

The pull based metrics are configured in the `metrics` attribute of a HRA (see snippet below). The period between polls is defined by the controller's `--sync-period` flag. If this flag isn't provided then the controller defaults to a sync period of `1m`, this can be configured in seconds or minutes.

Be aware that the shorter the sync period the quicker you will consume your rate limit budget, depending on your environment this may or may not be a risk. Consider monitoring ARCs rate limit budget when configuring this feature to find the optimal performance sync period.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 5
  # Your chosen scaling metrics here
  metrics: []
```

**Metric Options:**

**TotalNumberOfQueuedAndInProgressWorkflowRuns**

The `TotalNumberOfQueuedAndInProgressWorkflowRuns` metric polls GitHub for all pending workflow runs against a given set of repositories. The metric will scale the runner count up to the total number of pending jobs at the sync time up to the `maxReplicas` configuration.

**Benefits of this metric**
1. Supports named repositories allowing you to restrict the runner to a specified set of repositories server-side.
2. Scales the runner count based on the depth of the job queue meaning a 1:1 scaling of runners to queued jobs.
3. Like all scaling metrics, you can manage workflow allocation to the RunnerDeployment through the use of [GitHub labels](#runner-labels).

**Drawbacks of this metric**
1. A list of repositories must be included within the scaling metric. Maintaining a list of repositories may not be viable in larger environments or self-serve environments.
2. May not scale quickly enough for some users' needs. This metric is pull based and so the queue depth is polled as configured by the sync period, as a result scaling performance is bound by this sync period meaning there is a lag to scaling activity.
3. Relatively large amounts of API requests are required to maintain this metric, you may run into API rate limit issues depending on the size of your environment and how aggressive your sync period configuration is.

Example `RunnerDeployment` backed by a `HorizontalRunnerAutoscaler`:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runner-deployment
spec:
  template:
    spec:
      repository: example/myrepo
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: TotalNumberOfQueuedAndInProgressWorkflowRuns
    repositoryNames:
    # A repository name is the REPO part of `github.com/OWNER/REPO`
    - myrepo
```

**PercentageRunnersBusy**

The `HorizontalRunnerAutoscaler` will poll GitHub for the number of runners in the `busy` state which live in the RunnerDeployment's namespace, it will then scale depending on how you have configured the scale factors.

**Benefits of this metric**
1. Supports named repositories server-side the same as the `TotalNumberOfQueuedAndInProgressWorkflowRuns` metric [#313](https://github.com/actions/actions-runner-controller/pull/313)
2. Supports GitHub organization wide scaling without maintaining an explicit list of repositories, this is especially useful for those that are working at a larger scale. [#223](https://github.com/actions/actions-runner-controller/pull/223)
3. Like all scaling metrics, you can manage workflow allocation to the RunnerDeployment through the use of [GitHub labels](using-arc-runners-in-a-workflow.md#runner-labels)
4. Supports scaling desired runner count on both a percentage increase / decrease basis as well as on a fixed increase / decrease count basis [#223](https://github.com/actions/actions-runner-controller/pull/223) [#315](https://github.com/actions/actions-runner-controller/pull/315)

**Drawbacks of this metric**
1. May not scale quickly enough for some users' needs. This metric is pull based and so the number of busy runners is polled as configured by the sync period, as a result scaling performance is bound by this sync period meaning there is a lag to scaling activity.
2. We are scaling up and down based on indicative information rather than a count of the actual number of queued jobs and so the desired runner count is likely to under provision new runners or overprovision them relative to actual job queue depth, this may or may not be a problem for you.

Examples of each scaling type implemented with a `RunnerDeployment` backed by a `HorizontalRunnerAutoscaler`:

```yaml
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: PercentageRunnersBusy
    scaleUpThreshold: '0.75'    # The percentage of busy runners at which the number of desired runners are re-evaluated to scale up
    scaleDownThreshold: '0.3'   # The percentage of busy runners at which the number of desired runners are re-evaluated to scale down
    scaleUpFactor: '1.4'        # The scale up multiplier factor applied to desired count
    scaleDownFactor: '0.7'      # The scale down multiplier factor applied to desired count
```

```yaml
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: PercentageRunnersBusy
    scaleUpThreshold: '0.75'    # The percentage of busy runners at which the number of desired runners are re-evaluated to scale up
    scaleDownThreshold: '0.3'   # The percentage of busy runners at which the number of desired runners are re-evaluated to scale down
    scaleUpAdjustment: 2        # The scale up runner count added to desired count
    scaleDownAdjustment: 1      # The scale down runner count subtracted from the desired count
```

**Combining Pull Driven Scaling Metrics**

If a HorizontalRunnerAutoscaler is configured with a secondary metric of `TotalNumberOfQueuedAndInProgressWorkflowRuns`, then be aware that the controller will check the primary metric of `PercentageRunnersBusy` first and will only use the secondary metric to calculate the desired replica count if the primary metric returns 0 desired replicas.

`PercentageRunnersBusy` metrics must appear before `TotalNumberOfQueuedAndInProgressWorkflowRuns`; otherwise, the controller will fail to process the `HorizontalRunnerAutoscaler`. A valid configuration follows.

```yaml
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: PercentageRunnersBusy
    scaleUpThreshold: '0.75'    # The percentage of busy runners at which the number of desired runners are re-evaluated to scale up
    scaleDownThreshold: '0.3'   # The percentage of busy runners at which the number of desired runners are re-evaluated to scale down
    scaleUpAdjustment: 2        # The scale up runner count added to desired count
    scaleDownAdjustment: 1      # The scale down runner count subtracted from the desired count
  - type: TotalNumberOfQueuedAndInProgressWorkflowRuns
    repositoryNames:
    # A repository name is the REPO part of `github.com/OWNER/REPO`
    - myrepo
```

## Webhook Driven Scaling

> This feature requires controller version => [v0.20.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.20.0)

> To configure pull driven scaling see the [Pull Driven Scaling](#pull-driven-scaling) section

Alternatively ARC can be configured to scale based on the `workflow_job` webhook event. The primary benefit of autoscaling on webhooks compared to the pull driven scaling is that ARC is immediately notified of the scaling need.

Webhooks are processed by a separate webhook server. The webhook server receives `workflow_job` webhook events and scales RunnerDeployments / RunnerSets by updating HRAs configured for the webhook trigger. Below is an example set-up where a HRA has been configured to scale a `RunnerDeployment` from a `workflow_job` event:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runners
spec:
  template:
    spec:
      repository: example/myrepo
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runners
spec:
  minReplicas: 1
  maxReplicas: 10
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runners
  scaleUpTriggers:
    - githubEvent:
        workflowJob: {}
      duration: "30m"
```

With the `workflowJob` trigger, each event adds or subtracts a single runner. the `scaleUpTriggers.amount` field is ignored.

The `duration` field is there because event delivery is not guaranteed. If a scale-up event is received, but the corresponding
scale-down event is not, then the extra runner would be left running forever if there were not some clean-up mechanism.
The `duration` field sets the maximum amount of time to wait for a scale-down event. Scale-down happens at the 
earlier of receiving the scale-down event or the expiration of `duration` after the scale-up event is processed and
the scale-up itself is initiated.

The lifecycle of a runner provisioned from a webhook is different from that of a runner provisioned from the pull based scaling method:

1. GitHub sends a `workflow_job` event to ARC with `status=queued`
2. ARC finds the HRA with a `workflow_job` webhook scale trigger that backs a RunnerDeployment / RunnerSet with matching runner labels. (If it finds more than one match, the event is ignored.)
3. The matched HRA adds a `capacityReservation` to its list and sets it to expire at current time + `HRA.spec.scaleUpTriggers[].duration`
4. If there are fewer replicas running than `maxReplicas`, HRA adds a replica and sets the EffectiveTime of that replica to the current time

At this point there are a few things that can happen:
1. Due to idle runners already being available, the job is assigned to one of them and the new runner is left dangling due to it not being used 
2. The job gets allocated to the runner just launched
3. If there are already `maxReplicas` replicas running, the job waits for its `capacityReservation` to be assigned to one of them
 
If the runner gets assigned the job that triggered the scale up, the lifecycle looks like this:

1. The new runner gets allocated the job and processes it
2. Upon the job ending GitHub sends another `workflow_job` event to ARC but with `status=completed`
3. The HRA removes the oldest capacity reservation from its `capacityReservations` and picks a runner to terminate ensuring it isn't busy via the GitHub API beforehand

If the job has to wait for a runner because there are already `maxReplicas` replicas running, the lifecycle looks like this:
1. A `capacityReservation` is added to the list, but no scale-up happens because that would exceed `maxReplicas`
2. When one of the existing runners finishes a job, GitHub sends another `workflow_job` event to ARC but with `status=completed` (or `status=canceled` if the job was cancelled)
3. The HRA removes the oldest capacity reservation from its `capacityReservations`, the oldest waiting `capacityReservation` becomes active, and its `duration` timer starts
4. GitHub assigns a waiting job to the newly available runner

If the job is cancelled before it is allocated to a runner then the lifecycle looks like this:

1. Upon the job cancellation GitHub sends another `workflow_job` event to ARC but with `status=cancelled`
2. The HRA removes the oldest capacity reservation from its `capacityReservations` and picks a runner to terminate ensuring it isn't busy via the GitHub API beforehand

If the `status=completed` or `status=cancelled` is never delivered to ARC (which happens occasionally) then the lifecycle looks like this:

1. The scale trigger duration specified via `HRA.spec.scaleUpTriggers[].duration` elapses
2. The HRA notices that the capacity reservation has expired, removes it from HRA's `capacityReservation` list and (unless there are `maxReplicas` running and jobs waiting) terminates the expired runner ensuring it isn't busy via the GitHub API beforehand

Your `HRA.spec.scaleUpTriggers[].duration` value should be set long enough to account for the following things:

1. The potential amount of time it could take for a pod to become `Running` e.g. you need to scale horizontally because there isn't a node available +
2. The amount of time it takes for GitHub to allocate a job to that runner +
3. The amount of time it takes for the runner to notice the allocated job and starts running it +
4. The length of time it takes for the runner to complete the job

### Install with Helm

To enable this feature, you first need to install the GitHub webhook server. To install via our Helm chart,
_[see the values documentation for all configuration options](../charts/actions-runner-controller/README.md)_

```console
$ helm upgrade --install --namespace actions-runner-system --create-namespace \
             --wait actions-runner-controller actions-runner-controller/actions-runner-controller \
             --set "githubWebhookServer.enabled=true,service.type=NodePort,githubWebhookServer.ports[0].nodePort=33080"
```

The above command will result in exposing the node port 33080 for Webhook events.
Usually, you need to create an external load balancer targeted to the node port,
and register the hostname or the IP address of the external load balancer to the GitHub Webhook.

**With a custom Kubernetes ingress controller:**

> **CAUTION:** The Kubernetes ingress controllers described below is just a suggestion from the community and
> the ARC team will not provide any user support for ingress controllers as it's not a part of this project.
>
> The following guide on creating an ingress has been contributed by the awesome ARC community and is provided here as-is.
> You may, however, still be able to ask for help on the community on GitHub Discussions if you have any problems.

Kubernetes provides `Ingress` resources to let you configure your ingress controller to expose a Kubernetes service.
If you plan to expose ARC via Ingress, you might not be required to make it a `NodePort` service
(although nothing would prevent an ingress controller to expose NodePort services too):

```console
$ helm upgrade --install --namespace actions-runner-system --create-namespace \
             --wait actions-runner-controller actions-runner-controller/actions-runner-controller \
             --set "githubWebhookServer.enabled=true"
```

The command above will create a new deployment and a service for receiving Github Webhooks on the `actions-runner-system` namespace.

Now we need to expose this service so that GitHub can send these webhooks over the network with TLS protection.

You can do it in any way you prefer, here we'll suggest doing it with a k8s Ingress.
For the sake of this example we'll expose this service on the following URL:

- https://your.domain.com/actions-runner-controller-github-webhook-server

Where `your.domain.com` should be replaced by your own domain.

> Note: This step assumes you already have a configured `cert-manager` and domain name for your cluster.

Let's start by creating an Ingress file called `arc-webhook-server.yaml` with the following contents:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: actions-runner-controller-github-webhook-server
  namespace: actions-runner-system
  annotations:
    # Depending on your configuration of cert-manager
    # Cf https://cert-manager.io/docs/configuration/
    cert-manager.io/cluster-issuer: letsencrypt-http01
spec:
  ingressClassName: nginx
  tls:
  - hosts:
    - your.domain.com
    secretName: your-tls-secret-name
  rules:
    - host: your.domain.com
      http:
        paths:
          - path: /actions-runner-controller-github-webhook-server
            pathType: Prefix
            backend:
              service:
                name: actions-runner-controller-github-webhook-server
                port:
                  number: 80
```

Make sure to set the `spec.tls.secretName` to the name of your TLS secret and
`spec.tls.hosts[0]` to your own domain.

Then create this resource on your cluster with the following command:

```bash
kubectl apply -n actions-runner-system -f arc-webhook-server.yaml
```

**Configuring GitHub for sending webhooks for our newly created webhook server:**

After this step your webhook server should be ready to start receiving webhooks from GitHub.

To configure GitHub to start sending you webhooks, go to the settings page of your repository
or organization then click on `Webhooks`, then on `Add webhook`.

There set the "Payload URL" field with the webhook URL you just created,
if you followed the example ingress above the URL would be something like this:

- https://your.domain.com/actions-runner-controller-github-webhook-server

> Remember to replace `your.domain.com` with your own domain.

Then click on "Content type" and choose `application/json`.

Then click on "let me select individual events" and choose `Workflow Jobs`.

Then click on `Add Webhook`.

GitHub will then send a `ping` event to your webhook server to check if it is working, if it is you'll see a green V mark
alongside your webhook on the Settings -> Webhooks page.

Once you were able to confirm that the Webhook server is ready and running from GitHub create or update your
`HorizontalRunnerAutoscaler` resources by learning the following configuration examples.

### Install with Kustomize

To install this feature using Kustomize, add `github-webhook-server` resources to your `kustomization.yaml` file as in the example below:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
# You should already have this
- github.com/actions/actions-runner-controller/config//default?ref=v0.22.2
# Add the below!
- github.com/actions/actions-runner-controller/config//github-webhook-server?ref=v0.22.2
```

Finally, you will have to configure an ingress so that you may configure the webhook in github. An example of such ingress can be find below:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: actions-runners-webhook-server
spec:
  rules:
  - http:
      paths:
      - path: /
        backend:
          service:
            name: github-webhook-server
            port:
              number: 80
        pathType: Exact

```

## Autoscaling to/from 0

> This feature requires controller version => [v0.19.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.19.0)

The regular `RunnerDeployment` / `RunnerSet` `replicas:` attribute as well as the `HorizontalRunnerAutoscaler` `minReplicas:` attribute supports being set to 0.

The main use case for scaling from 0 is with the `HorizontalRunnerAutoscaler` kind. To scale from 0 whilst still being able to provision runners as jobs are queued we must use the `HorizontalRunnerAutoscaler` with only certain scaling configurations, only the below configurations support scaling from 0 whilst also being able to provision runners as jobs are queued:

- `TotalNumberOfQueuedAndInProgressWorkflowRuns`
- `PercentageRunnersBusy` + `TotalNumberOfQueuedAndInProgressWorkflowRuns`
- Webhook-based autoscaling

`PercentageRunnersBusy` can't be used alone for scale-from-zero as, by its definition, it needs one or more GitHub runners to become `busy` to be able to scale. If there isn't a runner to pick up a job and enter a `busy` state then the controller will never know to provision a runner to begin with as this metric has no knowledge of the job queue and is relying on using the number of busy runners as a means for calculating the desired replica count.

Webhook-based autoscaling is the best option as it is relatively easy to configure and also it can scale quickly.

## Scheduled Overrides

> This feature requires controller version => [v0.19.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.19.0)

`Scheduled Overrides` allows you to configure `HorizontalRunnerAutoscaler` so that its `spec:` gets updated only during a certain period of time. This feature is usually used for the following scenarios:

- You want to reduce your infrastructure costs by scaling your Kubernetes nodes down outside a given period
- You want to scale for scheduled spikes in workloads

The most basic usage of this feature is to set a non-repeating override:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  scheduledOverrides:
  # Override minReplicas to 100 only between 2021-06-01T00:00:00+09:00 and 2021-06-03T00:00:00+09:00
  - startTime: "2021-06-01T00:00:00+09:00"
    endTime: "2021-06-03T00:00:00+09:00"
    minReplicas: 100
  minReplicas: 1
```

A scheduled override without `recurrenceRule` is considered a one-off override, that is active between `startTime` and `endTime`. In the second scenario, it overrides `minReplicas` to `100` only between `2021-06-01T00:00:00+09:00` and `2021-06-03T00:00:00+09:00`.

A more advanced configuration is to include a `recurrenceRule` in the override:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    kind: RunnerDeployment
    # # In case the scale target is RunnerSet:
    # kind: RunnerSet
    name: example-runner-deployment
  scheduledOverrides:
  # Override minReplicas to 0 only between 0am sat to 0am mon
  - startTime: "2021-05-01T00:00:00+09:00"
    endTime: "2021-05-03T00:00:00+09:00"
    recurrenceRule:
      frequency: Weekly
      # Optional sunset datetime attribute
      # untilTime: "2022-05-01T00:00:00+09:00"
    minReplicas: 0
  minReplicas: 1
```

 A recurring override is initially active between `startTime` and `endTime`, and then it repeatedly gets activated after a certain period of time denoted by `frequency`.

`frequecy` can take one of the following values:

- `Daily`
- `Weekly`
- `Monthly`
- `Yearly`

By default, a scheduled override repeats forever. If you want it to repeat until a specific point in time, define `untilTime`. The controller creates the last recurrence of the override until the recurrence's `startTime` is equal or earlier than `untilTime`.

Do ensure that you have enough slack for `untilTime` so that a delayed or offline `actions-runner-controller` is much less likely to miss the last recurrence. For example, you might want to set `untilTime` to `M` minutes after the last recurrence's `startTime`, so that `actions-runner-controller` being offline up to `M` minutes doesn't miss the last recurrence.

**Combining Multiple Scheduled Overrides**:

In case you have a more complex scenario, try writing two or more entries under `scheduledOverrides`.

The earlier entry is prioritized higher than later entries. So you usually define one-time overrides at the top of your list, then yearly, monthly, weekly, and lastly daily overrides.

A common use case for this may be to have 1 override to scale to 0 during non-working hours and another override to scale to 0 on weekends.

## Configuring automatic termination

As of ARC 0.27.0 (unreleased as of 2022/09/30), runners can only wait for 15 seconds by default on pod termination.

This can be problematic in two scenarios:

- Scenario 1 - RunnerSet-only: You're triggering updates other than replica changes to `RunnerSet` very often- With current implementation, every update except `replicas` change to RunnerSet may result in terminating the in-progress workflow jobs to fail.
- Scenario 2 - RunnerDeployment and RunnerSet: You have another Kubernetes controller that evicts runner pods directly, not consulting ARC.

> RunnerDeployment is not affected by the Scenario 1 as RunnerDeployment-managed runners are already tolerable to unlimitedly long in-progress running job while being replaced, as it's graceful termination process is handled outside of the entrypoint and the Kubernetes' pod termination process.

To make it more reliable, please set `spec.template.spec.terminationGracePeriodSeconds` field and the `RUNNER_GRACEFUL_STOP_TIMEOUT` environment variable appropriately. **NOTE:** if you are using the default configuration of running DinD as a sidecar, you'll need to set this environment variable in both `spec.template.spec.env` as well as `spec.template.spec.dockerEnv` for RunnerDeployment objects, otherwise the `docker` container will recieve the same termination signal and exit while the remainder of the build runs.

If you want the pod to terminate in approximately 110 seconds at the latest since the termination request, try `terminationGracePeriodSeconds` of `110` and `RUNNER_GRACEFUL_STOP_TIMEOUT` of like `90`.

The difference between `terminationGracePeriodSeconds` and `RUNNER_GRACEFUL_STOP_TIMEOUT` can vary depending on your environment and cluster.

The idea is two fold:

- `RUNNER_GRACEFUL_STOP_TIMEOUT` is for giving the runner the longest possible time to wait for the in-progress job to complete. You should keep this smaller than `terminationGracePeriodSeconds` so that you don't unnecessarily cancel running jobs.
- `terminationGracePeriodSeconds` is for giving the runner the longest possible time to stop before disappear. If the pod forcefully terminated before a graceful stop, the job running within the runner pod can hang like 10 minutes in the GitHub Actions Workflow Run/Job UI. A correct value for this avoids the hang, even though it had to cancel the running job due to the approaching deadline.

> We know the default 15 seconds timeout is too short to be useful at all.
> In near future, we might raise the default to, for example, 100 seconds, so that runners that are tend to run up to 100 seconds jobs can
> terminate gracefully without failing running jobs. It will also allow the job which were running on the node that was requsted for termination
> to correct report its status as "cancelled", rather than hanging approximately 10 minutes in the Actions Web UI until it finally fails(without any specific error message).
> 100 seconds is just an example. It might be a good default in case you're using AWS EC2 Spot Instances because they tend to send
> termination notice two minutes before the termination.
> If you have any other suggestions for the default value, please share your thoughts in Discussions.

## Additional Settings

You can pass details through the spec selector. Here's an eg. of what you may like to do:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: actions-runner
  namespace: default
spec:
  replicas: 2
  template:
    metadata:
      annotations:
        cluster-autoscaler.kubernetes.io/safe-to-evict: "true"
    spec:
      priorityClassName: "high"
      nodeSelector:
        node-role.kubernetes.io/test: ""

      securityContext:
        #All level/role/type/user values will vary based on your SELinux policies.
        #See https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux_atomic_host/7/html/container_security_guide/docker_selinux_security_policy for information about SELinux with containers
        seLinuxOptions:
          level: "s0"
          role: "system_r"
          type: "super_t"
          user: "system_u"

      tolerations:
      - effect: NoSchedule
        key: node-role.kubernetes.io/test
        operator: Exists

      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              runner-deployment-name: actions-runner

      repository: mumoshu/actions-runner-controller-ci
      # The default "summerwind/actions-runner" images are available at DockerHub:
      # https://hub.docker.com/r/summerwind/actions-runner
      # You can also build your own and specify it like the below:
      image: custom-image/actions-runner:latest
      imagePullPolicy: Always
      resources:
        limits:
          cpu: "4.0"
          memory: "8Gi"
        requests:
          cpu: "2.0"
          memory: "4Gi"
      # Timeout after a node crashed or became unreachable to evict your pods somewhere else (default 5mins)
      tolerations:
        - key: "node.kubernetes.io/unreachable"
          operator: "Exists"
          effect: "NoExecute"
          tolerationSeconds: 10
      # true (default) = The runner restarts after running jobs, to ensure a clean and reproducible build environment
      # false = The runner is persistent across jobs and doesn't automatically restart
      # This directly controls the behaviour of `--once` flag provided to the github runner
      ephemeral: false
      # true (default) = A privileged docker sidecar container is included in the runner pod.
      # false = A docker sidecar container is not included in the runner pod and you can't use docker.
      # If set to false, there are no privileged container and you cannot use docker.
      dockerEnabled: false
      # Optional Docker containers network MTU
      # If your network card MTU is smaller than Docker's default 1500, you might encounter Docker networking issues.
      # To fix these issues, you should setup Docker MTU smaller than or equal to that on the outgoing network card.
      # More information:
      # - https://mlohr.com/docker-mtu/
      dockerMTU: 1500
      # Optional Docker registry mirror
      # Docker Hub has an aggressive rate-limit configuration for free plans.
      # To avoid disruptions in your CI/CD pipelines, you might want to setup an external or on-premises Docker registry mirror.
      # More information:
      # - https://docs.docker.com/docker-hub/download-rate-limit/
      # - https://cloud.google.com/container-registry/docs/pulling-cached-images
      dockerRegistryMirror: https://mirror.gcr.io/
      # false (default) = Docker support is provided by a sidecar container deployed in the runner pod.
      # true = No docker sidecar container is deployed in the runner pod but docker can be used within the runner container instead. The image summerwind/actions-runner-dind is used by default.
      dockerdWithinRunnerContainer: true
      #Optional environment variables for docker container
      # Valid only when dockerdWithinRunnerContainer=false
      dockerEnv:
        - name: HTTP_PROXY
          value: http://example.com
      # Docker sidecar container image tweaks examples below, only applicable if dockerdWithinRunnerContainer = false
      dockerdContainerResources:
        limits:
          cpu: "4.0"
          memory: "8Gi"
        requests:
          cpu: "2.0"
          memory: "4Gi"
      # Additional N number of sidecar containers
      sidecarContainers:
        - name: mysql
          image: mysql:5.7
          env:
            - name: MYSQL_ROOT_PASSWORD
              value: abcd1234
          securityContext:
            runAsUser: 0
      # workDir if not specified (default = /runner/_work)
      # You can customise this setting allowing you to change the default working directory location
      # for example, the below setting is the same as on the ubuntu-18.04 image
      workDir: /home/runner/work
      # You can mount some of the shared volumes to the dind container using dockerVolumeMounts, like any other volume mounting.
      # NOTE: in case you want to use an hostPath like the following example, make sure that Kubernetes doesn't schedule more than one runner
      # per physical host. You can achieve that by setting pod anti-affinity rules and/or resource requests/limits.
      volumes:
        - name: docker-extra
          hostPath:
            path: /mnt/docker-extra
            type: DirectoryOrCreate
        - name: repo
          hostPath:
            path: /mnt/repo
            type: DirectoryOrCreate
      dockerVolumeMounts:
        - mountPath: /var/lib/docker
          name: docker-extra
      # You can mount some of the shared volumes to the runner container using volumeMounts.
      # NOTE: Do not try to mount the volume onto the runner workdir itself as it will not work. You could mount it however on a subdirectory in the runner workdir
      # Please see https://github.com/actions/actions-runner-controller/issues/630#issuecomment-862087323 for more information.
      volumeMounts:
        - mountPath: /home/runner/work/repo
          name: repo
      # Optional storage medium type of runner volume mount.
      # More info: https://kubernetes.io/docs/concepts/storage/volumes/#emptydir
      # "" (default) = Node's default medium
      # Memory = RAM-backed filesystem (tmpfs)
      # NOTE: Using RAM-backed filesystem gives you fastest possible storage on your host nodes.
      volumeStorageMedium: ""
      # Total amount of local storage resources required for runner volume mount.
      # The default limit is undefined.
      # NOTE: You can make sure that nodes' resources are never exceeded by limiting used storage size per runner pod.
      # You can even disable the runner mount completely by setting limit to zero if dockerdWithinRunnerContainer = true.
      # Please see https://github.com/actions/actions-runner-controller/pull/674 for more information.
      volumeSizeLimit: 4Gi
      # Optional name of the container runtime configuration that should be used for pods.
      # This must match the name of a RuntimeClass resource available on the cluster.
      # More info: https://kubernetes.io/docs/concepts/containers/runtime-class
      runtimeClassName: "runc"
      # This is an advanced configuration. Don't touch it unless you know what you're doing.
      containers:
      - name: runner
        # Usually, the runner container's privileged field is derived from dockerdWithinRunnerContainer.
        # But in the case where you need to run privileged job steps even if you don't use docker/don't need dockerd within the runner container,
        # just specified `privileged: true` like this.
        # See https://github.com/actions/actions-runner-controller/issues/1282
        # Do note that specifying `privileged: false` while using dind is very likely to fail, even if you use some vm-based container runtimes
        # like firecracker and kata. Basically they run containers within dedicated micro vms and so
        # it's more like you can use `privileged: true` safer with those runtimes.
        #
        # privileged: true
```

### Status and future of this feature

Note that this feature is currently intended for use with runner pods being terminated by other Kubernetes controller and human operators, or those being replaced by ARC RunnerSet controller due to spec change(s) except `replicas`. RunnerDeployment has no issue for the scenario. non-dind runners are affected but this feature does not support those yet.

For example, a runner pod can be terminated prematurely by cluster-autoscaler when it's about to terminate the node on cluster scale down.
All the variants of RunnerDeployment and RunnerSet managed runner pods, including runners with dockerd sidecars, rootless and rootful dind runners are affected by it. For dind runner pods only, you can use this feature to fix or alleviate the issue.

To be clear, an increase/decrease in the desired replicas of RunnerDeployment and RunnerSet will never result in worklfow jobs being terminated prematurely.
That's because it's handled BEFORE the runner pod is terminated, by ARC respective controller.

For anyone interested in improving it, adding a dedicated pod finalizer for this issue will never work.
It's due to that a pod finalizer can't prevent SIGTERM from being sent when deletionTimestamp is updated to non-zero,
which triggers a Kubernetes pod termination process anyway.
What we want here is to delay the SIGTERM sent to the `actions/runner` process running within the runner container of the runner pod,
not blocking the removal of the pod resource in the Kubernetes cluster.

Also, handling all the graceful termination scenarios with a single method may or may not work.

The most viable option would be to do the graceful termination handling entirely in the SIGTERM handler within the runner entrypoint.
But this may or may not work long-term, as it's subject to terminationGracePeriodSeconds anyway and the author of this note thinks there still is
no formally defined limit for terminationGracePeriodSeconds and hence we arent' sure how long terminationGracePeriodSeconds can be set in practice.
Also, I think the max workflow job duration is approximately 24h. So Kubernetes must formally support setting terminationGracePeriodSeconds of 24h if
we are moving entirely to the entrypoint based solution.
If you have any insights about the matter, chime in to the development of ARC!

That's why we still rely on ARC's own graceful termination logic in Runner controller for the spec change and replica increase/decrease of RunnerDeployment and
replica increase/decrease of RunnerSet, even though we now have the entrypoint based graceful stop handler.

Our plan is to improve the RunnerSet to have the same logic as the Runner controller so that you don't need this feature based on the SIGTERM handler for the spec change of RunnerSet.
