# Managing access with runner groups

## Runner Groups

Runner groups can be used to limit which repositories are able to use the GitHub Runner at an organization level. Runner groups have to be [created in GitHub first](https://docs.github.com/en/actions/hosting-your-own-runners/managing-access-to-self-hosted-runners-using-groups) before they can be referenced.

To add the runner to the group `NewGroup`, specify the group in your `Runner` or `RunnerDeployment` spec.

```yaml
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

GitHub supports custom visibility in a Runner Group to make it available to a specific set of repositories only. By default if no GitHub
authentication is included in the webhook server ARC will be assumed that all runner groups to be usable in all repositories.
Currently, GitHub does not include the repository runner group membership information in the workflow_job event (or any webhook). To make the ARC "runner group aware" additional GitHub API calls are needed to find out what runner groups are visible to the webhook's repository. This behaviour will impact your rate-limit budget and so the option needs to be explicitly configured by the end user.

This option will be enabled when proper GitHub authentication options (token, app or basic auth) are provided in the webhook server and `useRunnerGroupsVisibility` is set to true, e.g.

```yaml
githubWebhookServer:
  enabled: false
  replicaCount: 1
  useRunnerGroupsVisibility: true
```