## Introduction

GitHub Actions can be run in GitHub-hosted cloud or self hosted environments. Self-hosted runners offer more control of hardware, operating system, and software tools than GitHub-hosted runners provide.

With just a few steps, you can set up your kubernetes (K8s) cluster to be a self-hosted environment.
In this guide, we will setup prerequistes, deploy Actions Runner controller (ARC) and then target that cluster to run GitHub Action workflows.

<p align="center">
  <img src="https://user-images.githubusercontent.com/53718047/181159115-dbf41416-89a7-408c-b575-bb0d059a1a36.png" />
</p>



## Setup your K8s cluster

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
   - Login to GitHub account and Navigate to "[Create new Token](https://github.com/settings/tokens/new)."
   - Select  **repo**.
   - Click **Generate Token** and then copy the token locally ( we’ll need it later).




## Deploy and Configure ARC
1️⃣ Deploy  and configure ARC on your K8s cluster. You may use Helm or Kubectl.


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
<sub> *note:- Replace REPLACE_YOUR_TOKEN_HERE with your PAT that was generated in Step 1 </sub>
</details>

<details><summary>Kubectl deployment</summary>

##### Deploy ARC
```shell
kubectl apply -f \
https://github.com/actions-runner-controller/actions-runner-controller/\
releases/download/v0.22.0/actions-runner-controller.yaml
```
<sub> *note:- Replace "v0.22.0" with the version you wish to deploy </sub>
 

##### Configure Personal Access Token
```shell
kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_token=REPLACE_YOUR_TOKEN_HERE
````
<sub> *note:- Replace REPLACE_YOUR_TOKEN_HERE with your PAT that was generated in Step 1. </sub>
  
  </details>

2️⃣ Create the GitHub self hosted runners and configure to run against your repository.

Create a `runnerdeployment.yaml` file containing..

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
<sub> *note:- Replace mumoshu/actions-runner-controller-ci with the full path to your github repository. </sub>

Apply this file to your K8s cluster.
```shell
kubectl apply -f runnerdeployment.yaml
````
 

>
>🎉 We are done - now we should have self hosted runners running in K8s configured to your repository.  🎉
> 
> Up Next - lets verify and execute some workflows.
 
## Verify and execute workflows
:one: Verify your setup is successful with.. 
```shell
$ kubectl get runners
NAME                             REPOSITORY                             STATUS
example-runnerdeploy2475h595fr   mumoshu/actions-runner-controller-ci   Running

$ kubectl get pods
NAME                           READY   STATUS    RESTARTS   AGE
example-runnerdeploy2475ht2qbr 2/2     Running   0          1m
````
Also, this runner has been registered directly to the specified repository, you can see it in repository settings. For more information, see "[settings](https://docs.github.com/en/actions/hosting-your-own-runners/monitoring-and-troubleshooting-self-hosted-runners#checking-the-status-of-a-self-hosted-runner)."

:two: You are ready to execute workflows against this self hosted runner. 
GitHub documentation lists the steps to target Actions against self hosted runners. For more information, see "[Using self-hosted runners in a workflow - GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/using-self-hosted-runners-in-a-workflow#using-self-hosted-runners-in-a-workflow)."

There's also has a quick start guide to get started on Actions, For more information, see "[Quick start Guide to GitHub Actions](https://docs.github.com/en/actions/quickstart)."

## Next steps
ARC provides several interesting features and capabilities. For more information, see "[Readme](https://github.com/actions-runner-controller/actions-runner-controller/blob/master/README.md)."



 
