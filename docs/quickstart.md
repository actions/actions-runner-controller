# Actions Runner Controller Quickstart

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

GitHub Actions automates the deployment of code to different environments, including production. The environments contain the `GitHub Runner` software which executes the automation. `GitHub Runner` can be run in GitHub-hosted cloud or self-hosted environments. Self-hosted environments offer more control of hardware, operating system, and software tools. They can be run on physical machines, virtual machines, or in a container. Containerized environments are lightweight, loosely coupled, highly efficient and can be managed centrally. However, they are not straightforward to use.

`Actions Runner Controller (ARC)` makes it simpler to run self hosted environments on Kubernetes(K8s) cluster.

With ARC you can :

- **Deploy self hosted runners on Kubernetes cluster** with a simple set of commands.
- **Auto scale runners** based on demand.
- **Setup across GitHub editions** including GitHub Enterprise editions and GitHub Enterprise Cloud.

## Overview

For an overview of ARC, please refer to "[About ARC](https://github.com/actions/actions-runner-controller/blob/master/docs/about-arc.md)."

## Getting Started

ARC can be setup with just a few steps.

In this section we will setup prerequisites, deploy ARC into a K8s cluster, and then run GitHub Action workflows on that cluster.

### Prerequisites

<details><summary><sub>Create a K8s cluster, if not available.</sub></summary>
   <sub>
If you don't have a K8s cluster, you can install a local environment using minikube. For more information, see <a href="https://minikube.sigs.k8s.io/docs/start/">"Installing minikube."</a>
   </sub>
</details>

:one: Install cert-manager in your cluster. For more information, see "[cert-manager](https://cert-manager.io/docs/installation/)."

```shell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.8.2/cert-manager.yaml
```

<sub> *note:- This command uses v1.8.2. Please replace with a later version, if available.</sub>

>You may also install cert-manager using Helm. For instructions, see "[Installing with Helm](https://cert-manager.io/docs/installation/helm/#installing-with-helm)."

:two: Next, Generate a Personal Access Token (PAT) for ARC to authenticate with GitHub.

- Login to your GitHub account and Navigate to "[Create new Token](https://github.com/settings/tokens/new)."
- Select  **repo**.
- Click **Generate Token** and then copy the token locally ( we’ll need it later).

### Deploy and Configure ARC

1️⃣ Deploy and configure ARC on your K8s cluster. You may use Helm or Kubectl.

<details><summary>Helm deployment</summary>

##### Add repository

```shell
helm repo add actions-runner-controller https://actions-runner-controller.github.io/actions-runner-controller
```

##### Install Helm chart

```shell
helm upgrade --install --namespace actions-runner-system --create-namespace\
  --set=authSecret.create=true\
  --set=authSecret.github_token="REPLACE_YOUR_TOKEN_HERE"\
  --wait actions-runner-controller actions-runner-controller/actions-runner-controller
```

<sub> *note:- Replace REPLACE_YOUR_TOKEN_HERE with your PAT that was generated previously. </sub>
</details>

<details><summary>Kubectl deployment</summary>

##### Deploy ARC

```shell
kubectl create -f \
https://github.com/actions/actions-runner-controller/\
releases/download/v0.22.0/actions-runner-controller.yaml
```

<sub> *note:- Replace "v0.22.0" with the version you wish to deploy </sub>

##### Configure Personal Access Token

```shell
kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_token=REPLACE_YOUR_TOKEN_HERE
````

<sub> *note:- Replace REPLACE_YOUR_TOKEN_HERE with your PAT that was generated previously.</sub>

  </details>

2️⃣ Create the GitHub self hosted runners and configure to run against your repository.

Create a `runnerdeployment.yaml` file and copy the following YAML contents into it:

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
````
<sub> *note:- Replace "mumoshu/actions-runner-controller-ci" with the name of the GitHub repository the runner will be associated with. </sub>

Apply this file to your K8s cluster.
```shell
kubectl apply -f runnerdeployment.yaml
````

*🎉 We are done - now we should have self hosted runners running in K8s configured to your repository.  🎉*

Next - lets verify our setup and execute some workflows.

### Verify and Execute Workflows

:one: Verify that your setup is successful:
```shell

$ kubectl get runners
NAME                             REPOSITORY                             STATUS
example-runnerdeploy2475h595fr   mumoshu/actions-runner-controller-ci   Running

$ kubectl get pods
NAME                           READY   STATUS    RESTARTS   AGE
example-runnerdeploy2475ht2qbr 2/2     Running   0          1m
````

Also, this runner has been registered directly to the specified repository, you can see it in repository settings. For more information, see "[Checking the status of a self-hosted runner - GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/monitoring-and-troubleshooting-self-hosted-runners#checking-the-status-of-a-self-hosted-runner)."

:two: You are ready to execute workflows against this self-hosted runner. For more information, see "[Using self-hosted runners in a workflow - GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/using-self-hosted-runners-in-a-workflow#using-self-hosted-runners-in-a-workflow)."

There is also a quick start guide to get started on Actions, For more information, please refer to "[Quick start Guide to GitHub Actions](https://docs.github.com/en/actions/quickstart)."

## Learn more

For more detailed documentation, please refer to "[Actions Runner Controller Documentation](https://github.com/actions/actions-runner-controller/blob/master/README.md#documentation)."

## Contributing

We welcome contributions from the community. For more details on contributing to the project (including requirements), please refer to "[Getting Started with Contributing](https://github.com/actions/actions-runner-controller/blob/master/CONTRIBUTING.md)."

## Troubleshooting

We are very happy to help you with any issues you have. Please refer to the "[Troubleshooting](https://github.com/actions/actions-runner-controller/blob/master/TROUBLESHOOTING.md)" section for common issues.
