# Add snapshot-backed cache volumes to runner scale sets

**Status**: Proposed

## Context

Runner pods created by a runner scale set are ephemeral, so every job starts with a cold filesystem. Build and dependency caches (Go build cache, npm/pnpm stores, cargo registry, Gradle caches, `RUNNER_TOOL_CACHE`, Docker layers on the dind sidecar) must be rebuilt or fetched over the network on every job. `actions/cache` helps but serializes the cache into archives that are uploaded and downloaded on every run, which becomes slow and costly for large caches.

Hosted runner vendors (Blacksmith sticky disks, Namespace cache volumes, RunsOn sticky disks) solve this by giving each job its **own writable clone of the most recent cache disk**, and by turning the finished job's disk into the new "latest" snapshot. Kubernetes already has every primitive needed for this: CSI `VolumeSnapshot`, PVC `dataSource`, and any CSI driver that supports snapshots. What is missing is the lifecycle around them. Users ask for it in [#3966](https://github.com/actions/actions-runner-controller/issues/3966) (PVC from snapshot) and [#3244](https://github.com/actions/actions-runner-controller/issues/3244) (cache to disk), and today can only approximate it with a statically named snapshot in `template.spec.volumes`, which never advances.

Constraints specific to ARC that shape the design:

- GitHub assigns a pending job to *any* idle runner of the scale set, and job information reaches `EphemeralRunner.status` only after the pod runs. The volume must therefore be chosen at pod creation time, without knowing the job. A cache is scoped to a runner scale set, not to a workflow-chosen key.
- ARC deletes a terminated runner pod immediately (`TerminatedPodGracePeriodSeconds` defaults to 0). A pod-scoped generic ephemeral volume would be destroyed before it could be snapshotted, so the claim must outlive the pod and be owned by the `EphemeralRunner`.
- An `EphemeralRunner` counts against the scale set's capacity. Finishing a runner must not wait for a snapshot to become `readyToUse`, which can take minutes on some storage backends.
- Kubernetes pods cannot hot-attach a volume after start, so workflow-level cache keys (as hosted vendors offer) are out of scope.

## Decision

Add an optional, off-by-default `cacheVolume` field to the scale set, with a controller-managed restore/commit/garbage-collect loop built only on core Kubernetes APIs (no storage-vendor specific behaviour).

### API

`AutoscalingRunnerSet.spec.cacheVolume`, propagated unchanged to `EphemeralRunnerSet.spec.ephemeralRunnerSpec.cacheVolume` and `EphemeralRunner.spec.cacheVolume`:

```yaml
cacheVolume:
  volumeName: cache            # name of the volume injected into the runner pod; default "cache"
  storageClassName: fast-ssd   # required; must support ReadWriteOnce and CSI snapshots
  size: 50Gi                   # required
  snapshotClassName: my-class  # optional; cluster default VolumeSnapshotClass when unset
```

ARC injects only the *volume*. Users mount it where they need it by adding `volumeMounts` to the containers of `template.spec` (for example `/home/runner/_cache` in the runner container, `/var/lib/docker` in the dind sidecar). The Helm chart exposes the same block and adds the namespaced RBAC it needs (PVCs, `volumesnapshots`, `volumeattachments` read) only when the feature is enabled.

### Lineage

The lineage is the `AutoscalingRunnerSet` (namespace and name). Snapshots carry the label `actions.github.com/cache-lineage=<hash>` and an owner reference to the `AutoscalingRunnerSet`. The "latest" snapshot is the newest `readyToUse` snapshot of the lineage, ordered by `status.creationTime`; there is no separate pointer to keep in sync, so the loop recovers from controller restarts and races on its own.

### Restore

Before creating the runner pod, the `EphemeralRunner` controller ensures a PVC `<runner-name>-cache` owned by the `EphemeralRunner`, restored from the latest snapshot via `spec.dataSource`. With no snapshot yet, an empty volume is provisioned (cold start). The pod mounts the claim through `persistentVolumeClaim.claimName`. A recreated pod reuses the claim.

### Commit

