## Docs

All additional docs are kept in the `docs/` folder, this README is solely for documenting the values.yaml keys and values

## Values

**_The values are documented as of HEAD, to review the configuration options for your chart version ensure you view this file at the relevent [tag](https://github.com/actions/actions-runner-controller/tags)_**

> _Default values are the defaults set in the charts values.yaml, some properties have default configurations in the code for when the property is omitted or invalid_

| Key                                             | Description                                                                                                                                                       | Default                   |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `labels`                                        | Set labels to apply to all resources in the chart                                                                                                                 |                           |
| `replicaCount`                                  | Set the number of runner pods                                                                                                                                     | 1                         |
| `image.repository`                              | The "repository/image" of the runner container                                                                                                                    | summerwind/actions-runner |
| `image.tag`                                     | The tag of the runner container                                                                                                                                   |                           |
| `image.pullPolicy`                              | The pull policy of the runner image                                                                                                                               | IfNotPresent              |
| `imagePullSecrets`                              | Specifies the secret to be used when pulling the runner pod containers                                                                                            |                           |
| `fullnameOverride`                              | Override the full resource names                                                                                                                                  |                           |
| `nameOverride`                                  | Override the resource name prefix                                                                                                                                 |                           |
| `podAnnotations`                                | Set annotations for the runner pod                                                                                                                                |                           |
| `podLabels`                                     | Set labels for the runner pod                                                                                                                                     |                           |
| `podSecurityContext`                            | Set the security context to runner pod                                                                                                                            |                           |
| `nodeSelector`                                  | Set the pod nodeSelector                                                                                                                                          |                           |
| `affinity`                                      | Set the runner pod affinity rules                                                                                                                                 |                           |
| `tolerations`                                   | Set the runner pod tolerations                                                                                                                                    |                           |
| `env`                                           | Set environment variables for the runner container                                                                                                                |                           |
| `organization`                                  | Github organization where the runner will be registered                                                                                                           | test                      |
| `repository`                                    | Github repository where the runner will be registered                                                                                                             |                           |
| `runnerLabels`                                  | Labels you want to add in your runner                                                                                                                             | test                      |
| `autoscaler.enabled`                            | Enable the HorizontalRunnerAutoscaler, if its enabled then replica count will not be used                                                                         | true                      |
| `autoscaler.minReplicas`                        | Minimum no of replicas                                                                                                                                            | 1                         |
| `autoscaler.maxReplicas`                        | Maximum no of replicas                                                                                                                                            | 5                         |
| `autoscaler.scaleDownDelaySecondsAfterScaleOut` | [Anti-Flapping Configuration](https://github.com/actions/actions-runner-controller/blob/master/docs/automatically-scaling-runners.md#anti-flapping-configuration) | 120                       |
| `autoscaler.metrics`                            | [Pull driven scaling](https://github.com/actions/actions-runner-controller/blob/master/docs/automatically-scaling-runners.md#pull-driven-scaling)                 | default                   |
| `autoscaler.scaleUpTriggers`                    | [Webhook driven scaling](https://github.com/actions/actions-runner-controller/blob/master/docs/automatically-scaling-runners.md#webhook-driven-scaling)           |                           |
