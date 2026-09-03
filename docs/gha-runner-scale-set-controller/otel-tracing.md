# OpenTelemetry tracing for listener job lifecycle

The listener can export OpenTelemetry trace spans for every completed
job, alongside (or instead of) the Prometheus metrics:

- `runner.queue` — job queued → assigned to the scale set
- `runner.startup` — assigned → runner picked the job up
- `runner.execution` — runner start → job finish

Spans use the deterministic ID contract published by the runner
(`actions/runner` `docs/otel-id-contract.md`):

- trace ID = `sha256("{run_id}-{run_attempt}")[:16]`
- parent span = the runner's job span,
  `sha256("job-{run_id}-{run_attempt}-{job_name}")[:8]`

so they merge into the runner's native OTLP trace with no propagation
or shared state. Spans carry the resource
`service.name=gha-listener` (override with `OTEL_SERVICE_NAME`),
`service.namespace`, and `github.scale_set.name`.

## Enabling

Via the `gha-runner-scale-set-controller` chart (flows to every
listener through its config secret, like the Prometheus metrics
flags):

```yaml
otel:
  listenerEndpoint: "http://otel-collector:4318"
```

The value is an OTLP/HTTP base URL (the same form as
`OTEL_EXPORTER_OTLP_ENDPOINT`); `/v1/traces` is appended, and TLS
follows the URL scheme.

Alternatively, set the standard `OTEL_EXPORTER_OTLP_*` environment
variables on the listener container (e.g. through the scale set's
`listenerTemplate`, or an OTel Operator that injects them); the SDK's
spec-compliant env handling applies (endpoint, headers, TLS).

Export is asynchronous and bounded: a slow or down collector never
delays autoscaling decisions; on overflow the newest job's spans are
dropped with a warning.

## Known limitations

- The scale-set message protocol does not carry `run_attempt`, so
  spans always assume attempt 1. For workflow re-runs the listener
  spans join the first attempt's trace.
- Configuration is controller-wide (one collector endpoint for all
  scale sets). Per-scale-set configuration would need an
  `AutoscalingRunnerSet` field.
