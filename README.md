# actions-runner-controller

[![awesome-runners](https://img.shields.io/badge/listed%20on-awesome--runners-blue.svg)](https://github.com/jonico/awesome-runners)

This controller operates self-hosted runners for GitHub Actions on your Kubernetes cluster.

ToC:

- [Motivation](#motivation)
- [Installation](#installation)
  - [GitHub Enterprise Support](#github-enterprise-support)
- [Setting Up Authentication with GitHub API](#setting-up-authentication-with-github-api)
  - [Deploying Using GitHub App Authentication](#deploying-using-github-app-authentication)
  - [Deploying Using PAT Authentication](#deploying-using-pat-authentication)
- [Usage](#usage)
  - [Repository Runners](#repository-runners)
  - [Organization Runners](#organization-runners)
  - [Enterprise Runners](#enterprise-runners)
  - [Runner Deployments](#runnerdeployments)
    - [Note on scaling to/from 0](#note-on-scaling-tofrom-0)
  - [Autoscaling](#autoscaling)
    - [Faster Autoscaling with GitHub Webhook](#faster-autoscaling-with-github-webhook)
    - [Autoscaling to/from 0](#autoscaling-tofrom-0)
    - [Scheduled Overrides](#scheduled-overrides)
  - [Runner with DinD](#runner-with-dind)
  - [Additional Tweaks](#additional-tweaks)
  - [Runner Labels](#runner-labels)
  - [Runner Groups](#runner-groups)
  - [Using IRSA (IAM Roles for Service Accounts) in EKS](#using-irsa-iam-roles-for-service-accounts-in-eks)
  - [Stateful Runners](#stateful-runners)
  - [Software Installed in the Runner Image](#software-installed-in-the-runner-image)
  - [Common Errors](#common-errors)
- [Contributing](#contributing)

## Motivation

[GitHub Actions](https://github.com/features/actions) is a very useful tool for automating development. GitHub Actions jobs are run in the cloud by default, but you may want to run your jobs in your environment. [Self-hosted runner](https://github.com/actions/runner) can be used for such use cases, but requires the provisioning and configuration of a virtual machine instance. Instead if you already have a Kubernetes cluster, it makes more sense to run the self-hosted runner on top of it.

**actions-runner-controller** makes that possible. Just create a *Runner* resource on your Kubernetes, and it will run and operate the self-hosted runner for the specified repository. Combined with Kubernetes RBAC, you can also build simple Self-hosted runners as a Service.

## Installation

actions-runner-controller uses [cert-manager](https://cert-manager.io/docs/installation/kubernetes/) for certificate management of Admission Webhook. Make sure you have already installed cert-manager before you install. The installation instructions for cert-manager can be found below.

- [Installing cert-manager on Kubernetes](https://cert-manager.io/docs/installation/kubernetes/)

Install the custom resource and actions-runner-controller with `kubectl` or `helm`. This will create actions-runner-system namespace in your Kubernetes and deploy the required resources.

**Kubectl Deployment:**

```shell
# REPLACE "v0.18.2" with the version you wish to deploy
kubectl apply -f https://github.com/actions-runner-controller/actions-runner-controller/releases/download/v0.18.2/actions-runner-controller.yaml
```

**Helm Deployment:**

__**Note: For all configuration options for the Helm chart see the chart's [README](./charts/actions-runner-controller/README.md)

```shell
helm repo add actions-runner-controller https://actions-runner-controller.github.io/actions-runner-controller
helm upgrade --install --namespace actions-runner-system --create-namespace \
             --wait actions-runner-controller actions-runner-controller/actions-runner-controller
```

### GitHub Enterprise Support

The solution supports both GitHub Enterprise Cloud and Server editions as well as regular GitHub. Both PAT (personal access token) and GitHub App authentication works for installations that will be deploying either repository level and / or organization level runners. If you need to deploy enterprise level runners then you are restricted to PAT based authentication as GitHub doesn't support GitHub App based authentication for enterprise runners currently.

If you are deploying this solution into a GitHub Enterprise Server environment then you will need version >= [3.0.0](https://docs.github.com/en/enterprise-server@3.0/admin/release-notes#3.0.0).

When deploying the solution for a GitHub Enterprise Server environment you need to provide an additional environment variable as part of the controller deployment:

```shell
kubectl set env deploy controller-manager -c manager GITHUB_ENTERPRISE_URL=<GHEC/S URL> --namespace actions-runner-system
```

__**Note: The repository maintainers do not have an enterprise environment (cloud or server). Support for the enterprise specific feature set is community driven and on a best effort basis. PRs from the community are welcomed to add features and maintain support.**__

## Setting Up Authentication with GitHub API

There are two ways for actions-runner-controller to authenticate with the GitHub API (only 1 can be configured at a time however):

1. Using a GitHub App (not supported for enterprise level runners due to lack of support from GitHub)
2. Using a PAT

Functionality wise, there isn't much of a difference between the 2 authentication methods. The primarily benefit of authenticating via a GitHub App is an [increased API quota](https://docs.github.com/en/developers/apps/rate-limits-for-github-apps).

If you are deploying the solution for a GitHub Enterprise Server environment you are able to [configure your rate limiting settings](https://docs.github.com/en/enterprise-server@3.0/admin/configuration/configuring-rate-limits) making the main benefit irrelevant. If you're deploying the solution for a GitHub Enterprise Cloud or regular GitHub environment and you run into rate limiting issues, consider deploying the solution using the GitHub App authentication method instead.

### Deploying Using GitHub App Authentication

You can create a GitHub App for either your user account or any organization, below are the app permissions required for each supported type of runner:

_Note: Links are provided further down to create an app for your logged in user account or an organisation with the permissions for all runner types set in each link's query string_

**Required Permissions for Repository Runners:**<br />
**Repository Permissions**

* Actions (read)
* Administration (read / write)
* Metadata (read)

**Required Permissions for Organisation Runners:**<br />
**Repository Permissions**

* Actions (read)
* Metadata (read)

**Organization Permissions**
* Self-hosted runners (read / write)

_Note: All API routes mapped to their permissions can be found [here](https://docs.github.com/en/rest/reference/permissions-required-for-github-apps) if you wish to review_

---

**Setup Steps**

If you want to create a GitHub App for your account, open the following link to the creation page, enter any unique name in the "GitHub App name" field, and hit the "Create GitHub App" button at the bottom of the page.

- [Create GitHub Apps on your account](https://github.com/settings/apps/new?url=http://github.com/actions-runner-controller/actions-runner-controller&webhook_active=false&public=false&administration=write&actions=read)

If you want to create a GitHub App for your organization, replace the `:org` part of the following URL with your organization name before opening it. Then enter any unique name in the "GitHub App name" field, and hit the "Create GitHub App" button at the bottom of the page to create a GitHub App.

- [Create GitHub Apps on your organization](https://github.com/organizations/:org/settings/apps/new?url=http://github.com/actions-runner-controller/actions-runner-controller&webhook_active=false&public=false&administration=write&organization_self_hosted_runners=write&actions=read)

You will see an *App ID* on the page of the GitHub App you created as follows, the value of this App ID will be used later.

<img width="750" alt="App ID" src="https://user-images.githubusercontent.com/230145/78968802-6e7c8880-7b40-11ea-8b08-0c1b8e6a15f0.png">

Download the private key file by pushing the "Generate a private key" button at the bottom of the GitHub App page. This file will also be used later.

<img width="750" alt="Generate a private key" src="https://user-images.githubusercontent.com/230145/78968805-71777900-7b40-11ea-97e6-55c48dfc44ac.png">

Go to the "Install App" tab on the left side of the page and install the GitHub App that you created for your account or organization.

<img width="750" alt="Install App" src="https://user-images.githubusercontent.com/230145/78968806-72100f80-7b40-11ea-810d-2bd3261e9d40.png">

When the installation is complete, you will be taken to a URL in one of the following formats, the last number of the URL will be used as the Installation ID later (For example, if the URL ends in `settings/installations/12345`, then the Installation ID is `12345`).

- `https://github.com/settings/installations/${INSTALLATION_ID}`
- `https://github.com/organizations/eventreactor/settings/installations/${INSTALLATION_ID}`


Finally, register the App ID (`APP_ID`), Installation ID (`INSTALLATION_ID`), and downloaded private key file (`PRIVATE_KEY_FILE_PATH`) to Kubernetes as Secret.

**Kubectl Deployment:**

```shell
$ kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_app_id=${APP_ID} \
    --from-literal=github_app_installation_id=${INSTALLATION_ID} \
    --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH}
```

**Helm Deployment:**

Configure your values.yaml, see the chart's [README](./charts/actions-runner-controller/README.md) for deploying the secret via Helm

### Deploying Using PAT Authentication

Personal Access Tokens can be used to register a self-hosted runner by *actions-runner-controller*.

Log-in to a GitHub account that has `admin` privileges for the repository, and [create a personal access token](https://github.com/settings/tokens/new) with the appropriate scopes listed below:

**Required Scopes for Repository Runners**

* repo (Full control)

**Required Scopes for Organization Runners**

* repo (Full control)
* admin:org (Full control)
* admin:public_key (read:public_key)
* admin:repo_hook (read:repo_hook)
* admin:org_hook (Full control)
* notifications (Full control)
* workflow (Full control)

**Required Scopes for Enterprise Runners**

* enterprise:admin (Full control)

_Note: When you deploy enterprise runners they will get access to organisations, however, access to the repositories themselves is **NOT** allowed by default. Each GitHub organisation must allow enterprise runner groups to be used in repositories as an initial one time configuration step, this  only needs to be done once after which it is permanent for that runner group._

---

Once you have created the appropriate token, deploy it as a secret to your Kubernetes cluster that you are going to deploy the solution on:

**Kubectl Deployment:**

```shell
kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_token=${GITHUB_TOKEN}
```

**Helm Deployment:**

Configure your values.yaml, see the chart's [README](./charts/actions-runner-controller/README.md) for deploying the secret via Helm

## Usage

[GitHub self-hosted runners can be deployed at various levels in a management hierarchy](https://docs.github.com/en/actions/hosting-your-own-runners/about-self-hosted-runners#about-self-hosted-runners):
- The repository level
- The organization level
- The enterprise level

There are two ways to use this controller:

- Manage runners one by one with `Runner`.
- Manage a set of runners with `RunnerDeployment`.

### Repository Runners

To launch a single self-hosted runner, you need to create a manifest file includes `Runner` resource as follows. This example launches a self-hosted runner with name *example-runner* for the *actions-runner-controller/actions-runner-controller* repository.

```yaml
# runner.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: example-runner
spec:
  repository: actions-runner-controller/actions-runner-controller
  env: []
```

Apply the created manifest file to your Kubernetes.

```shell
$ kubectl apply -f runner.yaml
runner.actions.summerwind.dev/example-runner created
```

You can see that the Runner resource has been created.

```shell
$ kubectl get runners
NAME             REPOSITORY                             STATUS
example-runner   actions-runner-controller/actions-runner-controller   Running
```

You can also see that the runner pod has been running.

```shell
$ kubectl get pods
NAME           READY   STATUS    RESTARTS   AGE
example-runner 2/2     Running   0          1m
```

The runner you created has been registered to your repository.

<img width="756" alt="Actions tab in your repository settings" src="https://user-images.githubusercontent.com/230145/73618667-8cbf9700-466c-11ea-80b6-c67e6d3f70e7.png">

Now you can use your self-hosted runner. See the [official documentation](https://help.github.com/en/actions/automating-your-workflow-with-github-actions/using-self-hosted-runners-in-a-workflow) on how to run a job with it.

### Organization Runners

To add the runner to an organization, you only need to replace the `repository` field with `organization`, so the runner will register itself to the organization.

```yaml
# runner.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: example-org-runner
spec:
  organization: your-organization-name
```

Now you can see the runner on the organization level (if you have organization owner permissions).

### Enterprise Runners

To add the runner to an enterprise, you only need to replace the `repository` field with `enterprise`, so the runner will register itself to the enterprise.

```yaml
# runner.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: example-enterprise-runner
spec:
  enterprise: your-enterprise-name
```

Now you can see the runner on the enterprise level (if you have enterprise access permissions).

### RunnerDeployments

There are `RunnerReplicaSet` and `RunnerDeployment` that corresponds to `ReplicaSet` and `Deployment` but for `Runner`.

You usually need only `RunnerDeployment` rather than `RunnerReplicaSet` as the former is for managing the latter.

```yaml
# runnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  replicas: 2
  template:
    spec:
      repository: mumoshu/actions-runner-controller-ci
      env: []
```

Apply the manifest file to your cluster:

```shell
$ kubectl apply -f runnerdeployment.yaml
runnerdeployment.actions.summerwind.dev/example-runnerdeploy created
```

You can see that 2 runners have been created as specified by `replicas: 2`:

```shell
$ kubectl get runners
NAME                             REPOSITORY                             STATUS
example-runnerdeploy2475h595fr   mumoshu/actions-runner-controller-ci   Running
example-runnerdeploy2475ht2qbr   mumoshu/actions-runner-controller-ci   Running
```

##### Note on scaling to/from 0

> This feature is available since actions-runner-controller v0.19.0

You can either delete the runner deployment, or update it to have `replicas: 0`, so that there will be 0 runner pods in the cluster. This, in combination with e.g. `cluster-autoscaler`, enables you to save your infrastructure cost when there's no need to run Actions jobs.

```yaml
# runnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  replicas: 0
```

The implication of setting `replicas: 0` instead of deleting the runner deployment is that you can let GitHub Actions queue jobs until there will be one or more runners. See [#465](https://github.com/actions-runner-controller/actions-runner-controller/pull/465) for more information.

Also note that the controller creates a "registration-only" runner per RunnerReplicaSet on it's being scaled to zero,
and retains it until there are one or more runners available.

This, in combination with a correctly configured HorizontalRunnerAutoscaler, allows you to automatically [scale to/from 0](#autoscaling-tofrom-0)

### Autoscaling

__**IMPORTANT : Due to limitations / a bug with GitHub's [routing engine](https://docs.github.com/en/actions/hosting-your-own-runners/using-self-hosted-runners-in-a-workflow#routing-precedence-for-self-hosted-runners) autoscaling does NOT work correctly with RunnerDeployments that target the enterprise level. Scaling activity works as expected however jobs fail to get assigned to the scaled out replicas. This was explored in issue [#470](https://github.com/actions-runner-controller/actions-runner-controller/issues/470). Once GitHub resolves the issue with their backend service we expect the solution to be able to support autoscaled enterprise runnerdeploments without any additional changes.**__

A `RunnerDeployment` (excluding enterprise runners) can scale the number of runners between `minReplicas` and `maxReplicas` fields based the chosen scaling metric as defined in the `metrics` attribute

**Scaling Metrics**

**TotalNumberOfQueuedAndInProgressWorkflowRuns**

In the below example, `actions-runner` will poll GitHub for all pending workflows with the poll period defined by the sync period configuration. It will then scale to e.g. 3 if there're 3 pending jobs at sync time.
With this scaling metric we are required to define a list of repositories within our metric.

The scale out performance is controlled via the manager containers startup `--sync-period` argument. The default value is set to 10 minutes to prevent default deployments rate limiting themselves from the GitHub API.

**Kustomize Config :** The period can be customised in the `config/default/manager_auth_proxy_patch.yaml` patch<br />

**Benefits of this metric**
1. Supports named repositories allowing you to restrict the runner to a specified set of repositories server side.
2. Scales the runner count based on the actual queue depth of the jobs meaning a more 1:1 scaling of runners to queued jobs (caveat, see drawback #4)
3. Like all scaling metrics, you can manage workflow allocation to the RunnerDeployment through the use of [GitHub labels](#runner-labels).

**Drawbacks of this metric**
1. Repositories must be named within the scaling metric, maintaining a list of repositories may not be viable in larger environments or self-serve environments.
2. May not scale quick enough for some users needs. This metric is pull based and so the queue depth is polled as configured by the sync period, as a result scaling performance is bound by this sync period meaning there is a lag to scaling activity.
3. Relatively large amounts of API requests required to maintain this metric, you may run in API rate limiting issues depending on the size of your environment and how aggressive your sync period configuration is
4. The GitHub API doesn't provide a way to filter workflow jobs to just those targeting self-hosted runners. If your environment's workflows target both self-hosted and GitHub hosted runners then the queue depth this metric scales against isn't a true 1:1 mapping of queue depth to required runner count. As a result of this, this metric may scale too aggressively for your actual self-hosted runner count needs.


Example `RunnerDeployment` backed by a `HorizontalRunnerAutoscaler`:

_Important!!! We no longer include the attribute `replicas` in our `RunnerDeployment` if we are configuring autoscaling!_

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runner-deployment
spec:
  template:
    spec:
      repository: actions-runner-controller/actions-runner-controller
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 3
  metrics:
  - type: TotalNumberOfQueuedAndInProgressWorkflowRuns
    repositoryNames:
    - actions-runner-controller/actions-runner-controller
```

Additionally, the `HorizontalRunnerAutoscaler` also has an anti-flapping option that prevents periodic loop of scaling up and down.
By default, it doesn't scale down until the grace period of 10 minutes passes after a scale up. The grace period can be configured however by adding the setting `scaleDownDelaySecondsAfterScaleOut` in the `HorizontalRunnerAutoscaler` `spec`:

```yaml
spec:
  scaleDownDelaySecondsAfterScaleOut: 60
```

**PercentageRunnersBusy**

The `HorizontalRunnerAutoscaler` will poll GitHub based on the configuration sync period for the number of busy runners which live in the RunnerDeployment's namespace and scale based on the settings

**Kustomize Config :** The period can be customised in the `config/default/manager_auth_proxy_patch.yaml` patch<br />

**Benefits of this metric**
1. Supports named repositories server side the same as the `TotalNumberOfQueuedAndInProgressWorkflowRuns` metric [#313](https://github.com/actions-runner-controller/actions-runner-controller/pull/313)
2. Supports GitHub organization wide scaling without maintaining an explicit list of repositories, this is especially useful for those that are working at a larger scale. [#223](https://github.com/actions-runner-controller/actions-runner-controller/pull/223)
3. Like all scaling metrics, you can manage workflow allocation to the RunnerDeployment through the use of [GitHub labels](#runner-labels)
4. Supports scaling desired runner count on both a percentage increase / decrease basis as well as on a fixed increase / decrease count basis [#223](https://github.com/actions-runner-controller/actions-runner-controller/pull/223) [#315](https://github.com/actions-runner-controller/actions-runner-controller/pull/315)

**Drawbacks of this metric**
1. May not scale quick enough for some users needs. This metric is pull based and so the number of busy runners are polled as configured by the sync period, as a result scaling performance is bound by this sync period meaning there is a lag to scaling activity.
2. We are scaling up and down based on indicative information rather than a count of the actual number of queued jobs and so the desired runner count is likely to under provision new runners or overprovision them relative to actual job queue depth, this may or may not be a problem for you.


Examples of each scaling type implemented with a `RunnerDeployment` backed by a `HorizontalRunnerAutoscaler`:


_Important!!! We no longer include the attribute `replicas` in our `RunnerDeployment` if we are configuring autoscaling!_

```yaml
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 3
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
    name: example-runner-deployment
  minReplicas: 1
  maxReplicas: 3
  metrics:
  - type: PercentageRunnersBusy
    scaleUpThreshold: '0.75'    # The percentage of busy runners at which the number of desired runners are re-evaluated to scale up
    scaleDownThreshold: '0.3'   # The percentage of busy runners at which the number of desired runners are re-evaluated to scale down
    scaleUpAdjustment: 2        # The scale up runner count added to desired count
    scaleDownAdjustment: 1      # The scale down runner count subtracted from the desired count
```

Like the previous metric, the scale down factor respects the anti-flapping configuration is applied to the `HorizontalRunnerAutoscaler` as mentioned previously:

```yaml
spec:
  scaleDownDelaySecondsAfterScaleOut: 60
```

#### Faster Autoscaling with GitHub Webhook

__**IMPORTANT : Due to missing webhook events, webhook based scaling is not available for enterprise level RunnerDeployments. This was explored in issue [#470](https://github.com/actions-runner-controller/actions-runner-controller/issues/470).**__

> This feature is an ADVANCED feature which may require more work to set up.
> Please get prepared to put some time and effort to learn and leverage this feature!

`actions-runner-controller` has an optional Webhook server that receives GitHub Webhook events and scale
[`RunnerDeployments`](#runnerdeployments) by updating corresponding [`HorizontalRunnerAutoscalers`](#autoscaling).

Today, the Webhook server can be configured to respond GitHub `check_run`, `pull_request`, and `push` events
by scaling up the matching `HorizontalRunnerAutoscaler` by N replica(s), where `N` is configurable within
`HorizontalRunnerAutoscaler's` `Spec`.

More concretely, you can configure the targeted GitHub event types and the `N` in
`scaleUpTriggers`:

```yaml
kind: HorizontalRunnerAutoscaler
spec:
  scaleTargetRef:
    name: myrunners
  scaleUpTriggers:
  - githubEvent:
      checkRun:
        types: ["created"]
        status: "queued"
    amount: 1
    duration: "5m"
```

With the above example, the webhook server scales `myrunners` by `1` replica for 5 minutes on each `check_run` event
with the type of `created` and the status of `queued` received.

The primary benefit of autoscaling on Webhook compared to the standard autoscaling is that this one allows you to
immediately add "resource slack" for future GitHub Actions job runs.

In contrast, the standard autoscaling requires you to wait next sync period to add
insufficient runners. You can definitely shorten the sync period to make the standard autoscaling more responsive.
But doing so eventually result in the controller not functional due to GitHub API rate limit.

> You can learn the implementation details in [#282](https://github.com/actions-runner-controller/actions-runner-controller/pull/282)

To enable this feature, you firstly need to install the webhook server.

Currently, only our Helm chart has the ability install it.

```console
$ helm --upgrade install actions-runner-controller/actions-runner-controller \
  githubWebhookServer.enabled=true \
  githubWebhookServer.ports[0].nodePort=33080
```

The above command will result in exposing the node port 33080 for Webhook events. Usually, you need to create an
external loadbalancer targeted to the node port, and register the hostname or the IP address of the external loadbalancer
to the GitHub Webhook.

Once you were able to confirm that the Webhook server is ready and running from GitHub - this is usually verified by the
GitHub sending PING events to the Webhook server - create or update your `HorizontalRunnerAutoscaler` resources
by learning the following configuration examples.

- [Example 1: Scale up on each `check_run` event](#example-1-scale-up-on-each-check_run-event)
- [Example 2: Scale on each `pull_request` event against `develop` or `main` branches](#example-2-scale-on-each-pull_request-event-against-develop-or-main-branches)

##### Example 1: Scale up on each `check_run` event

> Note: This should work almost like https://github.com/philips-labs/terraform-aws-github-runner

To scale up replicas of the runners for `example/myrepo` by 1 for 5 minutes on each `check_run`, you write manifests like the below:

```yaml
kind: RunnerDeployment
metadata:
   name: myrunners
spec:
  repository: example/myrepo
---
kind: HorizontalRunnerAutoscaler
spec:
  scaleTargetRef:
    name: myrunners
  scaleUpTriggers:
  - githubEvent:
      checkRun:
        types: ["created"]
        status: "queued"
    amount: 1
    duration: "5m"
```

###### Example 2: Scale on each `pull_request` event against `develop` or `main` branches

```yaml
kind: RunnerDeployment:
metadata:
   name: myrunners
spec:
  repository: example/myrepo
---
kind: HorizontalRunnerAutoscaler
spec:
  scaleTargetRef:
    name: myrunners
  scaleUpTriggers:
  - githubEvent:
      pullRequest:
        types: ["synchronize"]
        branches: ["main", "develop"]
    amount: 1
    duration: "5m"
```

See ["activity types"](https://docs.github.com/en/actions/reference/events-that-trigger-workflows#pull_request) for the list of valid values for `scaleUpTriggers[].githubEvent.pullRequest.types`.


#### Autoscaling to/from 0

> This feature is available since actions-runner-controller v0.19.0

Previously, we've discussed about [how to scale a RunnerDeployment to/from 0](#note-on-scaling-tofrom-0)

To automate the process of scaling to/from 0, you can use `HorizontalRunnerAutoscaler` with a caveat.

That is, you need to choose one of the following configuration for metrics and triggers:

- `TotalNumberOfQueuedAndInProgressWorkflowRuns`
- `PercentageRunnersBusy` + `TotalNumberOfQueuedAndInProgressWorkflowRuns`
- `PercentageRunnersBusy` + Webhook-based autoscaling

This is due to that `PercentageRunnersBusy`, by its definition, needs one or more GitHub runners that can become `busy`, which cannot happen at all when you have 0 active runners.

If and only if HorizontalRunnerAutoscaler is configured to have a secondary metric of `TotalNumberOfQueuedAndInProgressWorkflowRuns` and the controller sees the primary metric of `PercentageRunnersBusy` returned 0 desired replicas, it uses the secondary metric for calculating the desired replicas once again.

A correctly configured `TotalNumberOfQueuedAndInProgressWorkflowRuns` can return non-zero desired replicas even when there are no runners other than [registration-only runners](#note-on-scaling-tofrom-0), hence the `PercentageRunnersBusy` + `TotalNumberOfQueuedAndInProgressWorkflowRuns` configuration makes scaling from zero possible.

Similarly, Webhook-based autoscaling works regardless of there are active runners, hence `PercentageRunnersBusy` + Webhook-based autoscaling configuration makes scaling from zero, too.

#### Scheduled Overrides

> This feature is available since actions-runner-controller v0.19.0

`Scheduled Overrides` allows you to configure HorizontalRunnerAutoscaler so that its Spec gets updated only during a certain period of time.

usually, this feature is used for following scenarios:

- You want to pay for your infrastructure cost running runners only in business hours
- You want to prepare for scheduled spikes in workloads

For the first scenario, you might consider configuration like the below:

```
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    name: example-runner-deployment
  scheduledOverrides:
  # Override minReplicas to 0 only between 0am sat to 0am mon
  - startTime: "2021-05-01T00:00:00+09:00"
    endTime: "2021-05-03T00:00:00+09:00"
    recurrenceRule:
      frequency: Weekly
      untilTime: "2022-05-01T00:00:00+09:00"
    minReplicas: 0
  minReplicas: 1
```

For the second scenario, you might consider something like the below:

```
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    name: example-runner-deployment
  scheduledOverrides:
  # Override minReplicas to 100 only between 2021-06-01T00:00:00+09:00 and 2021-06-03T00:00:00+09:00
  - startTime: "2021-06-01T00:00:00+09:00"
    endTime: "2021-06-03T00:00:00+09:00"
    minReplicas: 100
  minReplicas: 1
```

The most basic usage of this feature is actually the second scenario mentioned above.
A scheduled override without `recurrenceRule` is considered a one-off override, that is active between `startTime` and `endTime`. In the second scenario, it overrides `minReplicas` to `100` only between `2021-06-01T00:00:00+09:00` and `2021-06-03T00:00:00+09:00`.

A scheduled override with `recurrenceRule` is considered a recurring override. A recurring override is initially active between `startTime` and `endTime`, and then it repeatedly get activated after a certain period of time denoted by `frequency`.

`frequecy` can take one of the following values:

- `Daily`
- `Weekly`
- `Monthly`
- `Yearly`

By default, a scheduled override repeats forever. If you want it to repeat until a specific point in time, define `untilTime`. The controller create the last recurrence of the override until the recurrence's `startTime` is equal or earlier than `untilTime`.

Do note that you have enough slack for `untilTime`, so that a delayed or offline `actions-runner-controller` is much less likely to miss the last recurrence. For example, you might want to set `untilTime` to `M` minutes after the last recurrence's `startTime`, so that `actions-runner-controller` being offline up to `M` minutes doesn't miss the last recurrence.

**Combining Multiple Scheduled Overrides**:

In case you have a more complex scenarios, try writing two or more entries under `scheduledOverrides`.

The earlier entry is prioritized higher than later entries. So you usually define one-time overrides in the top of your list, then yearly, monthly, weekly, and lastly daily overrides.

### Runner with DinD

When using default runner, runner pod starts up 2 containers: runner and DinD (Docker-in-Docker). This might create issues if there's `LimitRange` set to namespace.

```yaml
# dindrunnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-dindrunnerdeploy
spec:
  replicas: 2
  template:
    spec:
      image: summerwind/actions-runner-dind
      dockerdWithinRunnerContainer: true
      repository: mumoshu/actions-runner-controller-ci
      env: []
```

This also helps with resources, as you don't need to give resources separately to docker and runner.

### Additional Tweaks

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

      repository: mumoshu/actions-runner-controller-ci
      # The default "summerwind/actions-runner" images are available at DockerHub:
      #  https://hub.docker.com/r/summerwind/actions-runner
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
      # Docker Hub has enabled rate-limiting for free plans.
      # To avoid disruptions in your CI/CD pipelines, you might want to setup an external or on-premises Docker registry mirror.
      # More information:
      # - https://docs.docker.com/docker-hub/download-rate-limit/
      # - https://cloud.google.com/container-registry/docs/pulling-cached-images
      dockerRegistryMirror: https://mirror.gcr.io/
      # false (default) = Docker support is provided by a sidecar container deployed in the runner pod.
      # true = No docker sidecar container is deployed in the runner pod but docker can be used within the runner container instead. The image summerwind/actions-runner-dind is used by default.
      dockerdWithinRunnerContainer: true
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
      # NOTE: Do not try to mount the volume onto the runner workdir itself as it will not work. You could mount it however on a sub directory in the runner workdir
      # Please see https://github.com/actions-runner-controller/actions-runner-controller/issues/630#issuecomment-862087323 for more information.
      volumeMounts:
        - mountPath: /home/runner/work/repo
          name: repo
      # Optional name of the container runtime configuration that should be used for pods.
      # This must match the name of a RuntimeClass resource available on the cluster.
      # More info: https://kubernetes.io/docs/concepts/containers/runtime-class
      runtimeClassName: "runc"
```

### Runner Labels

To run a workflow job on a self-hosted runner, you can use the following syntax in your workflow:

```yaml
jobs:
  release:
    runs-on: self-hosted
```

When you have multiple kinds of self-hosted runners, you can distinguish between them using labels. In order to do so, you can specify one or more labels in your `Runner` or `RunnerDeployment` spec.

```yaml
# runnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: custom-runner
spec:
  replicas: 1
  template:
    spec:
      repository: actions-runner-controller/actions-runner-controller
      labels:
        - custom-runner
```

Once this spec is applied, you can observe the labels for your runner from the repository or organization in the GitHub settings page for the repository or organization. You can now select a specific runner from your workflow by using the label in `runs-on`:

```yaml
jobs:
  release:
    runs-on: custom-runner
```

Note that if you specify `self-hosted` in your workflow, then this will run your job on _any_ self-hosted runner, regardless of the labels that they have.

### Runner Groups

Runner groups can be used to limit which repositories are able to use the GitHub Runner at an organization level. Runner groups have to be [created in GitHub first](https://docs.github.com/en/actions/hosting-your-own-runners/managing-access-to-self-hosted-runners-using-groups) before they can be referenced.

To add the runner to the group `NewGroup`, specify the group in your `Runner` or `RunnerDeployment` spec.

```yaml
# runnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: custom-runner
spec:
  replicas: 1
  template:
    spec:
      group: NewGroup
```

### Using IRSA (IAM Roles for Service Accounts) in EKS

`actions-runner-controller` v0.15.0 or later has support for IRSA in EKS.

As similar as for regular pods and deployments, you firstly need an existing service account with the IAM role associated.
Create one using e.g. `eksctl`. You can refer to [the EKS documentation](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) for more details.

Once you set up the service account, all you need is to add `serviceAccountName` and `fsGroup` to any pods that uses the IAM-role enabled service account.

For `RunnerDeployment`, you can set those two fields under the runner spec at `RunnerDeployment.Spec.Template`:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  template:
    spec:
      repository: USER/REO
      serviceAccountName: my-service-account
      securityContext:
        fsGroup: 1000
```


### Use with Istio

Istio 1.7.0 or greater has `holdApplicationUntilProxyStarts` added in https://github.com/istio/istio/pull/24737, which enables you to delay the `runner` container startup until the injected `istio-proxy` container finish starting. Try using it if you need to use Istio. Otherwise the runner is unlikely to work, because it fails to call any GitHub API to register itself due to `istio-proxy` being not up and running yet.

Note that there's no official Istio integration in actions-runner-controller. It should work, but it isn't covered by our acceptance test(contribution is welcomed). In addition to that, none of the actions-runner-controller maintainers use Istio daily. If you need more information, or have any issues using it, refer to the following links:

- https://github.com/actions-runner-controller/actions-runner-controller/issues/591
- https://github.com/actions-runner-controller/actions-runner-controller/pull/592
- https://github.com/istio/istio/issues/11130

### Stateful Runners

> This is a documentation about a unreleased version of actions-runner-controller.
>
> It would be great if you could try building the latest controller image following https://github.com/actions-runner-controller/actions-runner-controller#contributing if you are eager to test it early and help
> developers by reporting any bugs :smile:

`actions-runner-controller` supports `RunnerSet` API that let you deploy stateful runners. A stateful runner is designed to be able to store some data persists across GitHub Actions workflow and job runs. You might find it useful, for example, to speed up your docker builds by persisting the docker layer cache.

A basic `RunnerSet` would look like this:

```
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerSet
metadata:
  name: example
spec:
  ephemeral: false
  replicas: 2
  repository: mumoshu/actions-runner-controller-ci
  # Other mandatory fields from StatefulSet
  selector:
    matchLabels:
      app: example
  serviceName: example
  template:
    metadata:
      labels:
        app: example
```

As it is based on `StatefulSet`, `selector` and `template.medatada.labels` needs to be defined and haev the exact same set of labels. `serviceName` must be set to some non-empty string as it is also required by `StatefulSet`.

Runner-related fields like `ephemeral`, `repository`, `organization`, `enterprise`, and so on should be written directly under `spec`.

Fields like `volumeClaimTemplates` that originates from `StatefulSet` should also be written directly under `spec`.

Pod-related fields like security contexts and volumes are written under `spec.template.spec` like `StatefulSet`.

Similarly, container-related fields like resource requests and limits, container image names and tags, security context, and so on are written under `spec.template.spec.containers`. There are two reserved container `name`, `runner` and `docker`. The former is for the container that runs [actions runner](https://github.com/actions/runner) and the latter is for the container that runs a dockerd.

For a more complex example, see the below:

```
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerSet
metadata:
  name: example
spec:
  # NOTE: RunnerSet supports non-ephemeral runners only today
  ephemeral: false
  replicas: 2
  repository: mumoshu/actions-runner-controller-ci
  dockerdWithinRunnerContainer: true
  template:
    spec:
      securityContext:
        #All level/role/type/user values will vary based on your SELinux policies.
        #See https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux_atomic_host/7/html/container_security_guide/docker_selinux_security_policy for information about SELinux with containers
        seLinuxOptions: 
          level: "s0"
          role: "system_r"
          type: "super_t"
          user: "system_u"
      containers:
      - name: runner
        env: []
        resources:
          limits:
            cpu: "4.0"
            memory: "8Gi"
          requests:
            cpu: "2.0"
            memory: "4Gi"
      - name: docker
        resources:
          limits:
            cpu: "4.0"
            memory: "8Gi"
          requests:
            cpu: "2.0"
            memory: "4Gi"
```

You can also read the design and usage documentation written in the original pull request that introduced `RunnerSet` for more information.

https://github.com/actions-runner-controller/actions-runner-controller/pull/629

Under the hood, `RunnerSet` relies on Kubernetes's `StatefulSet` and Mutating Webhook. A statefulset is used to create a number of pods that has stable names and dynamically provisioned persistent volumes, so that each statefulset-managed pod gets the same persisntet volume even after restarting. A mutating webhook is used to dynamically inject a runner's "registration token" which is used to call GitHub's "Create Runner" API.

We envision that `RunnerSet` will eventually replace `RunnerDeployment`, as `RunnerSet` provides a more standard API that is easy to learn and use because it is based on `StatefulSet`, and it has a support for `volumeClaimTemplates` which is crucial to manage dynamically provisioned persistent volumes.

### Software Installed in the Runner Image

**Cloud Tooling**<br />
The project supports being deployed on the various cloud Kubernetes platforms (e.g. EKS), it does not however aim to go beyond that. No cloud specific tooling is bundled in the base runner, this is an active decision to keep the overhead of maintaining the solution manageable.

**Bundled Software**<br />
The GitHub hosted runners include a large amount of pre-installed software packages. GitHub maintain a list in README files at <https://github.com/actions/virtual-environments/tree/main/images/linux>

This solution maintains a few runner images with `latest` aligning with GitHub's Ubuntu version. Older images are maintained whilst GitHub also provides them as an option. These images do not contain all of the software installed on the GitHub runners. It contains the following subset of packages from the GitHub runners:

- Basic CLI packages
- git
- docker
- build-essentials

The virtual environments from GitHub contain a lot more software packages (different versions of Java, Node.js, Golang, .NET, etc) which are not provided in the runner image. Most of these have dedicated setup actions which allow the tools to be installed on-demand in a workflow, for example: `actions/setup-java` or `actions/setup-node`

If there is a need to include packages in the runner image for which there is no setup action, then this can be achieved by building a custom container image for the runner. The easiest way is to start with the `summerwind/actions-runner` image and installing the extra dependencies directly in the docker image:

```shell
FROM summerwind/actions-runner:latest

RUN sudo apt update -y \
  && sudo apt install YOUR_PACKAGE
  && sudo rm -rf /var/lib/apt/lists/*
```

You can then configure the runner to use a custom docker image by configuring the `image` field of a `Runner` or `RunnerDeployment`:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: custom-runner
spec:
  repository: actions-runner-controller/actions-runner-controller
  image: YOUR_CUSTOM_DOCKER_IMAGE
```

### Common Errors

#### invalid header field value

```json
2020-11-12T22:17:30.693Z	ERROR	controller-runtime.controller	Reconciler error	
{
  "controller": "runner",
  "request": "actions-runner-system/runner-deployment-dk7q8-dk5c9",
  "error": "failed to create registration token: Post \"https://api.github.com/orgs/$YOUR_ORG_HERE/actions/runners/registration-token\": net/http: invalid header field value \"Bearer $YOUR_TOKEN_HERE\\n\" for key Authorization"
}
```

**Solution**

Your base64'ed PAT token has a new line at the end, it needs to be created without a `\n` added, either:
* `echo -n $TOKEN | base64`
* Create the secret as described in the docs using the shell and documented flags

#### Runner coming up before network available

If you're running your action runners on a service mesh like Istio, you might
have problems with runner configuration accompanied by logs like:

```
....
runner Starting Runner listener with startup type: service
runner Started listener process
runner An error occurred: Not configured
runner Runner listener exited with error code 2
runner Runner listener exit with retryable error, re-launch runner in 5 seconds.
....
```

This is because the `istio-proxy` has not completed configuring itself when the
configuration script tries to communicate with the network.

**Solution**<br />

> Added originally to help users with older istio instances.
> Newer Istio instances can use Istio's `holdApplicationUntilProxyStarts` attribute ([istio/istio#11130](https://github.com/istio/istio/issues/11130)) to avoid having to delay starting up the runner.
> Please read the discussion in [#592](https://github.com/actions-runner-controller/actions-runner-controller/pull/592) for more information.

_Note: Prior to the runner immutable tag `82d1be7` or the date `2021-07-03` for the mutable tags, the environment variable referenced below was called `STARTUP_DELAY`. This has been deprecated and will be removed from the codebase later (currently we check for both). If you are using this feature please update to using `STARTUP_DELAY_IN_SECONDS` instead when you next upgrade your runner image to a current release._

You can add a delay to the runner's entrypoint script by setting the `STARTUP_DELAY_IN_SECONDS` environment
variable for the runner pod. This will cause the script to sleep X seconds, this works with any runner kind.

*Example `RunnerDeployment` with a 2 second startup delay:*
```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeployment-with-sleep
spec:
  template:
    spec:
      env:
        - name: STARTUP_DELAY_IN_SECONDS
          value: "2" # Remember! env var values must be strings.
```

# Contributing

For more details on contributing to the project (including requirements) please check out [Getting Started with Contributing](CONTRIBUTING.md).