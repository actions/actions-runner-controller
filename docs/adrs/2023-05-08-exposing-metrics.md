# Exposing metrics

Date: 2023-05-08

**Status**: Proposed

## Context

Prometheus metrics are a common way to monitor the cluster. Providing metrics
can be a helpful way to monitor scale sets and the health of the ephemeral runners.

## Proposal

Two main components are driving the behavior of the scale set:

1. ARC controllers responsible for managing Kubernetes resources.
2. The `AutoscalingListener`, driver of the autoscaling solution responsible for
   describing the desired state.

We can approach publishing those metrics in 3 different ways

### Option 1: Expose a metrics endpoint for the controller-manager and every instance of the listener

To expose metrics, we would need to create 3 additional resources:

1. `ServiceMonitor` - a resource used by Prometheus to match namespaces and
   services from where it needs to gather metrics
2. `Service` for the `gha-runner-scale-set-controller` - service that will
   target ARC controller `Deployment`
3. `Service` for each `gha-runner-scale-set` listener - service that will target
   a single listener pod for each `AutoscalingRunnerSet`

#### Pros

- Easy to control which scale set exposes metrics and which does not.
- Easy to implement using helm charts in case they are enabled per chart
  installation.

#### Cons

- With a cluster running many scale sets, we are going to create a lot of
  resources.
- In case metrics are enabled on the controller manager level, and they should
  be applied across all `AutoscalingRunnerSets`, it is difficult to inherit this
  configuration by applying helm charts.

### Option 2: Create a single metrics aggregator service

To create an aggregator service, we can create a simple web application
responsible for publishing and gathering metrics. All listeners would be
responsible to communicate the metrics on each message, and controllers are
responsible to communicate the metrics on each reconciliation.

The application can be executed as a single pod, or as a side container next to
the manager.

#### Running the aggregator as a container in the controller-manager pod

**Pros:**
- It exists side by side and is following the life cycle of the controller
  manager
- We don't need to introduce another controller managing the state of the pod

**Cons**

- Crashes of the aggregator can influence the controller manager execution
- The controller manager pod needs more resources to run

#### Running the aggregator in a separate pod

**Pros**

- Does not influence the controller manager pod
- The life cycle of the metric can be controlled by the controller manager (by
  implementing another controller)

**Cons**

- We need to implement the controller that can spin up the aggregator in case of
  the crash.
- If we choose not to implement the controller, the resource like `Deployment`
  can be used to manage the aggregator, but we lose control over its life cycle.

#### Metrics webserver requirements

1. Create a web server with a single `/metrics` endpoint. The endpoint will have
   `POST` and `GET` methods registered. The `GET` is used by Prometheus to
   fetch the metrics, while the `POST` is going to be used by controllers and
   listeners to publish their metrics.
2. `ServiceMonitor` - to target the metrics aggregator service
3. `Service` sitting in front of the web server.

**Pros**

- This implementation requires a few additional resources to be created
  in a cluster.
- Web server is easy to implement and easy to document - all metrics are aggregated in a
  single package, and the web server only needs to apply them to its state on
  `POST`. The `GET` handler is simple.
- We can avoid Pushgateway from Prometheus.

**Cons**

- Another image that we need to publish on release.
- Change in metric configuration (on manager update) would require re-creation
  of all listeners. This is not a big problem but is something to point out.
- Managing requests/limits can be tricky.

### Option 3: Use a Prometheus Pushgateway

#### Pros

- Using a supported way of pushing the metrics.
- Easy to implement using their library.

#### Cons

- In the Prometheus docs, they specify that: "Usually, the only valid use case
  for Pushgateway is for capturing the outcome of a service-level batch job".
  The listener does not really fit this criteria.
- Pushgateway is a single point of failure and potential bottleneck.
- You lose Prometheus's automatic instance health monitoring via the up metric (generated on every scrape).
- The Pushgateway never forgets series pushed to it and will expose them to Prometheus forever unless those series are manually deleted via the Pushgateway's API.

## Decision

Since there are many ways in which you can collect metrics, we have decided not
to apply `prometheus-operator` resources nor `Service`.

The responsibility of the controller and the autoscaling listener is
only to expose metrics. It is up to the user to decide how to collect them.

When installing the ARC, the configuration for both the controller manager
and autoscaling listeners' metric servers is established.

### Controller metrics

By default, metrics server is listening on `0.0.0.0:8080`.
You can control the port of the metrics server using the `--metrics-addr` flag.

Metrics can be collected from `/metrics` endpoint

If the value of  `--metrics-addr` is an empty string, metrics server won't be
started.

### Autoscaling listeners

By default, metrics server is listening on `0.0.0.0:8080`.
The endpoint used to expose metrics is `/metrics`.

You can control both the address and the endpoint using `--listener-metrics-addr` and `--listener-metrics-endpoint` flags.

If the value of  `--listener-metrics-addr` is an empty string, metrics server won't be
started.

### Metrics exposed by the controller

To get a better understanding of health and workings of the cluster
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

Users can define alerts, monitor the behavior of both the actions-based metrics
(gathered from the listener) and the Kubernetes resource-based metrics
(gathered from the controller manager).

