# Exposing metrics

Date: 2023-05-08

**Status**: Proposed

## Context

Prometheus metrics are a common way to monitor the cluster. Providing metrics
can be a helpful way to monitor scale sets and health of the ephemeral runners.

## Proposal

There are two main components driving the behaviour of the scale set:

1. ARC controllers responsible for managing kubernetes resources.
2. The `AutoscalingListener`, driver of the autoscaling solution responsible for
   describing the desired state.

To expose metrics, we would need to create 3 additional resources:

1. `ServiceMonitor` - resource used by prometheus to match namespaces and
    services from where it needs to gather metrics
2. `Service` for the `gha-runner-scale-set-controller` - service that will
    target ARC controller `Deployment`
3. `Service` for the `gha-runner-scale-set` listeners - service that will target
    all listeners for each `AutoscalingRunnerSet`

Metrics can be enabled or disabled on the controller level. If the flag
`metrics-addr` is not empty, the controller and listeners are going to register
the appropriate collectors.

Alternatively, if there is a need to control the metrics exposure per
`AutoscalingRunnerSet`, we can control it by applying additional label on the
listener.

### Metrics exposed by the controller

To get a better understanding about health and workings of the cluster
resources, we need to expose the following metrics:

- `pending_ephemeral_runners` - Number of ephemeral runners in a pending state.
  This information can show the latency between creating an `EphemeralRunner`
  resource, and having an ephemeral runner pod started and ready to receive a
  job.
- `running_ephemeral_runners` - Number of ephemeral runners currently running.
  This information is helpful to see how many ephemeral runner pods are running
  at any given time.
- `failed_ephemeral_runners` - Number of ephemeral runners in a `Failed` state.
  This information is helpful to catch the faulty image, or some underlying
  problem. When the ephemeral runner controller is not able to start the
  ephemeral runner pod after multiple retries, it will set the state of the
  `EphemeralRunner` to failed. Since the controller can not recover from this
  state, it can be useful to set Prometheus alerts to catch this issue quickly.

### Metrics exposed by the `AutoscalingListener`

Since the listener is responsible for communicating the state with the actions
service, it can expose actions service related data through metrics. In
particular:

- `available_jobs` - Number of jobs with `runs-on` matching the runner scale set name. Jobs are not yet assigned but are acquired by the runner scale set.
- `acquired_jobs`- Number of jobs acquired by the scale set.
- `assigned_jobs` - Number of jobs assigned to this scale set.
- `running_jobs` - Number of jobs running (or about to be run).
- `registered_runners` - Number of registered runners.
- `busy_runners` - Number of registered runners running a job.
- `min_runners` - Number of runners desired by the scale set.
- `max_runners` - Number of runners desired by the scale set.
- `desired_runners` - Number of runners desired by the scale set.
- `idle_runners` - Number of registered runners not running a job.
- `available_jobs_total` - Total number of jobs available for the scale set (runs-on matches and scale set passes all the runner group permission checks).
- `acquired_jobs_total` - Total number of jobs acquired by the scale set.
- `assigned_jobs_total` - Total number of jobs assigned to the scale set.
- `started_jobs_total` - Total number of jobs started.
- `completed_jobs_total` - Total number of jobs completed.
- `job_queue_duration_seconds` - Time spent waiting for workflow jobs to get assigned to the scale set after queueing (in seconds).
- `job_startup_duration_seconds` - Time spent waiting for a workflow job to get started on the runner owned by the scale set (in seconds).
- `job_execution_duration_seconds` - Time spent executing workflow jobs by the scale set (in seconds).

### Metric names

Listener metrics belong to the `github_runner_scale_set` subsystem, so the names
are going to have the `github_runner_scale_set_` prefix.

Controller metrics belong to the `github_runner_scale_set_controller` subsystem,
so the names are going to have `github_runner_scale_set_controller` prefix.

## Consequences

Users can define alerts, monitor the behaviour of both the actions-based metrics
(gathered from the listener) and the kubernetes resource-based metrics
(gathered from the controller manager).
