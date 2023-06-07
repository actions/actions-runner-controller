# ADR 2023-04-11: Limit Permissions for Service Accounts in Actions-Runner-Controller

**Date**: 2023-04-11

**Status**: Done [^1]

## Context

- `actions-runner-controller` is a Kubernetes CRD (with controller) built using https://github.com/kubernetes-sigs/controller-runtime

- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) has a default cache based k8s API client.Reader to make query k8s API server more efficiency.

- The cache-based API client requires cluster scope `list` and `watch` permission for any resource the controller may query.

- This documentation only scopes to the AutoscalingRunnerSet CRD and its controller.

## Service accounts and their role binding in actions-runner-controller

There are 3 service accounts involved for a working `AutoscalingRunnerSet` based `actions-runner-controller`

1. Service account for each Ephemeral runner Pod

This should have the lowest privilege (not any `RoleBinding` nor `ClusterRoleBinding`) by default, in the case of `containerMode=kubernetes`, it will get certain write permission with `RoleBinding` to limit the permission to a single namespace.

> References:
>
> - ./charts/gha-runner-scale-set/templates/no_permission_serviceaccount.yaml
> - ./charts/gha-runner-scale-set/templates/kube_mode_role.yaml
> - ./charts/gha-runner-scale-set/templates/kube_mode_role_binding.yaml
> - ./charts/gha-runner-scale-set/templates/kube_mode_serviceaccount.yaml

2. Service account for AutoScalingListener Pod

This has a `RoleBinding` to a single namespace with a `Role` that has permission to `PATCH` `EphemeralRunnerSet` and `EphemeralRunner`.

3. Service account for the controller manager

Since the CRD controller is a singleton installed in the cluster that manages the CRD across multiple namespaces by default, the service account of the controller manager pod has a `ClusterRoleBinding` to a `ClusterRole` with broader permissions.

The current `ClusterRole` has the following permissions:

- Get/List/Create/Delete/Update/Patch/Watch on `AutoScalingRunnerSets` (with `Status` and `Finalizer` sub-resource)
- Get/List/Create/Delete/Update/Patch/Watch on `AutoScalingListeners` (with `Status` and `Finalizer` sub-resource)
- Get/List/Create/Delete/Update/Patch/Watch on `EphemeralRunnerSets` (with `Status` and `Finalizer` sub-resource)
- Get/List/Create/Delete/Update/Patch/Watch on `EphemeralRunners` (with `Status` and `Finalizer` sub-resource)

- Get/List/Create/Delete/Update/Patch/Watch on `Pods` (with `Status` sub-resource)
- **Get/List/Create/Delete/Update/Patch/Watch on `Secrets`**
- Get/List/Create/Delete/Update/Patch/Watch on `Roles`
- Get/List/Create/Delete/Update/Patch/Watch on `RoleBindings`
- Get/List/Create/Delete/Update/Patch/Watch on `ServiceAccounts`

> Full list can be found at: https://github.com/actions/actions-runner-controller/blob/facae69e0b189d3b5dd659f36df8a829516d2896/charts/actions-runner-controller-2/templates/manager_role.yaml

## Limit cluster role permission on Secrets

The cluster scope `List` `Secrets` permission might be a blocker for adopting `actions-runner-controller` for certain customers as they may have certain restriction in their cluster that simply doesn't allow any service account to have cluster scope `List Secrets` permission.

To help these customers and improve security for `actions-runner-controller` in general, we will try to limit the `ClusterRole` permission of the controller manager's service account down to the following:

- Get/List/Create/Delete/Update/Patch/Watch on `AutoScalingRunnerSets` (with `Status` and `Finalizer` sub-resource)
- Get/List/Create/Delete/Update/Patch/Watch on `AutoScalingListeners` (with `Status` and `Finalizer` sub-resource)
- Get/List/Create/Delete/Update/Patch/Watch on `EphemeralRunnerSets` (with `Status` and `Finalizer` sub-resource)
- Get/List/Create/Delete/Update/Patch/Watch on `EphemeralRunners` (with `Status` and `Finalizer` sub-resource)

- List/Watch on `Pods`
- List/Watch/Patch on `Roles`
- List/Watch on `RoleBindings`
- List/Watch on `ServiceAccounts`

> We will change the default cache-based client to bypass cache on reading `Secrets` and `ConfigMaps`(ConfigMap is used when you configure `githubServerTLS`), so we can eliminate the need for `List` and `Watch` `Secrets` permission in cluster scope.

Introduce a new `Role` for the controller and `RoleBinding` the `Role` with the controller's `ServiceAccount` in the namespace the controller is deployed. This role will grant the controller's service account required permission to work with `AutoScalingListeners` in the controller namespace.

- Get/Create/Delete on `Pods`
- Get on `Pods/status`
- Get/Create/Delete/Update/Patch on `Secrets`
- Get/Create/Delete/Update/Patch on `ServiceAccounts`

The `Role` and `RoleBinding` creation will happen during the `helm install demo oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller`

