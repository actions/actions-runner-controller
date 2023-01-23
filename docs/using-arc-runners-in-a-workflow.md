# Using Actions Runner Controller runners in a workflow

To run a workflow job on any self-hosted runner, you can use the following syntax in your GitHub Actions workflow:

```yaml
jobs:
  release:
    runs-on: self-hosted
```

If you have multiple kinds of self-hosted runners, you can choose specific types of runners using labels. For more information, see "[Choosing the runner for a job](https://docs.github.com/en/actions/using-jobs/choosing-the-runner-for-a-job)."

To assign workflow jobs to runners created by Actions Runner Controller (ARC), you must set specific labels for the runners, and then use those labels to route jobs to the runners.

1. When creating your ARC `Runner` or `RunnerDeployment` spec, you can specify one or more labels to assign to your runners when they are created. For example, the following `RunnerDeployment` spec assigns the `custom-runner` label to new runners:

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

   When this spec is applied, you can see the labels assigned your runners in the settings for the repository or organization where the runners were created. For more information, see "[Monitoring and troubleshooting self-hosted runners](https://docs.github.com/en/actions/hosting-your-own-runners/monitoring-and-troubleshooting-self-hosted-runners#checking-the-status-of-a-self-hosted-runner)."

   > **Note:** GitHub automatically applies some default labels to every self-hosted runner. These include the `self-hosted` label, as well as labels for each runner's operating system and architecture. For more information, see "[Using self-hosted runners in a workflow](https://docs.github.com/en/actions/hosting-your-own-runners/using-self-hosted-runners-in-a-workflow#using-default-labels-to-route-jobs)."
   >
   > If you are going to use the default operating system or architecture labels in your workflows and want ARC to accurately scale runners based on them, you must also add those labels to your runner manifests.
1. You can now select a specific type of runner in your workflow job by using the label in `runs-on`, in combination with the `self-hosted` label. For example, to use the label assigned in the previous step:

   ```yaml
   jobs:
     release:
       runs-on: [self-hosted, custom-runner]
   ```

   Although the `self-hosted` label is not required, we strongly recommend specifying it when using self-hosted runners to ensure that your job does not unintentionally specify any current or future GitHub-hosted runners.<!-- Make this a reusable with the existing instances of this para -->
