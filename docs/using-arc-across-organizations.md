# Using ARC across organizations

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Multitenancy

> This feature requires controller version => [v0.26.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.26.0)

In a large enterprise, there might be many GitHub organizations that require self-hosted runners. Previously, the only way to provide ARC-managed self-hosted runners in such environment was [Deploying Multiple Controllers](deploying-arc-runners.md#deploying-multiple-controllers), which incurs overhead due to it requires one ARC installation per GitHub organization.

With multitenancy, you can let ARC manage self-hosted runners across organizations. It's enabled by default and the only thing you need to start using it is to set the `spec.githubAPICredentialsFrom.secretRef.name` fields for the following resources:

- `HorizontalRunnerAutoscaler`
- `RunnerSet`

Or `spec.template.spec.githubAPICredentialsFrom.secretRef.name` field for the following resource:

- `RunnerDeployment`

> Although not explained above, `spec.githubAPICredentialsFrom` fields do exist in `Runner` and `RunnerReplicaSet`. A comparable pod annotation exists for the runner pod, too.
> However, note that `Runner`, `RunnerReplicaSet` and runner pods are implementation details and are managed by `RunnerDeployment` and ARC.
> Usually you don't need to manually set the fields for those resources.

`githubAPICredentialsFrom.secretRef.name` should refer to the name of the Kubernetes secret that contains either PAT or GitHub App credentials that is used for GitHub API calls for the said resource.

Usually, you should have a set of GitHub App credentials per a GitHub organization and you would have a RunnerDeployment and a HorizontalRunnerAutoscaler per an organization runner group. So, you might end up having the following resources for each organization:

- 1 Kubernetes secret that contains GitHub App credentials
- 1 RunnerDeployment/RunnerSet and 1 HorizontalRunnerAutoscaler per Runner Group

And the RunnerDeployment/RunnerSet and HorizontalRunnerAutoscaler should have the same value for `spec.githubAPICredentialsFrom.secretRef.name`, which refers to the name of the Kubernetes secret.

```yaml
kind: Secret
data:
  github_app_id: ...
  github_app_installation_id: ...
  github_app_private_key: ...
---
kind: RunnerDeployment
metadata:
  namespace: org1-runners
spec:
  template:
    spec:
      githubAPICredentialsFrom:
        secretRef:
          name: org1-github-app
---
kind: HorizontalRunnerAutoscaler
metadata:
  namespace: org1-runners
spec:
  githubAPICredentialsFrom:
    secretRef:
      name: org1-github-app
```

> Do note that, as shown in the above example, you usually set the same secret name to `githubAPICredentialsFrom.secretRef.name` fields of both `RunnerDeployment` and `HorizontalRunnerAutoscaler`, so that GitHub API calls for the same set of runners shares the specified credentials, regardless of
when and which varying ARC component(`horizontalrunnerautoscaler-controller`, `runnerdeployment-controller`, `runnerreplicaset-controller`, `runner-controller` or `runnerpod-controller`) makes specific API calls.
> Just don't be surprised you have to repeat `githubAPICredentialsFrom.secretRef.name` settings among two resources!

Please refer to [Deploying Using GitHub App Authentication](authenticating-to-the-github-api.md#deploying-using-github-app-authentication) for how you could create the Kubernetes secret containing GitHub App credentials.
