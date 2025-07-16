# ArgoCD Health Check Configuration for Actions Runner Controller

This document explains how to configure ArgoCD to properly monitor the health status of GitHub Actions Runner resources.

## Problem

By default, ArgoCD doesn't understand the health status of custom resources like `Runner`. Even when a Runner Pod is up and running, ArgoCD may show the status as "Progressing" instead of "Healthy".

## Overview

ArgoCD needs custom health check configurations to understand the status of Actions Runner Controller resources. This guide provides ready-to-use configurations that enable ArgoCD to correctly display the health status of your runners.

## Quick Start

Apply one of the following configurations based on your runner deployment type:

For New Runner API
```bash
kubectl apply -f config/argocd/ephemeralrunner-health.yaml
```

For Legacy Runner API
```bash
kubectl apply -f config/argocd/runner-health.yaml
```

After applying, restart ArgoCD server:
```bash
kubectl rollout restart deployment argocd-server -n argocd
```

## What These Configurations Do

### Runner Health Status in ArgoCD

Once configured, ArgoCD will display runner health as follows:

| Runner State | ArgoCD Display | Description |
|-------------|----------------|-------------|
| Running and Ready | **Healthy** (Green) | Runner is online and processing jobs |
| Starting up | **Progressing** (Yellow) | Runner pod is initializing |
| Failed | **Degraded** (Red) | Runner encountered an error |
| Scaling | **Progressing** (Yellow) | AutoScaler is adjusting runner count |

### Supported Resources

The configurations support three resource types:

1. **Runner** (actions.summerwind.dev/v1alpha1)
   - Legacy runner type
   - Shows as healthy when pod is running and runner is registered

2. **EphemeralRunner** (actions.github.com/v1alpha1)
   - New ephemeral runner type
   - Supports job-specific runners that terminate after use
   - Shows as healthy during job execution and after completion

3. **AutoScalingRunnerSet** (actions.github.com/v1alpha1)
   - Manages groups of ephemeral runners
   - Shows current vs desired runner count
   - Healthy when scaled to target size

## Installation Methods

### Method 1: Apply YAML Files

Use the provided configuration files:

```bash
# For ephemeral runners
kubectl apply -f config/argocd/ephemeralrunner-health.yaml

# For legacy runners
kubectl apply -f config/argocd/runner-health.yaml
```

### Method 2: Edit ConfigMap Directly

Add the health check configurations directly to the existing ArgoCD ConfigMap:

```bash
kubectl edit configmap argocd-cm -n argocd
```

Then add the health check configurations under the `data` section. You can copy the content from the provided YAML files, ensuring proper indentation.

### Method 3: Patch Existing ConfigMap

If you already have an ArgoCD ConfigMap:

```bash
kubectl patch configmap argocd-cm -n argocd --type merge -p @config/argocd/ephemeralrunner-health.yaml
```

### Method 4: Using Kustomize

Create a kustomization.yaml file:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: argocd

configMapGenerator:
- name: argocd-cm
  behavior: merge
  files:
  - resource.customizations.health.actions.summerwind.dev_Runner=config/argocd/runner-health.yaml
  - resource.customizations.health.actions.github.com_EphemeralRunner=config/argocd/ephemeralrunner-health.yaml
```

Then apply with:
```bash
kubectl apply -k .
```

### Method 5: Helm Values

When installing ArgoCD via Helm, add to your values.yaml:

```yaml
server:
  config:
    # Copy the health check configurations from the YAML files
    resource.customizations.health.actions.summerwind.dev_Runner: |
      # ... (content from YAML file)
```

## Verifying the Configuration

### Check ArgoCD UI

1. Navigate to your application in ArgoCD UI
2. Look for Runner resources
3. Verify health status indicators show correct colors

### Using ArgoCD CLI

```bash
# Refresh and check application status
argocd app get <your-app-name> --refresh

# Check specific resource health
argocd app resources <your-app-name> --kind Runner
```

### Using kubectl

Verify runner status that ArgoCD reads:

```bash
# Check runner status
kubectl get runners -o jsonpath='{.items[*].status.phase}'

# Check ephemeral runner status
kubectl get ephemeralrunners -o jsonpath='{.items[*].status.phase}'

# Check autoscaling runner set
kubectl get autoscalingrunnersets -o jsonpath='{.items[*].status.currentReplicas}'
```

## Troubleshooting

### Health Status Not Updating

1. **Verify ConfigMap is applied**:
   ```bash
   kubectl get configmap argocd-cm -n argocd -o yaml | grep actions
   ```

2. **Ensure ArgoCD server was restarted**:
   ```bash
   kubectl rollout status deployment argocd-server -n argocd
   ```

3. **Check ArgoCD logs**:
   ```bash
   kubectl logs -n argocd deployment/argocd-server | grep health
   ```

### Incorrect Health Status

If runners show as "Progressing" when they should be "Healthy":

1. Check runner pod status:
   ```bash
   kubectl get pods -l app.kubernetes.io/name=runner
   ```

2. Verify runner registration:
   ```bash
   kubectl describe runner <runner-name>
   ```

3. Look for status fields:
   - `status.phase` should be "Running"
   - `status.ready` should be "true"
