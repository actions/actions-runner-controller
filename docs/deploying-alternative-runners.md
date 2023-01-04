# Deploying alternative runners

## Alternative Runners

ARC also offers a few alternative runner options

### Runner with DinD

When using the default runner, the runner pod starts up 2 containers: runner and DinD (Docker-in-Docker). ARC maintains an alternative all in one runner image with docker running in the same container as the runner. This may be prefered from a resource or complexity perspective or to be compliant with a `LimitRange` namespace configuration.

```yaml
# dindrunnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-dindrunnerdeploy
spec:
  replicas: 1
  template:
    spec:
      image: summerwind/actions-runner-dind
      dockerdWithinRunnerContainer: true
      repository: mumoshu/actions-runner-controller-ci
      env: []
```

### Runner with rootless DinD

When using the DinD runner, it assumes that the main runner is rootful, which can be problematic in a regulated or more security-conscious environment, such as co-tenanting across enterprise projects.  The `actions-runner-dind-rootless` image runs rootless Docker inside the container as `runner` user.  Note that this user does not have sudo access, so anything requiring admin privileges must be built into the runner's base image (like running `apt` to install additional software).

### Runner with K8s Jobs

When using the default runner, jobs that use a container will run in docker. This necessitates privileged mode, either on the runner pod or the sidecar container

By setting the container mode, you can instead invoke these jobs using a [kubernetes implementation](https://github.com/actions/runner-container-hooks/tree/main/packages/k8s) while not executing in privileged mode.

The runner will dynamically spin up pods and k8s jobs in the runner's namespace to run the workflow, so a `workVolumeClaimTemplate` is required for the runner's working directory, and a service account with the [appropriate permissions.](https://github.com/actions/runner-container-hooks/tree/main/packages/k8s#pre-requisites)

There are some [limitations](https://github.com/actions/runner-container-hooks/tree/main/packages/k8s#limitations) to this approach, mainly [job containers](https://docs.github.com/en/actions/using-jobs/running-jobs-in-a-container) are required on all workflows.

```yaml
# runner.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: Runner
metadata:
  name: example-runner
spec:
  repository: example/myrepo
  containerMode: kubernetes
  serviceAccountName: my-service-account
  workVolumeClaimTemplate:
    storageClassName: "my-dynamic-storage-class"
    accessModes:
    - ReadWriteOnce
    resources:
      requests:
        storage: 10Gi
  env: []
```


  