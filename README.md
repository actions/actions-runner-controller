# actions-runner-controller

[![awesome-runners](https://img.shields.io/badge/listed%20on-awesome--runners-blue.svg)](https://github.com/jonico/awesome-runners)

This controller operates self-hosted runners for GitHub Actions on your Kubernetes cluster.

ToC:

- [Motivation](#motivation)
- [Installation](#installation)
  - [GitHub Enterprise support](#github-enterprise-support)
- [Setting up authentication with GitHub API](#setting-up-authentication-with-github-api)
  - [Deploying using GitHub App Authentication](#deploying-using-github-app-authentication)
  - [Deploying using PAT Authentication](#deploying-using-pat-authentication)
- [Usage](#usage)
  - [Repository Runners](#repository-runners)
  - [Organization Runners](#organization-runners)
  - [Runner Deployments](#runnerdeployments)
    - [Autoscaling](#autoscaling)
      - [Faster Autoscaling with GitHub Webhook](#faster-autoscaling-with-github-webhook)
  - [Runner with DinD](#runner-with-dind)
  - [Additional tweaks](#additional-tweaks)
  - [Runner labels](#runner-labels)
  - [Runner groups](#runner-groups)
  - [Using EKS IAM role for service accounts](#using-eks-iam-role-for-service-accounts)
  - [Software installed in the runner image](#software-installed-in-the-runner-image)
  - [Common errors](#common-errors)
- [Developing](#developing)
- [Alternatives](#alternatives)

## Motivation

[GitHub Actions](https://github.com/features/actions) is a very useful tool for automating development. GitHub Actions jobs are run in the cloud by default, but you may want to run your jobs in your environment. [Self-hosted runner](https://github.com/actions/runner) can be used for such use cases, but requires the provisioning and configuration of a virtual machine instance. Instead if you already have a Kubernetes cluster, it makes more sense to run the self-hosted runner on top of it.

**actions-runner-controller** makes that possible. Just create a *Runner* resource on your Kubernetes, and it will run and operate the self-hosted runner for the specified repository. Combined with Kubernetes RBAC, you can also build simple Self-hosted runners as a Service.

## Installation

actions-runner-controller uses [cert-manager](https://cert-manager.io/docs/installation/kubernetes/) for certificate management of Admission Webhook. Make sure you have already installed cert-manager before you install. The installation instructions for cert-manager can be found below.

- [Installing cert-manager on Kubernetes](https://cert-manager.io/docs/installation/kubernetes/)

Install the custom resource and actions-runner-controller with `kubectl` or `helm`. This will create actions-runner-system namespace in your Kubernetes and deploy the required resources.

`kubectl`:

```shell
# REPLACE "v0.17.0" with the version you wish to deploy
kubectl apply -f https://github.com/summerwind/actions-runner-controller/releases/download/v0.17.0/actions-runner-controller.yaml
```

`helm`:

```shell
helm repo add actions-runner-controller https://summerwind.github.io/actions-runner-controller
helm upgrade --install -n actions-runner-system actions-runner-controller/actions-runner-controller
```

### Github Enterprise support

If you use either Github Enterprise Cloud or Server, you can use **actions-runner-controller**  with those, too.
Authentication works same way as with public Github (repo and organization level).
The minimum version of Github Enterprise Server is 3.0.0 (or rc1/rc2).
__**NOTE : The maintainers do not have an Enterprise environment to be able to test changes and so are reliant on the community for testing, support is a best endeavors basis only and is community driven**__

```shell
kubectl set env deploy controller-manager -c manager GITHUB_ENTERPRISE_URL=<GHEC/S URL> --namespace actions-runner-system
```

#### Enterprise runners usage

In order to use enterprise runners you must have Admin access to Github Enterprise and you should do Personal Access Token (PAT)
with `enterprise:admin` access. Enterprise runners are not possible to run with Github APP or any other permission.

When you use enterprise runners those will get access to Github Organisations. However, access to the repositories is **NOT**
allowed by default. Each Github Organisation must allow Enterprise runner groups to be used in repositories.
This is needed only one time and is permanent after that.

Example:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: ghe-runner-deployment
spec:
  replicas: 2
  template:
    spec:
      enterprise: your-enterprise-name
      dockerdWithinRunnerContainer: true
      resources:
        limits:
          cpu: "4000m"
          memory: "2Gi"
        requests:
          cpu: "200m"
          memory: "200Mi"
      volumeMounts:
      - mountPath: /runner
        name: runner
      volumes:
      - name: runner
        emptyDir: {}

```

## Setting up authentication with GitHub API

There are two ways for actions-runner-controller to authenticate with the GitHub API (only 1 can be configured at a time however):

1. Using GitHub App.
2. Using Personal Access Token.

Functionality wise there isn't a difference between the 2 authentication methods. There are however some benefits to using a GitHub App for authentication over a PAT such as an [increased API quota](https://docs.github.com/en/developers/apps/rate-limits-for-github-apps), if you run into rate limiting consider deploying this solution using GitHub App authentication instead.

### Deploying using GitHub App Authentication

You can create a GitHub App for either your account or any organization. If you want to create a GitHub App for your account, open the following link to the creation page, enter any unique name in the "GitHub App name" field, and hit the "Create GitHub App" button at the bottom of the page.

- [Create GitHub Apps on your account](https://github.com/settings/apps/new?url=http://github.com/summerwind/actions-runner-controller&webhook_active=false&public=false&administration=write&actions=read)

If you want to create a GitHub App for your organization, replace the `:org` part of the following URL with your organization name before opening it. Then enter any unique name in the "GitHub App name" field, and hit the "Create GitHub App" button at the bottom of the page to create a GitHub App.

- [Create GitHub Apps on your organization](https://github.com/organizations/:org/settings/apps/new?url=http://github.com/summerwind/actions-runner-controller&webhook_active=false&public=false&administration=write&organization_self_hosted_runners=write&actions=read)

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

```shell
$ kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_app_id=${APP_ID} \
    --from-literal=github_app_installation_id=${INSTALLATION_ID} \
    --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH}
```

### Deploying using PAT Authentication

Personal Acess Token can be used to register a self-hosted runner by *actions-runner-controller*.

Self-hosted runners in GitHub can either be connected to a single repository, or to a GitHub organization (so they are available to all repositories in the organization). How you plan on using the runner will affect what scopes are needed for the token.

Log-in to a GitHub account that has `admin` privileges for the repository, and [create a personal access token](https://github.com/settings/tokens/new) with the appropriate scopes listed below:

**Scopes for a Repository Runner**

* repo (Full control)

**Scopes for a Organization Runner**

* repo (Full control)
* admin:org (Full control)
* admin:public_key - read:public_key
* admin:repo_hook - read:repo_hook
* admin:org_hook
* notifications
* workflow

Once you have created the appropriate token, deploy it as a secret to your kubernetes cluster that you are going to deploy the solution on:

```shell
kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_token=${GITHUB_TOKEN}
```

## Usage

There are two ways to use this controller:

- Manage runners one by one with `Runner`.
- Manage a set of runners with `RunnerDeployment`.

### Repository Runners

To launch a single self-hosted runner, you need to create a manifest file includes *Runner* resource as follows. This example launches a self-hosted runner with name *example-runner* for the *summerwind/actions-runner-controller* repository.

```yaml
# runner.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: example-runner
spec:
  repository: summerwind/actions-runner-controller
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
example-runner   summerwind/actions-runner-controller   Running
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
$ kubectl apply -f runner.yaml
runnerdeployment.actions.summerwind.dev/example-runnerdeploy created
```

You can see that 2 runners have been created as specified by `replicas: 2`:

```shell
$ kubectl get runners
NAME                             REPOSITORY                             STATUS
example-runnerdeploy2475h595fr   mumoshu/actions-runner-controller-ci   Running
example-runnerdeploy2475ht2qbr   mumoshu/actions-runner-controller-ci   Running
```

#### Autoscaling

A `RunnerDeployment` can scale the number of runners between `minReplicas` and `maxReplicas` fields based the chosen scaling metric as defined in the `metrics` attribute

**Scaling Metrics**

**TotalNumberOfQueuedAndInProgressWorkflowRuns**

In the below example, `actions-runner` will poll GitHub for all pending workflows with the poll period defined by the sync period configuration. It will then scale to e.g. 3 if there're 3 pending jobs at sync time.
With this scaling metric we are required to define a list of repositories within our metric.

The scale out performance is controlled via the manager containers startup `--sync-period` argument. The default value is set to 10 minutes to prevent default deployments rate limiting themselves from the GitHub API.

**Kustomize Config :** The period can be customised in the `config/default/manager_auth_proxy_patch.yaml` patch<br />
**Helm Config :** `syncPeriod`

**Benefits of this metric**
1. Supports named repositories allowing you to restrict the runner to a specified set of repositories server side.
2. Scales the runner count based on the actual queue depth of the jobs meaning a more 1:1 scaling of runners to queued jobs.
3. Like all scaling metrics, you can manage workflow allocation to the RunnerDeployment through the use of [Github labels](#runner-labels).

**Drawbacks of this metric**
1. Repositories must be named within the scaling metric, maintaining a list of repositories may not be viable in larger environments or self-serve environments.
2. May not scale quick enough for some users needs. This metric is pull based and so the queue depth is polled as configured by the sync period, as a result scaling performance is bound by this sync period meaning there is a lag to scaling activity.
3. Relatively large amounts of API requests required to maintain this metric, you may run in API rate limiting issues depending on the size of your environment and how aggressive your sync period configuration is


Example `RunnerDeployment` backed by a `HorizontalRunnerAutoscaler`


```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runner-deployment
spec:
  template:
    spec:
      repository: summerwind/actions-runner-controller
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
    - summerwind/actions-runner-controller
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
**Helm Config :** `syncPeriod`

**Benefits of this metric**
1. Supports named repositories server side the same as the `TotalNumberOfQueuedAndInProgressWorkflowRuns` metric [#313](https://github.com/summerwind/actions-runner-controller/pull/313)
2. Supports GitHub organisation wide scaling without maintaining an explicit list of repositories, this is especially useful for those that are working at a larger scale. [#223](https://github.com/summerwind/actions-runner-controller/pull/223)
3. Like all scaling metrics, you can manage workflow allocation to the RunnerDeployment through the use of [Github labels](#runner-labels)
4. Supports scaling desired runner count on both a percentage increase / decrease basis as well as on a fixed increase / decrease count basis [#223](https://github.com/summerwind/actions-runner-controller/pull/223) [#315](https://github.com/summerwind/actions-runner-controller/pull/315)

**Drawbacks of this metric**
1. May not scale quick enough for some users needs. This metric is pull based and so the number of busy runners are polled as configured by the sync period, as a result scaling performance is bound by this sync period meaning there is a lag to scaling activity.
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
    ScaleUpAdjustment: '2'      # The scale up runner count added to desired count
    ScaleDownAdjustment: '1'    # The scale down runner count subtracted from the desired count
```

Like the previous metric, the scale down factor respects the anti-flapping configuration is applied to the `HorizontalRunnerAutoscaler` as mentioned previously:

```yaml
spec:
  scaleDownDelaySecondsAfterScaleOut: 60
```

#### Faster Autoscaling with GitHub Webhook

> This feature is an ADVANCED feature which may require more work to set up.
> Please get prepared to put some time and effort to learn and leverage this feature!

`actions-runner-controller` has an optional Webhook server that receives GitHub Webhook events and scale
[`RunnerDeployments`](#runnerdeployments) by updating corresponding [`HorizontalRunnerAutoscalers`](#autoscaling).

Today, the Webhook server can be configured to respond GitHub `check_run`, `pull_request`, and `push` events
by scaling up the matching `HorizontalRunnerAutoscaler` by N replica(s), where `N` is configurable within
`HorizontalRunerAutoscaler's` `Spec`.

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

> You can learn the implementation details in #282

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

### Additional tweaks

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
    spec:
      nodeSelector:
        node-role.kubernetes.io/test: ""

      tolerations:
      - effect: NoSchedule
        key: node-role.kubernetes.io/test
        operator: Exists

      repository: mumoshu/actions-runner-controller-ci
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
      # true (default) = A privileged docker sidecar container is included in the runner pod.
      # false = A docker sidecar container is not included in the runner pod and you can't use docker.
      # If set to false, there are no privileged container and you cannot use docker.
      dockerEnabled: false
      # false (default) = Docker support is provided by a sidecar container deployed in the runner pod.
      # true = No docker sidecar container is deployed in the runner pod but docker can be used within teh runner container instead. The image summerwind/actions-runner-dind is used by default.
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
```

### Runner labels

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
      repository: summerwind/actions-runner-controller
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

Runner groups can be used to limit which repositories are able to use the GitHub Runner at an Organisation level. Runner groups have to be [created in GitHub first](https://docs.github.com/en/actions/hosting-your-own-runners/managing-access-to-self-hosted-runners-using-groups) before they can be referenced.

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

### Using EKS IAM role for service accounts

`actions-runner-controller` v0.15.0 or later has support for EKS IAM role for service accounts.

As similar as for regular pods and deployments, you firstly need an existing service account with the IAM role associated.
Create one using e.g. `eksctl`. You can refer to [the EKS documentation](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) for more details.

Once you set up the service account, all you need is to add `serviceAccountName` and `fsGroup` to any pods that uses
the IAM-role enabled service account.

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
        fsGroup: 1447
```

### Software installed in the runner image

The GitHub hosted runners include a large amount of pre-installed software packages. For Ubuntu 18.04, this list can be found at <https://github.com/actions/virtual-environments/blob/master/images/linux/Ubuntu1804-README.md>

The container image is based on Ubuntu 18.04, but it does not contain all of the software installed on the GitHub runners. It contains the following subset of packages from the GitHub runners:

- Basic CLI packages
- git (2.26)
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
  repository: summerwind/actions-runner-controller
  image: YOUR_CUSTOM_DOCKER_IMAGE
```

### Common Errors

#### invalid header field value

```json
2020-11-12T22:17:30.693Z	ERROR	controller-runtime.controller	Reconciler error	{"controller": "runner", "request": "actions-runner-system/runner-deployment-dk7q8-dk5c9", "error": "failed to create registration token: Post \"https://api.github.com/orgs/$YOUR_ORG_HERE/actions/runners/registration-token\": net/http: invalid header field value \"Bearer $YOUR_TOKEN_HERE\\n\" for key Authorization"}
```

**Solutions**<br />
Your base64'ed PAT token has a new line at the end, it needs to be created without a `\n` added
* `echo -n $TOKEN | base64`
* Create the secret as described in the docs using the shell and documeneted flags

# Developing

If you'd like to modify the controller to fork or contribute, I'd suggest using the following snippet for running
the acceptance test:

```shell
# This sets `VERSION` envvar to some appropriate value
. hack/make-env.sh

NAME=$DOCKER_USER/actions-runner-controller \
  GITHUB_TOKEN=*** \
  APP_ID=*** \
  PRIVATE_KEY_FILE_PATH=path/to/pem/file \
  INSTALLATION_ID=*** \
  make docker-build docker-push acceptance
```

Please follow the instructions explained in [Using Personal Access Token](#using-personal-access-token) to obtain
`GITHUB_TOKEN`, and those in [Using GitHub App](#using-github-app) to obtain `APP_ID`, `INSTALLATION_ID`, and
`PRIAVTE_KEY_FILE_PATH`.

The test creates a one-off `kind` cluster, deploys `cert-manager` and `actions-runner-controller`,
creates a `RunnerDeployment` custom resource for a public Git repository to confirm that the
controller is able to bring up a runner pod with the actions runner registration token installed.

If you prefer to test in a non-kind cluster, you can instead run:

```shell script
KUBECONFIG=path/to/kubeconfig \
NAME=$DOCKER_USER/actions-runner-controller \
  GITHUB_TOKEN=*** \
  APP_ID=*** \
  PRIVATE_KEY_FILE_PATH=path/to/pem/file \
  INSTALLATION_ID=*** \
  ACCEPTANCE_TEST_SECRET_TYPE=token \
  make docker-build docker-push \
       acceptance/setup acceptance/tests
```
# Alternatives

The following is a list of alternative solutions that may better fit you depending on your use-case:

- <https://github.com/evryfs/github-actions-runner-operator/>
- <https://github.com/philips-labs/terraform-aws-github-runner/>

Although the situation can change over time, as of writing this sentence, the benefits of using `actions-runner-controller` over the alternatives are:

- `actions-runner-controller` has the ability to autoscale runners based on number of pending/progressing jobs (#99)
- `actions-runner-controller` is able to gracefully stop runners (#103)
- `actions-runner-controller` has ARM support
- `actions-runner-controller` has GitHub Enterprise support (see [GitHub Enterprise support](#github-enterprise-support) section for caveats)
