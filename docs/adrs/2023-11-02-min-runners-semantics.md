# Changing semantics of the `minRunners` field

**Status**: Accepted

## Context

Current implementation treats the `minRunners` field as the number of runners that should be running on your cluster. They can be busy running the job, starting up, idle. This ensures faster cold startup time when workflows are acquired as well as trying to use the minimum amount of runners needed to fulfill the scaling requirement.

However, especially large and busy clusters could benefit having `minRunners` as minimum idle runners. When jobs are comming in large batches, the `AutoscalingRunnerSet` should pre-emptively increase the number of idle runners to further decrease the startup time for the next batch. In that scenario, the amount of runners that should be created should be calculated as the number of assigned jobs plus the number of `minRunners`.

## Decision

We will redefine the minRunners field to represent the minimum number of idle runners instead. The total number of runners would then be the sum of jobs assigned to the scale set and the minRunners value. If the maxRunners field is set, the desired number of runners will be the lesser of maxRunners and the sum of minRunners and the number of jobs.

The change in the behavior is completely internal, it does not require any modifications on the user side.

## Consequences

Changing the semantics of the `minRunners` field should result in faster job startup times on spikes as well as on cold startups.
