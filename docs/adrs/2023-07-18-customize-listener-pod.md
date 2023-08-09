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

The change is extending the gha-runner-scale-set template. We will extend `values.yaml` file to add `listenerTemplate` object that is optional.

If not provided, the listener will be created the same way it was created before. Otherwise, the `listenerTemplate.metadata` and the `listenerTemplate.spec` are going to be merged with the default listener specification.

The way the merge will work is:

1. Create a default spec used for the listener
2. All non-reserved fields are going to be applied from the provided `listenerTemplate` if they are not empty. If empty, the default configuration is used.
3. For the container:
   1. If the container name is "listener", values specified for that container are going to be merged with the default listener container spec. The name "listener" serves just as an indicator that the container spec should be merged with the listener container. Name will be overwritten by the controller. All fields are optional, and non-null fields are merged as described above.
   2. If the container name is **not** "listener", the spec provided for that container will be appended to the `pod.spec.containers` without any modifications. Fields that must be specified are the required fields for the kubernetes container spec.

### Pros:

- Env `CONTROLLER_MANAGER_LISTENER_IMAGE_PULL_POLICY` can be removed as a global configuration for building Autoscaling listener resources
- Ability to customize securityContext, requests, limits, and other fields.
- Avoid re-creating CRDs whenever a new field requirement occurs. Fields that are not reserved by the controller are applied if specified.

### Cons:

- Keep the documentation updated when new reserved fields are introduced.
- Since the listener spec can be customized, debugging possible problems with customized spec can be harder.

## Consequences

With the listener pod spec exposed, we can provide a way to run ARC for users with policies prohibiting them to do so at this moment.
