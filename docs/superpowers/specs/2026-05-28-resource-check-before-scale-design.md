# Resource Check Before Scale-Up Design

**Date:** 2026-05-28  
**Branch:** add_resource_check

## Overview

Before accepting GitHub CI tasks, the scaler checks whether the cluster has sufficient
resources (CPU, memory, NPU) to run the requested number of runners. If resources are
insufficient, all incoming tasks are rejected (scale to 0). This prevents over-scheduling
on clusters with Ascend NPU or heterogeneous CPU node pools.

## Requirements

- Check cluster-wide available resources before scaling up
- Filter nodes by the runner's `nodeSelector` (e.g., `resources.type.arch: arm64`)
- Count both Running and Pending pods as consuming resources
- Reject all runners (return 0) if resources are insufficient for the full requested count
- Fail-open: if the resource check itself errors, proceed with normal scaling

## Architecture

### New file: `cmd/ghalistener/scaler/resource_checker.go`

**Interface:**

```go
type ResourceChecker interface {
    HasSufficientResources(ctx context.Context, count int) (bool, error)
}
```

**Implementation: `KubernetesResourceChecker`**

```go
type KubernetesResourceChecker struct {
    clientset              *kubernetes.Clientset
    ephemeralRunnerSetNS   string
    ephemeralRunnerSetName string
    logger                 *slog.Logger
}
```

Shares the same `clientset` and EphemeralRunnerSet coordinates as the Scaler.
No additional configuration required.

### Modified: `cmd/ghalistener/scaler/scaler.go`

- New field: `resourceChecker ResourceChecker` (nil = skip check, backward compatible)
- New option: `WithResourceChecker(rc ResourceChecker) Option`
- `HandleDesiredRunnerCount` calls the checker before existing scaling logic

### Modified: `cmd/ghalistener/main.go`

Constructs `KubernetesResourceChecker` after creating the k8s clientset and injects it
via `WithResourceChecker`.

### Component relationship

```
main.go
  └─ scaler.New(..., WithResourceChecker(checker))
       └─ HandleDesiredRunnerCount
            └─ checker.HasSufficientResources(ctx, count)
                 ├─ fetch EphemeralRunnerSet → extract resources + nodeSelector
                 ├─ list matching nodes → sum Allocatable
                 └─ list Running/Pending pods → sum Requests → compute available
```

## Algorithm: HasSufficientResources

**Step 1 — Read runner resource requirements**

Fetch `EphemeralRunnerSet` via k8s API. Extract from
`spec.ephemeralRunnerSpec.spec.containers[0].resources.requests`:
- `cpu`
- `memory`
- All `huawei.com/*` extended resources (NPU)

Also extract `spec.ephemeralRunnerSpec.spec.nodeSelector` (may be empty).

**Step 2 — Filter target nodes**

List all Nodes. Keep nodes whose labels contain all key-value pairs from nodeSelector.
If nodeSelector is empty, all nodes are included.

**Step 3 — Sum cluster allocatable**

```
clusterAllocatable = Σ node.Status.Allocatable  (over target nodes)
```

**Step 4 — Sum used resources**

List all Pods. Include a pod if:
- Its `spec.nodeName` is in the target node set, OR `spec.nodeName` is empty (unbound Pending)
- Its phase is `Running` or `Pending`
- Its phase is NOT `Succeeded` or `Failed`

```
usedResources = Σ pod.Spec.Containers[*].Resources.Requests
```

Unbound Pending pods (nodeName == "") are subtracted from cluster totals directly,
making the check maximally conservative.

**Step 5 — Decision**

```
available = clusterAllocatable - usedResources

for each resource type r in {cpu, memory, huawei.com/ascend-*}:
    if count × runnerRequest[r] > available[r]:
        return false
return true
```

## Error Handling

| Situation | Behavior |
|-----------|----------|
| k8s API error | Log warning, fail-open (proceed with scaling) |
| EphemeralRunnerSet not found | Return error → fail-open |
| `resources.requests` empty on runner | Skip check, return true |
| Node lacks an extended resource the runner needs | available = 0 → reject |
| `resourceChecker` is nil | Skip check entirely |

**Rejection log format (structured):**

```
level=INFO msg="Insufficient resources, rejecting scale-up"
  resource=huawei.com/ascend-1980 requested=16 available=12
  resource=cpu requested=256 available=180
```

## Integration point in HandleDesiredRunnerCount

```go
if w.resourceChecker != nil {
    ok, err := w.resourceChecker.HasSufficientResources(ctx, count)
    if err != nil {
        w.logger.Warn("Resource check failed, proceeding without check", "error", err)
    } else if !ok {
        w.logger.Info("Insufficient resources, rejecting all runners", "requestedCount", count)
        return 0, nil
    }
}
```

## Test Plan

### resource_checker_test.go (fake clientset)

| Scenario | Expected |
|----------|----------|
| Sufficient CPU + memory | true |
| Sufficient CPU + memory + NPU | true |
| NPU insufficient | false |
| CPU insufficient | false |
| Memory insufficient | false |
| nodeSelector filters to sufficient nodes | true |
| nodeSelector filters to insufficient nodes | false |
| Pending pod causes insufficiency | false |
| Runner has no resources.requests | true (skip) |
| EphemeralRunnerSet not found | error → fail-open |

### scaler_test.go extensions (mock ResourceChecker)

| Scenario | Expected |
|----------|----------|
| checker returns false | HandleDesiredRunnerCount returns 0 |
| checker returns error | Normal scaling proceeds (fail-open) |
| checker is nil | Normal scaling, checker never called |