During `helm install demo oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller`, we will store the controller's service account info as labels on the controller `Deployment`.
Ex:

```yaml
actions.github.com/controller-service-account-namespace: {{ .Release.Namespace }}
actions.github.com/controller-service-account-name: {{ include "gha-runner-scale-set-controller.serviceAccountName" . }}
```

Introduce a new `Role` per `AutoScalingRunnerSet` installation and `RoleBinding` the `Role` with the controller's `ServiceAccount` in the namespace that each `AutoScalingRunnerSet` deployed with the following permission.

- Get/Create/Delete/Update/Patch/List on `Secrets`
- Create/Delete on `Pods`
- Get on `Pods/status`
- Get/Create/Delete/Update/Patch on `Roles`
- Get/Create/Delete/Update/Patch on `RoleBindings`
- Get on `ConfigMaps`

The `Role` and `RoleBinding` creation will happen during `helm install demo oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set` to grant the controller's service account required permissions to operate in the namespace the `AutoScalingRunnerSet` deployed.

The `gha-runner-scale-set` helm chart will try to find the `Deployment` of the controller using `helm lookup`, and get the service account info from the labels of the controller `Deployment` (`actions.github.com/controller-service-account-namespace` and `actions.github.com/controller-service-account-name`).

The `gha-runner-scale-set` helm chart will use this service account to properly render the `RoleBinding` template.

The `gha-runner-scale-set` helm chart will also allow customers to explicitly provide the controller service account info, in case the `helm lookup` couldn't locate the right controller `Deployment`.

New sections in `values.yaml` of `gha-runner-scale-set`:

```yaml
## Optional controller service account that needs to have required Role and RoleBinding
## to operate this gha-runner-scale-set installation.
## The helm chart will try to find the controller deployment and its service account at installation time.
## In case the helm chart can't find the right service account, you can explicitly pass in the following value
## to help it finish RoleBinding with the right service account.
## Note: if your controller is installed to only watch a single namespace, you have to pass these values explicitly.
controllerServiceAccount:
  namespace: arc-system
  name: test-arc-gha-runner-scale-set-controller
```

## Install ARC to only watch/react resources in a single namespace

In case the user doesn't want to have any `ClusterRole`, they can choose to install the `actions-runner-controller` in a mode that only requires a `Role` with `RoleBinding` in a particular namespace.

In this mode, the `actions-runner-controller` will only be able to watch the `AutoScalingRunnerSet` resource in a single namespace.

If you want to deploy multiple `AutoScalingRunnerSet` into different namespaces, you will need to install `actions-runner-controller` in this mode multiple times as well and have each installation watch the namespace you want to deploy an `AutoScalingRunnerSet`

You will install `actions-runner-controller` with something like `helm install arc --namespace arc-system --set watchSingleNamespace=test-namespace oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller` (the `test-namespace` namespace needs to be created first).

You will deploy the `AutoScalingRunnerSet` with something like `helm install demo --namespace TestNamespace oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set`

In this mode, you will end up with a manager `Role` that has all Get/List/Create/Delete/Update/Patch/Watch permissions on resources we need, and a `RoleBinding` to bind the `Role` with the controller `ServiceAccount` in the watched single namespace and the controller namespace, ex: `test-namespace` and `arc-system` in the above example.

The downside of this mode:

- When you have multiple controllers deployed, they will still use the same version of the CRD. So you will need to make sure every controller you deployed has to be the same version as each other.
- You can't mismatch install both `actions-runner-controller` in this mode (watchSingleNamespace) with the regular installation mode (watchAllClusterNamespaces) in your cluster.

## Cleanup process

We will apply following annotations during the installation that are going to be used in the cleanup process (`helm uninstall`). If annotation is not present, cleanup of that resource is going to be skipped.

The cleanup only patches the resource removing the `actions.github.com/cleanup-protection` finalizer. The client that created a resource is responsible for deleting them. Keep in mind, `helm uninstall` will automatically delete resources, causing the cleanup procedure to be complete.

Annotations applied to the `AutoscalingRunnerSet` used in the cleanup procedure
are:

- `actions.github.com/cleanup-github-secret-name`
- `actions.github.com/cleanup-manager-role-binding`
- `actions.github.com/cleanup-manager-role-name`
- `actions.github.com/cleanup-kubernetes-mode-role-binding-name`
- `actions.github.com/cleanup-kubernetes-mode-role-name`
- `actions.github.com/cleanup-kubernetes-mode-service-account-name`
- `actions.github.com/cleanup-no-permission-service-account-name`

The order in which resources are being patched to remove finalizers:

1. Kubernetes mode `RoleBinding`
1. Kubernetes mode `Role`
1. Kubernetes mode `ServiceAccount`
1. No permission `ServiceAccount`
1. GitHub `Secret`
1. Manager `RoleBinding`
1. Manager `Role`

[^1]: Supersedes [ADR 2023-02-10](2023-02-10-limit-manager-role-permission.md)
