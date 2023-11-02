# Introducing min runner strategies

**Status**: Proposed

## Context

Current implementation treats the `minRunners` field as the number of runners that should be running on your cluster. They can be busy running the job, starting up, idle. This ensures faster cold startup time when workflows are acquired as well as trying to use the minimum amount of runners needed to fulfill the scaling requirement.

However, large and busy clusters could benefit having `minRunners` as `minIdleRunners`. When jobs are comming in large batches, the `AutoscalingRunnerSet` should pre-emptively increase the number of idle runners to further decrease the startup time for the next batch. In that scenario, the amount of runners that should be created should be calculated as the number of acquired workflows plus the number of `minRunners`.

## Decision

The decision is to include an additional `minRunnerStrategy` field per `AutoscalingRunnerSet`. This field will allow the scaling strategy to be picked based on the scale set. There will be two strategies: "lazy" and "eager".

With the "lazy" strategy, the current behavior of `minRunners` will be preserved. The `minRunners` field will specify the minimum number of runners running in a cluster regardless of their state (running, starting up or idle).

With the "eager" strategy, `minRunners` will be treated as `minIdleRunners`. This strategy will calculate the number of runners based on the number of workflows acquired plus the number of minRunners. If the `maxRunners` field is specified, it will be respected, so the number of idle runners can be less than the number of idle `minRunners` when the number of acquired jobs plus the number of min runners is greater than `maxRunners`.

An additional field will be added as a top-level field in values.yaml for gha-runner-scale-set. The default value will be "lazy" to preserve backward compatibility. If the value is neither "lazy" nor "eager", it will default to "lazy".

## Consequences

The introduction of the minRunnerStrategy field per AutoscalingRunnerSet and the inclusion of two strategies, "lazy" and "eager", will provide greater flexibility and control over the scaling behavior of the scale set. The "eager" strategy, in particular, will allow for pre-emptive scaling up of idle runners, resulting in significantly reduced startup times for busy scale sets. This will improve the overall performance and efficiency of the cluster, leading to faster job completion times.