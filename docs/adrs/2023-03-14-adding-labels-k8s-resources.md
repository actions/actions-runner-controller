# ADR 2023-04-14: Adding labels to our resources

**Date**: 2023-04-14

**Status**: Done [^1]

## Context

Users need to provide us with logs so that we can help support and troubleshoot their issues. We need a way for our users to filter and retrieve the logs we need.

## Proposal

A good start would be a catch-all label to get all logs that are
ARC-related: one of the [recommended labels](https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/)
is `app.kubernetes.io/part-of` and we can set that for all ARC components
to be `actions-runner-controller`.

Assuming standard logging that would allow us to get all ARC logs by running

```bash
kubectl logs -l 'app.kubernetes.io/part-of=gha-runner-scale-set-controller'
```

which would be very useful for development to begin with.

The proposal is to add these sets of labels to the pods ARC creates:

#### controller-manager

Labels to be set by the Helm chart:

```yaml
metadata:
  labels:
    app.kubernetes.io/part-of: gha-runner-scale-set-controller
    app.kubernetes.io/component: controller-manager
    app.kubernetes.io/version: "x.x.x"
```

#### Listener

Labels to be set by controller at creation:

```yaml
metadata:
  labels:
    app.kubernetes.io/part-of: gha-runner-scale-set-controller
    app.kubernetes.io/component: runner-scale-set-listener
    app.kubernetes.io/version: "x.x.x"
    actions.github.com/scale-set-name: scale-set-name # this corresponds to metadata.name as set for AutoscalingRunnerSet

    # the following labels are to be extracted by the config URL
    actions.github.com/enterprise: enterprise
    actions.github.com/organization: organization
    actions.github.com/repository: repository
```

#### Runner

Labels to be set by controller at creation:

```yaml
metadata:
  labels:
    app.kubernetes.io/part-of: gha-runner-scale-set-controller
    app.kubernetes.io/component: runner
    app.kubernetes.io/version: "x.x.x"
    actions.github.com/scale-set-name: scale-set-name # this corresponds to metadata.name as set for AutoscalingRunnerSet
    actions.github.com/runner-name: runner-name
    actions.github.com/runner-group-name: runner-group-name

    # the following labels are to be extracted by the config URL
    actions.github.com/enterprise: enterprise
    actions.github.com/organization: organization
    actions.github.com/repository: repository
```

This would allow us to ask users:

> Can you please send us the logs coming from pods labelled 'app.kubernetes.io/part-of=gha-runner-scale-set-controller'?

Or for example if they're having problems specifically with runners:

> Can you please send us the logs coming from pods labelled 'app.kubernetes.io/component=runner'?

This way users don't have to understand ARC moving parts but we still have a
way to target them specifically if we need to.

[^1]: Supersedes [ADR 2022-12-05](2022-12-05-adding-labels-k8s-resources.md)