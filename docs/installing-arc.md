# Installing ARC

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Installation

By default, actions-runner-controller uses [cert-manager](https://cert-manager.io/docs/installation/kubernetes/) for certificate management of Admission Webhook. Make sure you have already installed cert-manager before you install. The installation instructions for the cert-manager can be found below.

- [Installing cert-manager on Kubernetes](https://cert-manager.io/docs/installation/kubernetes/)

After installing cert-manager, install the custom resource definitions and actions-runner-controller with `kubectl` or `helm`. This will create an actions-runner-system namespace in your Kubernetes and deploy the required resources.

**Kubectl Deployment:**

```shell
# REPLACE "v0.25.2" with the version you wish to deploy
kubectl create -f https://github.com/actions/actions-runner-controller/releases/download/v0.25.2/actions-runner-controller.yaml
```

**Helm Deployment:**

Configure your values.yaml, see the chart's [README](../charts/actions-runner-controller/README.md) for the values documentation

```shell
helm repo add actions-runner-controller https://actions-runner-controller.github.io/actions-runner-controller
helm upgrade --install --namespace actions-runner-system --create-namespace \
             --wait actions-runner-controller actions-runner-controller/actions-runner-controller
```

### Granting users access to ARC resources

All three Helm charts (`gha-runner-scale-set-controller`, `gha-runner-scale-set-controller-experimental` and the legacy `actions-runner-controller`) ship two ClusterRoles: `aggregate-to-view` (read verbs, aggregated into `view`, `edit` and `admin`) and `aggregate-to-edit` (write verbs, aggregated into `edit` and `admin`). The legacy chart and the kustomize manifests additionally ship `aggregate-to-read-sensitive` (read verbs for `runners`, aggregated into `edit` and `admin` only, not `view`). Set `rbac.aggregateRoles.enabled=false` (`controller.rbac.aggregateRoles.enabled` in the experimental chart) to skip them. They are aggregated into the builtin `view`, `edit` and `admin` roles, so users bound to those roles can read (and, for `edit`/`admin`, manage) ARC resources without extra bindings. The legacy `Runner` resource is intentionally not readable through `view` because `status.registration.token` holds a runner registration token; the separate `aggregate-to-read-sensitive` role grants edit and admin read access to it, and `aggregate-to-edit` grants the write access.
