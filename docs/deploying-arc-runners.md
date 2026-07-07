# Deploying ARC runners

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Deploying runners with RunnerDeployments

In our previous examples we were deploying a single runner via the `RunnerDeployment` kind, the amount of runners deployed can be statically set via the `replicas:` field, we can increase this value to deploy additional sets of runners instead:

```yaml
# runnerdeployment.yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  # This will deploy 2 runners now
  replicas: 2
  template:
    spec:
      repository: mumoshu/actions-runner-controller-ci
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

## Deploying runners with RunnerSets

> This feature requires controller version => [v0.20.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.20.0)

We can also deploy sets of RunnerSets the same way, a basic `RunnerSet` would look like this:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerSet
metadata:
  name: example
spec:
  replicas: 1
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

As it is based on `StatefulSet`, `selector` and `template.metadata.labels` it needs to be defined and have the exact same set of labels. `serviceName` must be set to some non-empty string as it is also required by `StatefulSet`.

Runner-related fields like `ephemeral`, `repository`, `organization`, `enterprise`, and so on should be written directly under `spec`.

Fields like `volumeClaimTemplates` that originates from `StatefulSet` should also be written directly under `spec`.

Pod-related fields like security contexts and volumes are written under `spec.template.spec` like `StatefulSet`.

Similarly, container-related fields like resource requests and limits, container image names and tags, security context, and so on are written under `spec.template.spec.containers`. There are two reserved container names, `runner` and `docker`. The former is for the container that runs [actions runner](https://github.com/actions/runner) and the latter is for the container that runs a `dockerd`.

For a more complex example, see the below:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerSet
metadata:
  name: example
spec:
  replicas: 1
  repository: mumoshu/actions-runner-controller-ci
  dockerdWithinRunnerContainer: true
  template:
    spec:
      securityContext:
        # All level/role/type/user values will vary based on your SELinux policies.
        # See https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux_atomic_host/7/html/container_security_guide/docker_selinux_security_policy for information about SELinux with containers
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
        # This is an advanced configuration. Don't touch it unless you know what you're doing.
        securityContext:
          # Usually, the runner container's privileged field is derived from dockerdWithinRunnerContainer.
          # But in the case where you need to run privileged job steps even if you don't use docker/don't need dockerd within the runner container,
          # just specified `privileged: true` like this.
          # See https://github.com/actions/actions-runner-controller/issues/1282
          # Do note that specifying `privileged: false` while using dind is very likely to fail, even if you use some vm-based container runtimes
          # like firecracker and kata. Basically they run containers within dedicated micro vms and so
          # it's more like you can use `privileged: true` safer with those runtimes.
          #
          # privileged: true
      - name: docker
        resources:
          limits:
            cpu: "4.0"
            memory: "8Gi"
          requests:
            cpu: "2.0"
            memory: "4Gi"
```

You can also read the design and usage documentation written in the original pull request that introduced `RunnerSet` for more information [#629](https://github.com/actions/actions-runner-controller/pull/629).

Under the hood, `RunnerSet` relies on Kubernetes's `StatefulSet` and Mutating Webhook. A `statefulset` is used to create a number of pods that has stable names and dynamically provisioned persistent volumes, so that each `statefulset-managed` pod gets the same persistent volume even after restarting. A mutating webhook is used to dynamically inject a runner's "registration token" which is used to call GitHub's "Create Runner" API.

## Using persistent runners

Every runner managed by ARC is "ephemeral" by default. The life of an ephemeral runner managed by ARC looks like this- ARC creates a runner pod for the runner. As it's an ephemeral runner, the `--ephemeral` flag is passed to the `actions/runner` agent that runs within the `runner` container of the runner pod.

`--ephemeral` is an `actions/runner` feature that instructs the runner to stop and de-register itself after the first job run.

Once the ephemeral runner has completed running a workflow job, it stops with a status code of 0, hence the runner pod is marked as completed, removed by ARC.

As it's removed after a workflow job run, the runner pod is never reused across multiple GitHub Actions workflow jobs, providing you a clean environment per each workflow job.

Although not generally recommended, it's possible to disable the passing of the `--ephemeral` flag by explicitly setting `ephemeral: false` in the `RunnerDeployment` or `RunnerSet` spec. When disabled, your runner becomes "persistent". A persistent runner does not stop after workflow job ends, and in this mode `actions/runner` is known to clean only runner's work dir after each job. Whilst this can seem helpful it creates a non-deterministic environment which is not ideal for a CI/CD environment. Between runs, your actions cache, docker images stored in the `dind` and layer cache, globally installed packages etc are retained across multiple workflow job runs which can cause issues that are hard to debug and inconsistent.

Persistent runners are available as an option for some edge cases however they are not preferred as they can create challenges around providing a deterministic and secure environment.

## Deploying Multiple Controllers

> This feature requires controller version => [v0.18.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.18.0)

**_Note: Be aware when using this feature that CRDs are cluster-wide and so you should upgrade all of your controllers (and your CRDs) at the same time if you are doing an upgrade. Do not mix and match CRD versions with different controller versions. Doing so risks out of control scaling._**

By default the controller will look for runners in all namespaces, the watch namespace feature allows you to restrict the controller to monitoring a single namespace. This then lets you deploy multiple controllers in a single cluster. You may want to do this either because you wish to scale beyond the API rate limit of a single PAT / GitHub App configuration or you wish to support multiple GitHub organizations with runners installed at the organization level in a single cluster.

This feature is configured via the controller's `--watch-namespace` flag. When a namespace is provided via this flag, the controller will only monitor runners in that namespace.

You can deploy multiple controllers either in a single shared namespace, or in a unique namespace per controller.

If you plan on installing all instances of the controller stack into a single namespace there are a few things you need to do for this to work.

1. All resources per stack must have a unique name, in the case of Helm this can be done by giving each install a unique release name, or via the `fullnameOverride` properties.
2. `authSecret.name` needs to be unique per stack when each stack is tied to runners in different GitHub organizations and repositories AND you want your GitHub credentials to be narrowly scoped.
3. `leaderElectionId` needs to be unique per stack. If this is not unique to the stack the controller tries to race onto the leader election lock resulting in only one stack working concurrently. Your controller will be stuck with a log message something like this `attempting to acquire leader lease arc-controllers/actions-runner-controller...`
4. The MutatingWebhookConfiguration in each stack must include a namespace selector for that stack's corresponding runner namespace, this is already configured in the helm chart.

Alternatively, you can install each controller stack into a unique namespace (relative to other controller stacks in the cluster). Implementing ARC this way avoids the first, second and third pitfalls (you still need to set the corresponding namespace selector for each stack's mutating webhook)
