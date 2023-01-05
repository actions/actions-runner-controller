# Adding ARC runners to a repository, organization, or enterprise

## Usage

[GitHub self-hosted runners can be deployed at various levels in a management hierarchy](https://docs.github.com/en/actions/hosting-your-own-runners/about-self-hosted-runners#about-self-hosted-runners):
- The repository level
- The organization level
- The enterprise level

Runners can be deployed as 1 of 2 abstractions:

- A `RunnerDeployment` (similar to k8s's `Deployments`, based on `Pods`)
- A `RunnerSet` (based on k8s's `StatefulSets`)

We go into details about the differences between the 2 later, initially lets look at how to deploy a basic `RunnerDeployment` at the 3 possible management hierarchies.

## Adding runners to a repository

To launch a single self-hosted runner, you need to create a manifest file that includes a `RunnerDeployment` resource as follows. This example launches a self-hosted runner with name *example-runnerdeploy* for the *actions/actions-runner-controller* repository.

```yaml
# runnerdeployment.yaml
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

Apply the created manifest file to your Kubernetes.

```shell
$ kubectl apply -f runnerdeployment.yaml
runnerdeployment.actions.summerwind.dev/example-runnerdeploy created
```

You can see that 1 runner and its underlying pod has been created as specified by `replicas: 1` attribute:

```shell
$ kubectl get runners
NAME                             REPOSITORY                             STATUS
example-runnerdeploy2475h595fr   mumoshu/actions-runner-controller-ci   Running

$ kubectl get pods
NAME                           READY   STATUS    RESTARTS   AGE
example-runnerdeploy2475ht2qbr 2/2     Running   0          1m
```

The runner you created has been registered directly to the defined repository, you should be able to see it in the settings of the repository.

Now you can use your self-hosted runner. See the [official documentation](https://help.github.com/en/actions/automating-your-workflow-with-github-actions/using-self-hosted-runners-in-a-workflow) on how to run a job with it.

## Adding runners to an organization

To add the runner to an organization, you only need to replace the `repository` field with `organization`, so the runner will register itself to the organization.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  replicas: 1
  template:
    spec:
      organization: your-organization-name
```

Now you can see the runner on the organization level (if you have organization owner permissions).

## Adding runners to an enterprise

To add the runner to an enterprise, you only need to replace the `repository` field with `enterprise`, so the runner will register itself to the enterprise.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  replicas: 1
  template:
    spec:
      enterprise: your-enterprise-name
```

Now you can see the runner on the enterprise level (if you have enterprise access permissions).
