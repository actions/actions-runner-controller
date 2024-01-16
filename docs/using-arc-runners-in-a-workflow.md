# Using ARC runners in a workflow

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Runner Labels

To run a workflow job on a self-hosted runner, you can use the following syntax in your workflow:

```yaml
jobs:
  release:
    runs-on: self-hosted
```

When you have multiple kinds of self-hosted runners, you can distinguish between them using labels. In order to do so, you can specify one or more labels in your `Runner` or `RunnerDeployment` spec.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: custom-runner
spec:
  replicas: 1
  template:
    spec:
      repository: actions/actions-runner-controller
      labels:
        - custom-runner
```

Once this spec is applied, you can observe the labels for your runner from the repository or organization in the GitHub settings page for the repository or organization. You can now select a specific runner from your workflow by using the label in `runs-on`:

```yaml
jobs:
  release:
    runs-on: custom-runner
```

When using labels there are a few things to be aware of:

1. `self-hosted` is implict with every runner as this is an automatic label GitHub apply to any self-hosted runner. As a result ARC can treat all runners as having this label without having it explicitly defined in a runner's manifest. You do not need to explicitly define this label in your runner manifests (you can if you want though).
2. In addition to the `self-hosted` label, GitHub also applies a few other [default](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/using-self-hosted-runners-in-a-workflow#using-default-labels-to-route-jobs) labels to any self-hosted runner. The other default labels relate to the architecture of the runner and so can't be implicitly applied by ARC as ARC doesn't know if the runner is `linux` or `windows`, `x64` or `ARM64` etc. If you wish to use these labels in your workflows and have ARC scale runners accurately you must also add them to your runner manifests.