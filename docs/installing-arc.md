# Installing Actions Runner Controller

## Overview
You can install Actions Runner Controller (ARC) using a `kubectl` or `helm` deployment. The installation will create an `actions-runner-system` namespace in your Kubernetes cluster and deploy the required resources.

## Prerequisites

Before installing ARC, you must install cert-manager. By default, ARC uses cert-manager for certificate management of the admission webhook. For instructions on how to install cert-manager on Kubernetes, see "[Installation](https://cert-manager.io/docs/installation/)" in the cert-manager documentation.

## Installing ARC using `kubectl`

1. Create a deployment. Replace `<v0.25.2>` with the version you want to deploy.
```
kubectl create -f https://github.com/actions/actions-runner-controller/releases/download/v0.25.2/actions-runner-controller.yaml
```
## Installing ARC using `helm`

1. Configure the `values.yaml` file. For more information on the possible values, see "[README](../charts/actions-runner-controller/README.md)."<!-- Update article title / link -->

1. Add the repository.
```
helm repo add actions-runner-controller https://actions-runner-controller.github.io/actions-runner-controller
```

1. Install the `helm` chart.
```
helm upgrade --install --namespace actions-runner-system --create-namespace \
             --wait actions-runner-controller actions-runner-controller/actions-runner-controller
```

<!-- Add ## Further Reading  -->