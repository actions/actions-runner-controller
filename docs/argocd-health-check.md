# ArgoCD Health Check Configuration for Actions Runner Controller

This document explains how to configure ArgoCD to properly recognize the health status of Runner resources.

## Problem

By default, ArgoCD doesn't understand the health status of custom resources like `Runner`. Even when a Runner Pod is up and running, ArgoCD may show the status as "Progressing" instead of "Healthy".

## Solution

Add a custom health check configuration to ArgoCD's ConfigMap to interpret the Runner resource's status fields.

### 1. Apply the Custom Health Check

Apply the following configuration to your ArgoCD installation:

```bash
kubectl apply -f argocd-runner-health.yaml
```

Or, if you already have an `argocd-cm` ConfigMap, add the following to the `data` section:

```yaml
data:
  resource.customizations.health.actions.summerwind.dev_Runner: |
    hs = {}
    if obj.status ~= nil then
      if obj.status.ready == true and obj.status.phase == "Running" then
        hs.status = "Healthy"
        hs.message = "Runner is ready and running"
      elseif obj.status.phase == "Pending" or obj.status.phase == "Created" then
        hs.status = "Progressing"
        hs.message = "Runner is starting up"
      elseif obj.status.phase == "Failed" then
        hs.status = "Degraded"
        hs.message = obj.status.message or "Runner has failed"
      else
        hs.status = "Progressing"
        hs.message = "Runner status: " .. (obj.status.phase or "Unknown")
      end
    else
      hs.status = "Progressing"
      hs.message = "Waiting for runner status"
    end
    return hs
```

### 2. Restart ArgoCD Server

After applying the configuration, restart the ArgoCD server to load the new health check:

```bash
kubectl rollout restart deployment argocd-server -n argocd
```

## Health Status Mapping

The custom health check maps Runner statuses to ArgoCD health statuses as follows:

| Runner Status | ArgoCD Health Status | Description |
| -- | -- | -- |
| `ready: true` and `phase: Running` | Healthy | Runner is fully operational |
| `phase: Pending` or `Created` | Progressing | Runner is starting up |
| `phase: Failed` | Degraded | Runner has encountered an error |
| Other states | Progressing | Runner is in transition |

## Verification

After configuration, you can verify the health status in ArgoCD:

1. Check the ArgoCD UI - Runner resources should show as "Healthy" when ready
2. Use the ArgoCD CLI:
   ```bash
   argocd app get <your-app-name> --refresh
   ```

## Alternative Approach: Patching the ConfigMap

If you need to patch an existing ConfigMap:

```bash
kubectl patch configmap argocd-cm -n argocd --type merge -p '{"data":{"resource.customizations.health.actions.summerwind.dev_Runner":"hs = {}\nif obj.status ~= nil then\n  if obj.status.ready == true and obj.status.phase == \"Running\" then\n    hs.status = \"Healthy\"\n    hs.message = \"Runner is ready and running\"\n  elseif obj.status.phase == \"Pending\" or obj.status.phase == \"Created\" then\n    hs.status = \"Progressing\"\n    hs.message = \"Runner is starting up\"\n  elseif obj.status.phase == \"Failed\" then\n    hs.status = \"Degraded\"\n    hs.message = obj.status.message or \"Runner has failed\"\n  else\n    hs.status = \"Progressing\"\n    hs.message = \"Runner status: \" .. (obj.status.phase or \"Unknown\")\n  end\nelse\n  hs.status = \"Progressing\"\n  hs.message = \"Waiting for runner status\"\nend\nreturn hs"}}'
```
