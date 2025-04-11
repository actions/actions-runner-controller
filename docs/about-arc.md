# About ARC

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Introduction
This document provides a high-level overview of Actions Runner Controller (ARC). ARC enables running Github Actions Runners on Kubernetes (K8s) clusters.

With this overview, you can get a foundation of basic scenarios and be capable of reviewing other advanced topics.

## GitHub Actions
[GitHub Actions](https://github.com/features/actions) is a continuous integration and continuous delivery (CI/CD) platform to automate your build, test, and deployment pipeline. 

You can create workflows that build and test every pull request to your repository, or deploy merged pull requests to production. Your workflow contains one or more jobs which can run in sequential order or in parallel. Each job will run inside its own runner and has one or more steps that either run a script that you define or run an action, which is a reusable extension that can simplify your workflow. To learn more about Actions - see "[Learn Github Actions](https://docs.github.com/en/actions/learn-github-actions)".

## Runners
Runners execute the job that is assigned to them by Github Actions workflow. There are two types of Runners:

- [Github-hosted runners](https://docs.github.com/en/actions/using-github-hosted-runners/about-github-hosted-runners) - GitHub provides Linux, Windows, and macOS virtual machines to run your workflows. These virtual machines are hosted in the cloud by Github.
- [Self-hosted runners](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/about-self-hosted-runners) - you can host your own self-hosted runners in your own data center or cloud infrastructure. ARC deploys self-hosted runners.

## Self hosted runners
Self-hosted runners offer more control of hardware, operating system, and software tools than GitHub-hosted runners. With self-hosted runners, you can create custom hardware configurations that meet your needs with processing power or memory to run larger jobs, install software available on your local network, and choose an operating system not offered by GitHub-hosted runners.

### Types of Self hosted runners
Self-hosted runners can be physical, virtual, in a container, on-premises, or in a cloud.
- Traditional Deployment is having a physical machine, with OS and apps on it. The runner runs on this machine and executes any jobs. It comes with the cost of owning and operating the hardware 24/7 even if it isn't in use that entire time. 
- Virtualized deployments are simpler to manage. Each runner runs on a virtual machine (VM) that runs on a host. There could be multiple such VMs running on the same host. VMs are complete OS’s and might take time to bring up everytime a clean environment is needed to run workflows.
- Containerized deployments are similar to VMs, but instead of bringing up entire VM’s, a container gets deployed. Kubernetes (K8s) provides a scalable and reproducible environment for containerized workloads. They are lightweight, loosely coupled, highly efficient and can be managed centrally. There are advantages to using Kubernetes (outlined "[here](https://kubernetes.io/docs/concepts/overview/what-is-kubernetes/)."), but it is more complicated and less widely-understood than the other options. A managed provider makes this much simpler to run at scale.

*Actions Runner Controller(ARC) makes it simpler to run self hosted runners on K8s managed containers.*

## Actions Runner Controller (ARC)
ARC  is a K8s controller to create self-hosted runners on your K8s cluster. With few commands, you can set up self hosted runners that can scale up and down based on demand. And since these could be ephemeral and based on containers, new instances of the runner can be brought up rapidly and cleanly.

### Deploying ARC
We have a quick start guide that demonstrates how to easily deploy ARC into your K8s environment. For more details, see "[QuickStart Guide](/README.md#getting-started)."


## ARC components
ARC basically consists of a set of custom resources. An ARC deployment is applying these custom resources onto a K8s cluster. Once applied, it creates a set of Pods, with the Github Actions runner running within them. Github is now able to treat these Pods as self hosted runners and allocate jobs to them.

### Custom resources 
ARC consists of several custom resource definitions (Runner, Runner Set, Runner Deployment, Runner Replica Set and Horizontal Runner AutoScaler). For more information on CRDs, refer "[Kubernetes Custom Resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)."

The helm command (in the QuickStart guide) installs the custom resources into the actions-runner-system namespace.
```console
helm install -f custom-values.yaml --wait --namespace actions-runner-system \
  --create-namespace actions-runner-controller \
  actions-runner-controller/actions-runner-controller
 ```

### Runner deployment
Once the custom resources are installed, another command deploys ARC into your K8s cluster.

![actions-runner-controller architecture](https://user-images.githubusercontent.com/53718047/183928236-ddf72c15-1d11-4304-ad6f-0a0ff251ca55.jpg)



The `Deployment and Configure ARC` section in the `Quick Start guide` lists the steps to deploy ARC using a `runnerdeployment.yaml` file. Here, we will explain the details
For more details, see "[QuickStart Guide](/README.md#getting-started)."

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  replicas: 1
  template:
    spec:
      repository: mumoshu/actions-runner-controller-ci
```

- `kind: RunnerDeployment`: indicates it's a kind of custom resource RunnerDeployment.
- `replicas: 1` : will deploy one replica. Multiple replicas can also be deployed ( more on that later).
- `repository: mumoshu/actions-runner-controller-ci` : is the repository to link to when the pod comes up with the Actions runner (Note, this can be configured to link at the Enterprise or Organization level also).

When this configuration is applied with `kubectl apply -f runnerdeployment.yaml` , ARC creates one pod `example-runnerdeploy-[**]` with 2 containers `runner` and `docker`.
`runner` container has the github runner component installed, `docker` container has docker installed.


### The Runner container image
The GitHub hosted runners include a large amount of pre-installed software packages. For complete list, see "[Runner images](https://github.com/actions/virtual-environments/tree/main/images/linux)."

ARC maintains a few runner images with `latest` aligning with GitHub's Ubuntu version. These images do not contain all of the software installed on the GitHub runners. They contain subset of packages from the GitHub runners: Basic CLI packages, git, docker and build-essentials. To install additional software, it is recommended to use the corresponding setup actions. For instance, `actions/setup-java` for Java or `actions/setup-node` for Node.

## Executing workflows
Now, all the setup and configuration is done. A workflow can be created in the same repository that could target the self hosted runner created from ARC. The workflow needs to have `runs-on: self-hosted` so it can target the self host pool. For more information on targeting workflows to run on self hosted runners, see "[Using Self-hosted runners](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/using-self-hosted-runners-in-a-workflow)."

## Scaling runners - statically with replicas count
With a small tweak to the replicas count (for eg - `replicas: 2`) in the `runnerdeployment.yaml` file, more runners can be created. Depending on the count of replicas, those many sets of pods would be created. As before, Each pod contains the two containers.


## Scaling runners - dynamically with Pull Driven Scaling
ARC also allows for scaling the runners dynamically. There are two mechanisms for dynamically scaling - (1) Webhook driven scaling and (2) Pull Driven scaling, This document describes the Pull Driven scaling model.

![actions-runner-controller architecture_2](https://user-images.githubusercontent.com/53718047/183928429-7000329d-38eb-4054-9879-41ae44e1ff85.jpg)



You can enable scaling with 3 steps
1) Enable `HorizontalRunnerAutoscaler` - Create a `deployment.yaml` file of type `HorizontalRunnerAutoscaler`. The schema for this file is defined below.
2) Scaling parameters - `minReplicas` and `maxReplicas` indicate the min and max number of replicas to scale to.
3) Scaling metrics - ARC currently supports `PercentageRunnersBusy` as a metric type. The `PercentageRunnersBusy` will poll GitHub for the number of runners in the `busy` state in the RunnerDeployment's namespace, it will then scale depending on how you have configured the scale factors.

### Pull Driven Scaling Schema
```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name: example-runner-deployment-autoscaler
spec:
  scaleTargetRef:
    # Your RunnerDeployment Here
    name: example-runnerdeploy
    kind: RunnerDeployment
  minReplicas: 1
  maxReplicas: 5
  metrics:
  - type: PercentageRunnersBusy
    scaleUpThreshold: '0.75'
    scaleDownThreshold: '0.25'
    scaleUpFactor: '2'
    scaleDownFactor: '0.5'
  ```

For more details - please see "[Pull Driven Scaling](automatically-scaling-runners.md#pull-driven-scaling)."

*The period between polls is defined by the controller's `--sync-period` flag. If this flag isn't provided then the controller defaults to a sync period of `1m`, this can be configured in seconds or minutes.*

## Other Configurations
ARC supports several different advanced configuration. 
- support for alternate runners : Setting up runner pods with Docker-In-Docker configuration.
- managing runner groups : Managing a set of running with runner groups thus making it easy to manage different groups within enterprise
- Webhook driven scaling.

Please refer to the documentation in this repo for further details.

## GitHub Enterprise Support

The solution supports both GHEC (GitHub Enterprise Cloud) and GHES (GitHub Enterprise Server) editions as well as regular GitHub. Both PAT (personal access token) and GitHub App authentication works for installations that will be deploying either repository level and / or organization level runners. If you need to deploy enterprise level runners then you are restricted to PAT based authentication as GitHub doesn't support GitHub App based authentication for enterprise runners currently.

If you are deploying this solution into a GHES environment then you will need to be running version >= [3.6.0](https://docs.github.com/en/enterprise-server@3.6/admin/release-notes).

When deploying the solution for a GHES environment you need to provide an additional environment variable as part of the controller deployment:

```shell
kubectl set env deploy controller-manager -c manager GITHUB_ENTERPRISE_URL=<GHEC/S URL> --namespace actions-runner-system
```

**_Note: The repository maintainers do not have an enterprise environment (cloud or server). Support for the enterprise specific feature set is community driven and on a best effort basis. PRs from the community are welcome to add features and maintain support._**

## Software Installed in the Runner Image

**Cloud Tooling**<br />
The project supports being deployed on the various cloud Kubernetes platforms (e.g. EKS), it does not however aim to go beyond that. No cloud specific tooling is bundled in the base runner, this is an active decision to keep the overhead of maintaining the solution manageable.

**Bundled Software**<br />
The GitHub hosted runners include a large amount of pre-installed software packages. GitHub maintains a list in README files at <https://github.com/actions/runner-images/blob/main/images/ubuntu/>.

This solution maintains a few Ubuntu based runner images, these images do not contain all of the software installed on the GitHub runners. The images contain the following subset of packages from the GitHub runners:

- Some basic CLI packages
- Git
- Git LFS
- Docker
- Docker Compose

The virtual environments from GitHub contain a lot more software packages (different versions of Java, Node.js, Golang, .NET, etc) which are not provided in the runner image. Most of these have dedicated setup actions which allow the tools to be installed on-demand in a workflow, for example: `actions/setup-java` or `actions/setup-node`

If there is a need to include packages in the runner image for which there is no setup action, then this can be achieved by building a custom container image for the runner. The easiest way is to start with the `summerwind/actions-runner` image and then install the extra dependencies directly in the docker image:

```shell
FROM summerwind/actions-runner:latest

RUN sudo apt-get update -y \
  && sudo apt-get install $YOUR_PACKAGES \
  && sudo rm -rf /var/lib/apt/lists/*
```

You can then configure the runner to use a custom docker image by configuring the `image` field of a `RunnerDeployment` or `RunnerSet`:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: custom-runner
spec:
  repository: actions/actions-runner-controller
  image: YOUR_CUSTOM_RUNNER_IMAGE
```