When an `EphemeralRunner` finishes in phase `Succeeded` (the runner container exited cleanly, which includes jobs that failed their steps), the controller creates a `VolumeSnapshot` of its claim. The snapshot name is derived from the runner name, so creation is idempotent. Before snapshotting, the controller waits a bounded time (default 60 seconds) for the `VolumeAttachment` of the volume to disappear, so that the snapshot is taken from a detached, cleanly unmounted filesystem; drivers without attachments skip the wait. The controller does **not** wait for `readyToUse`: the snapshot controller's as-source protection keeps the PVC alive until the snapshot is cut, so runner cleanup is not delayed. Runners that fail, are killed, or are deleted with a job assigned never commit.

### Garbage collection

When a lineage snapshot becomes `readyToUse`, the `AutoscalingRunnerSet` controller deletes all older snapshots of that lineage, keeping the newest ready one and any newer snapshots still in progress. Snapshots that report an error are deleted after a grace period. Snapshot-as-source protection delays deletion of a snapshot that is still being restored from, without affecting correctness.

### Semantics

- Concurrent runners restore from the snapshot that is newest when they are created and commit independently; the last snapshot to become ready wins (matching hosted vendors).
- Runners created ahead of time (`minRunners`) restore at creation and do not see later commits.
- Cache problems never fail a job: a missing snapshot means a cold start.
- The feature is inert unless `cacheVolume` is set. If the `VolumeSnapshot` CRD is not installed, ARC reports a clear condition/event instead of failing.

### Scope

This ADR covers only **runner scale sets** (`AutoscalingRunnerSet`, `EphemeralRunnerSet`, `EphemeralRunner` in `actions.github.com`, installed by the `gha-runner-scale-set` chart). The legacy `actions.summerwind.net` resources (`RunnerDeployment`, `RunnerSet`, `HorizontalRunnerAutoscaler`) are not changed.

### Out of scope (follow-ups)

Commit filtering by ref/workflow (poisoning protection for less trusted organizations), free-space guard, idle expiry and retain-on-delete, multiple caches per scale set, metrics, `containerMode: kubernetes` (job pods are separate from the runner pod), and transparent `actions/cache` acceleration (requires a patched runner).

### Alternatives considered

- **Pod-scoped generic ephemeral volume in `template.spec.volumes`**: restores fine today, but the claim is destroyed with the pod before it can be snapshotted, and `dataSource.name` cannot follow a rotating "latest".
- **Volume populator (`dataSourceRef` → custom resource)**: resolves "latest" without ARC changes but adds a temporary PVC and PV rebind to every runner start, plus a new CRD and controller.
- **Admission mutation (webhook / `MutatingAdmissionPolicy`) to rewrite `dataSource`**: adds an availability dependency on the admission path (or requires Kubernetes 1.36+) and still needs a separate commit mechanism.
- **Separate companion controller**: viable fallback, but duplicates ARC's runner lifecycle knowledge (when a runner is done, when its pod is gone) and cannot easily own the claim.
- **Object-store `actions/cache` proxy**: a different, complementary approach; needs a patched runner because the runner overwrites `ACTIONS_RESULTS_URL` before every action.

## Consequences

- Jobs on a scale set start with a warm, native-format cache and no archive download or extraction; large caches stop dominating job time.
- Network traffic and object-store request costs for caches drop; the cost moves to PV and snapshot storage, bounded by `size` and one retained snapshot per scale set.
- Runner start-up gains the latency of provisioning a volume from a snapshot; this is storage-dependent and should be measured per backend (for example lazily restored cloud volumes may be slow on first read).
- Requires a CSI driver with snapshot support, the snapshot CRDs/controller, and a restore that works on the node the runner is scheduled to; drivers with node-local snapshots are unsupported, and `WaitForFirstConsumer` binding is recommended for zonal storage.
- Cache contents are shared by every job on the scale set. Operators who run untrusted workflows must use separate scale sets (or wait for commit filtering) to avoid cache poisoning.
- ARC gains a new optional dependency on the snapshot API group, three optional RBAC rules, and a new field in three CRDs; because runner drift detection compares the whole `EphemeralRunnerSpec`, changing `cacheVolume` rolls idle runners like any other spec change.
