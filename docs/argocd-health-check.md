# ArgoCD Health Checks for Actions Runner Controller

This document explains how to configure ArgoCD to properly monitor the health status of GitHub Actions Runner resources.

## Problem

By default, ArgoCD doesn't understand the health status of custom resources like `Runner`. Even when a Runner Pod is up and running, ArgoCD may show the status as "Progressing" instead of "Healthy".

## Overview

ArgoCD needs custom health check configurations to understand the status of Actions Runner Controller resources. This guide provides ready-to-use configurations that enable ArgoCD to correctly display the health status of your runners.

## File Structure

```
config/argocd/
├── README.md                               # This file
├── argocd-cm.yaml                          # Complete health check configuration
├── health-check-runner.yaml                # Legacy Runner API health check
├── health-check-ephemeralrunner.yaml       # New Runner API health check
├── health-check-autoscalingrunnerset.yaml  # AutoScalingRunnerSet health check
├── health-check-pod.yaml                   # Pod health check for runners
└── kustomization.yaml                      # Main kustomization file with usage examples
```

## Quick Start

### Method 1: Apply All Health Checks

```sh
kubectl apply -f config/argocd/argocd-cm.yaml
```

### Method 2: Use Kustomize

```sh
kubectl apply -k .
```

### Method 3: Apply Specific Health Checks

```sh
# For legacy runners only
kubectl apply -f health-check-runner.yaml

# For new API runners
kubectl apply -f health-check-ephemeralrunner.yaml
kubectl apply -f health-check-autoscalingrunnerset.yaml

# For pod monitoring
kubectl apply -f health-check-pod.yaml
```

### Method 4: Edit ConfigMap Directly

Add the health check configurations directly to the existing ArgoCD ConfigMap:

```sh
kubectl edit configmap argocd-cm -n argocd
```

Then add the health check configurations under the `data` section. You can copy the content from the provided YAML files, ensuring proper indentation.

### Method 5: Patch Existing ConfigMap

If you already have an ArgoCD ConfigMap:

```sh
kubectl patch configmap argocd-cm -n argocd --type merge -p @config/argocd/ephemeralrunner-health.yaml
```

### Method 6: Helm Values

When installing ArgoCD via Helm, add to your values.yaml:

```yaml
server:
  config:
    # Copy the health check configurations from the YAML files
    resource.customizations.health.actions.summerwind.dev_Runner: |
      # ... (content from YAML file)
```

## Kustomize Usage

The provided `kustomization.yaml` file includes three different usage patterns:

### Option 1: Apply All Health Checks
The default configuration applies all health checks at once using the complete `argocd-cm.yaml`.

### Option 2: Selective Health Checks
Uncomment specific patches in `kustomization.yaml` to apply only the health checks you need.

### Option 3: ConfigMapGenerator
Use the configMapGenerator approach when ArgoCD ConfigMap is managed by another system. This method merges health checks without replacing the existing ConfigMap.

See `kustomization.yaml` for detailed examples and comments for each option.

## Verifying the Configuration

### Check ArgoCD UI

1. Navigate to your application in ArgoCD UI
2. Look for Runner resources
3. Verify health status indicators show correct colors

### Using ArgoCD CLI

```sh
# Refresh and check application status
argocd app get <your-app-name> --refresh

# Check specific resource health
argocd app resources <your-app-name> --kind Runner
```

### Using kubectl

Verify runner status that ArgoCD reads:

```sh
# Check runner status
kubectl get runners -o jsonpath='{.items[*].status.phase}'

# Check ephemeral runner status
kubectl get ephemeralrunners -o jsonpath='{.items[*].status.phase}'

# Check autoscaling runner set
kubectl get autoscalingrunnersets -o jsonpath='{.items[*].status.currentReplicas}'
```

## Health Status Mappings

### Supported Resources

The configurations support three resource types:

1. **Runner** (actions.summerwind.dev/v1alpha1)
   - Legacy runner type
   - Shows as healthy when pod is running and runner is registered

2. **EphemeralRunner** (actions.github.com/v1alpha1)
   - New ephemeral runner type
   - Supports job-specific runners that terminate after use
   - Shows as healthy during job execution and after completion
   - Enhanced status tracking including job IDs and runner IDs

3. **AutoScalingRunnerSet** (actions.github.com/v1alpha1)
   - Manages groups of ephemeral runners
   - Shows current vs desired runner count
   - Healthy when scaled to target size
   - Displays pending, running, and terminating runner counts

4. **Pod** (v1)
   - Health checks for runner pods specifically
   - Monitors container readiness and status
   - Detects common issues like CrashLoopBackOff and ImagePullBackOff
   - Only applies to pods with runner-specific labels

### Runner States

| Resource Type        | State               | ArgoCD Status | Description           |
|----------------------|---------------------|---------------|-----------------------|
| Runner               | Running + Ready     | Healthy       | Runner is operational |
| Runner               | Running + Not Ready | Progressing   | Runner is starting    |
| Runner               | Failed/Error        | Degraded      | Runner has failed     |
| EphemeralRunner      | Running/Finished    | Healthy       | Runner completed job  |
| EphemeralRunner      | Failed              | Degraded      | Runner failed         |
| AutoScalingRunnerSet | Desired = Ready     | Healthy       | All runners ready     |
| AutoScalingRunnerSet | Scaling             | Progressing   | Scaling in progress   |

### Pod States

| Pod Phase | Container Status | ArgoCD Status | Description           |
|-----------|------------------|---------------|-----------------------|
| Running   | All Ready        | Healthy       | Pod fully operational |
| Succeeded | -                | Healthy       | Pod completed         |
| Failed    | -                | Degraded      | Pod failed            |
| Pending   | -                | Progressing   | Pod starting          |
| Running   | Not Ready        | Progressing   | Containers starting   |

## Important Notes

1. **Restart ArgoCD**: After applying health checks, restart ArgoCD server:
```sh
kubectl rollout restart deployment argocd-server -n argocd
```

2. **Label Detection**: Pod health checks only apply to pods with runner-specific labels

3. **Namespace**: All configurations assume ArgoCD is installed in the `argocd` namespace

## Troubleshooting

### Health Status Not Updating

1. Verify ConfigMap is applied:
```sh
kubectl get configmap argocd-cm -n argocd -o yaml | grep customizations
```

2. Check ArgoCD logs:
```sh
kubectl logs -n argocd deployment/argocd-server | grep health
```

3. Refresh application in ArgoCD:
```sh
argocd app get <app-name> --refresh
```

### Incorrect Health Status

1. Check runner status:
```sh
kubectl get runners -o yaml
kubectl get pods -l app.kubernetes.io/component=runner
```

2. Verify labels on pods:
```sh
kubectl get pods -o jsonpath='{.items[*].metadata.labels}'
```
