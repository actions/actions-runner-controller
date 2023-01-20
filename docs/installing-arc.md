# Installing Actions Runner Controller

## Overview
You can install ARC and the custom resource definitions using a `kubectl` or `helm` deployment. The installation will create an actions-runner-system namespace in your Kubernetes cluster and deploy the required resources.

## Prerequisites

You must have a Kubernetes cluster already created.

By default, ARC uses cert-manager for certificate management of the admission webhook. Before installing ARC, you must install cert-manager. For instructions on how to install cert-manager on Kubernetes, see "[Installation](https://cert-manager.io/docs/installation/)" in the cert-manager documentation.

## Installing ARC using `kubectl`
**Kubectl Deployment:**

```shell
# REPLACE "v0.25.2" with the version you wish to deploy
kubectl create -f https://github.com/actions/actions-runner-controller/releases/download/v0.25.2/actions-runner-controller.yaml
```
## Installing ARC using `helm`

**Helm Deployment:**

Configure your values.yaml, see the chart's [README](../charts/actions-runner-controller/README.md) for the values documentation

```shell
helm repo add actions-runner-controller https://actions-runner-controller.github.io/actions-runner-controller
helm upgrade --install --namespace actions-runner-system --create-namespace \
             --wait actions-runner-controller actions-runner-controller/actions-runner-controller
```
