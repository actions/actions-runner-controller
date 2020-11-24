# actions-runner-controller

This controller operates self-hosted runners for GitHub Actions on your Kubernetes cluster.

## Motivation

[GitHub Actions](https://github.com/features/actions) is a very useful tool for automating development. GitHub Actions jobs are run in the cloud by default, but you may want to run your jobs in your environment. [Self-hosted runner](https://github.com/actions/runner) can be used for such use cases, but requires the provisioning and configuration of a virtual machine instance. Instead if you already have a Kubernetes cluster, it makes more sense to run the self-hosted runner on top of it.

**actions-runner-controller** makes that possible. Just create a *Runner* resource on your Kubernetes, and it will run and operate the self-hosted runner for the specified repository. Combined with Kubernetes RBAC, you can also build simple Self-hosted runners as a Service.

## Installation

actions-runner-controller uses [cert-manager](https://cert-manager.io/docs/installation/kubernetes/) for certificate management of Admission Webhook. Make sure you have already installed cert-manager before you install. The installation instructions for cert-manager can be found below.

- [Installing cert-manager on Kubernetes](https://cert-manager.io/docs/installation/kubernetes/)

Install the custom resource and actions-runner-controller itself. This will create actions-runner-system namespace in your Kubernetes and deploy the required resources.

```
kubectl apply -f https://github.com/summerwind/actions-runner-controller/releases/latest/download/actions-runner-controller.yaml
```

### Github Enterprise support

If you use either Github Enterprise Cloud or Server (and have recent enought version supporting Actions), you can use **actions-runner-controller**  with those, too. Authentication works same way as with public Github (repo and organization level).

```shell
kubectl set env deploy controller-manager -c manager GITHUB_ENTERPRISE_URL=<GHEC/S URL>
```

[Enterprise level](https://docs.github.com/en/enterprise-server@2.22/actions/hosting-your-own-runners/adding-self-hosted-runners#adding-a-self-hosted-runner-to-an-enterprise) runners are not working yet as there's no API definition for those.

## Setting up authentication with GitHub API

There are two ways for actions-runner-controller to authenticate with the GitHub API:

1. Using GitHub App.
2. Using Personal Access Token.

Regardless of which authentication method you use, the same permissions are required, those permissions are:
- Repository: Administration (read/write)
- Repository: Actions (read)
- Organization: Self-hosted runners (read/write)


**NOTE: It is extremely important to only follow one of the sections below and not both.**

### Using GitHub App

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

### Using Personal Access Token

From an account that has `admin` privileges for the repository, create a [personal access token](https://github.com/settings/tokens) with `repo` scope. This token is used to register a self-hosted runner by *actions-runner-controller*.

Self-hosted runners in GitHub can either be connected to a single repository, or to a GitHub organization (so they are available to all repositories in the organization). This token is used to register a self-hosted runner by *actions-runner-controller*.

For adding a runner to a repository, the token should have `repo` scope. If the runner should be added to an organization, the token should have `admin:org` scope. Note that to use a Personal Access Token, you must issue the token with an account that has `admin` privileges (on the repository and/or the organization).

Open the Create Token page from the following link, grant the `repo` and/or `admin:org` scope, and press the "Generate Token" button at the bottom of the page to create the token.

- [Create personal access token](https://github.com/settings/tokens/new)

Register the created token (`GITHUB_TOKEN`) as a Kubernetes secret.

```shell
kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_token=${GITHUB_TOKEN}
```

## Usage

There are two ways to use this controller:

- Manage runners one by one with `Runner`.
- Manage a set of runners with `RunnerDeployment`.

### Repository runners

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

`RunnerDeployment` can scale the number of runners between `minReplicas` and `maxReplicas` fields, depending on pending workflow runs.

In the below example, `actions-runner` checks for pending workflow runs for each sync period, and scale to e.g. 3 if there're 3 pending jobs at sync time.

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

The scale out performance is controlled via the manager containers startup `--sync-period` argument. The default value is 10 minutes to prevent unconfigured deployments rate limiting themselves from the GitHub API. The period can be customised in the `config/default/manager_auth_proxy_patch.yaml` patch for those that are building the solution via the kustomize setup.

Additionally, the autoscaling feature has an anti-flapping option that prevents periodic loop of scaling up and down.
By default, it doesn't scale down until the grace period of 10 minutes passes after a scale up. The grace period can be configured by setting `scaleDownDelaySecondsAfterScaleUp`:

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
  scaleDownDelaySecondsAfterScaleOut: 60
  metrics:
  - type: TotalNumberOfQueuedAndInProgressWorkflowRuns
    repositoryNames:
    - summerwind/actions-runner-controller
```

## Runner with DinD

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

## Additional tweaks

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
      # If set to false, there are no privileged container and you cannot use docker. 
      dockerEnabled: false
      # If set to true, runner pod container only 1 container that's expected to be able to run docker, too.
      # image summerwind/actions-runner-dind or custom one should be used with true -value
      dockerdWithinRunnerContainer: false
      # Valid if dockerdWithinRunnerContainer is not true
      dockerdContainerResources:
        limits:
          cpu: "4.0"
          memory: "8Gi"
        requests:
          cpu: "2.0"
          memory: "4Gi"
      sidecarContainers:
        - name: mysql
          image: mysql:5.7
          env:
            - name: MYSQL_ROOT_PASSWORD
              value: abcd1234
          securityContext:
            runAsUser: 0
      # if workDir is not specified, the default working directory is /runner/_work
      # this setting allows you to customize the working directory location
      # for example, the below setting is the same as on the ubuntu-18.04 image
      workDir: /home/runner/work
```

## Runner labels

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

## Runner Groups

Runner groups can be used to limit which repositories are able to use the GitHub Runner at an Organisation level.

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

## Software installed in the runner image

The GitHub hosted runners include a large amount of pre-installed software packages. For Ubuntu 18.04, this list can be found at <https://github.com/actions/virtual-environments/blob/master/images/linux/Ubuntu1804-README.md>

The container image is based on Ubuntu 18.04, but it does not contain all of the software installed on the GitHub runners. It contains the following subset of packages from the GitHub runners:

- Basic CLI packages
- git (2.26)
- docker
- build-essentials

The virtual environments from GitHub contain a lot more software packages (different versions of Java, Node.js, Golang, .NET, etc) which are not provided in the runner image. Most of these have dedicated setup actions which allow the tools to be installed on-demand in a workflow, for example: `actions/setup-java` or `actions/setup-node`

If there is a need to include packages in the runner image for which there is no setup action, then this can be achieved by building a custom container image for the runner. The easiest way is to start with the `summerwind/actions-runner` image and installing the extra dependencies directly in the docker image:

```shell
FROM summerwind/actions-runner:v2.169.1

RUN sudo apt update -y \
  && apt install YOUR_PACKAGE
  && rm -rf /var/lib/apt/lists/*
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

## Common Errors

### invalid header field value

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
NAME=$DOCKER_USER/actions-runner-controller VERSION=dev \
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

# Alternatives

The following is a list of alternative solutions that may better fit you depending on your use-case:

- <https://github.com/evryfs/github-actions-runner-operator/>

Although the situation can change over time, as of writing this sentence, the benefits of using `actions-runner-controller` over the alternatives are:

- `actions-runner-controller` has the ability to autoscale runners based on number of pending/progressing jobs (#99)
- `actions-runner-controller` is able to gracefully stop runners (#103)
- `actions-runner-controller` has ARM support
