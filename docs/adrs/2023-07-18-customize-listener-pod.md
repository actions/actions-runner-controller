# Customize listener pod

**Status**: Proposed

## Context

The Autoscaling listener is a critical component of the autoscaling solution, as it monitors the workload and communicates with the ephemeral runner set to adjust the number of runners as needed.

The problem can arise when cluster policies are configured to disallow pods with (or without) certain fields. Since the Autoscaling listener pod is an internal detail that is not currently customizable, it can be a blocker for users with Kubernetes clusters that enforce such policies.

## Decision

Expose field on the `AutoscalingRunnerSetSpec` resource called `ListenerTemplate` of type `PodTemplateSpec`.

Expose field on the `AutoscalingListenerSpec` resource called `Template` of type `PodTemplateSpec`.

The `AutoscalingRunnerSetController` is responsible for creating the `AutoscalingListener` with the `ListenerTemplate`.

The `AutoscalingListenerController` then creates the listener pod based on the default spec, and the customized spec.

List of fields that are going to be ignored by the merge:

- `spec.serviceAccountName`: Created by the AutoscalingListener.
- reserved `metadata.labels`: Labels that collide with reserved labels used by the system are ignored.
- reserved `spec.containers[0].env`: Environment variables used by the listener application
- `metadata.name`: Name of the listener pod
- `metadata.namespace`: Namespace of the listener pod

### Pros:

- Env `CONTROLLER_MANAGER_LISTENER_IMAGE_PULL_POLICY` can be removed as a global configuration for building Autoscaling listener resources
- Ability to customize securityContext, requests, limits, and other fields.
- Avoid re-creating CRDs whenever a new field requirement occurs. Fields that are not reserved by the controller are applied if specified.

### Cons:

- Keep the documentation updated when new reserved fields are introduced.
- Since the listener spec can be customized, debugging possible problems with customized spec can be harder.

## Consequences

With the listener pod spec exposed, we can provide a way to run ARC for users with policies prohibiting them to do so at this moment.
