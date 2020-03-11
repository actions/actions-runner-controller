# actions-runner-controller

This controller operates self-hosted runners for GitHub Actions on your Kubernetes cluster.

## Motivation

[GitHub Actions](https://github.com/features/actions) is very useful as a tool for automating development. GitHub Actions job is run in the cloud by default, but you may want to run your jobs in your environment. [Self-hosted runner](https://github.com/actions/runner) can be used for such use cases, but requires the provision of a virtual machine instance and configuration. If you already have a Kubernetes cluster, you'll want to run the self-hosted runner on top of it.

*actions-runner-controller* makes that possible. Just create a *Runner* resource on your Kubernetes, and it will run and operate the self-hosted runner of the specified repository. Combined with Kubernetes RBAC, you can also build simple Self-hosted runners as a Service.

## Installation

First, install *actions-runner-controller* with a manifest file. This will create a *actions-runner-system* namespace in your Kubernetes and deploy the required resources.

```
$ kubectl apply -f https://github.com/summerwind/actions-runner-controller/releases/latest/download/actions-runner-controller.yaml
```

Set your access token of GitHub to the secret. `${GITHUB_TOKEN}` is the value you must replace with your access token. This token is used to register Self-hosted runner by *actions-runner-controller*.

```
$ kubectl create secret generic controller-manager --from-literal=github_token=${GITHUB_TOKEN} -n actions-runner-system
```

## Usage

There's generally two ways to use this controller:

- Manage runners one by one with `Runner`
- Manage a set of runners with `RunnerDeployment`

### Runners

To launch a single Self-hosted runner, you need to create a manifest file includes *Runner* resource as follows. This example launches a self-hosted runner with name *example-runner* for the *summerwind/actions-runner-controller* repository.

```
# runner.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: example-runner
spec:
  repository: summerwind/actions-runner-controller
```

Apply the created manifest file to your Kubernetes.

```
$ kubectl apply -f runner.yaml
runner.actions.summerwind.dev/example-runner created
```

You can see that the Runner resource has been created.

```
$ kubectl get runners
NAME             REPOSITORY                             STATUS
example-runner   summerwind/actions-runner-controller   Running
```

You can also see that the runner pod has been running.

```
$ kubectl get pods
NAME           READY   STATUS    RESTARTS   AGE
example-runner 2/2     Running   0          1m
```

The runner you created has been registerd to your repository.

<img width="756" alt="Actions tab in your repository settings" src="https://user-images.githubusercontent.com/230145/73618667-8cbf9700-466c-11ea-80b6-c67e6d3f70e7.png">

Now your can use your self-hosted runner. See the [official documentation](https://help.github.com/en/actions/automating-your-workflow-with-github-actions/using-self-hosted-runners-in-a-workflow) on how to run a job with it.

### RunnerDeployments

There's also `RunnerReplicaSet` and `RunnerDeployment` that corresponds to `ReplicaSet` and `Deployment` but for `Runner`.

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
```

Apply the manifest file to your cluster:

```
$ kubectl apply -f runner.yaml
runnerdeployment.actions.summerwind.dev/example-runnerdeploy created
```

You can see that 2 runners has been created as specified by `replicas: 2`:

```
$ kubectl get runners
NAME             REPOSITORY                             STATUS
NAME                             REPOSITORY                             STATUS
example-runnerdeploy2475h595fr   mumoshu/actions-runner-controller-ci   Running
example-runnerdeploy2475ht2qbr   mumoshu/actions-runner-controller-ci   Running
```
