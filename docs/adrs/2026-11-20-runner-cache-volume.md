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
  commitPolicy:                # optional; without it every runner that exits cleanly commits
    repositories: ["my-org/*"]
    events: ["push", "schedule", "workflow_dispatch"]
    refs: ["refs/heads/main"]
  retention:                   # optional; these are the defaults
    maxAge: 336h
    idleTimeout: 168h
  restoreTimeout: 5m           # optional; how long a runner waits for its restored volume
```

ARC injects only the *volume*. Users mount it where they need it by adding `volumeMounts` to the containers of `template.spec` (for example `/home/runner/_cache` in the runner container, `/var/lib/docker` in the dind sidecar). The Helm chart exposes the same block and adds the namespaced RBAC it needs (PVCs and `volumesnapshots`) only when the feature is enabled. Waiting for the volume to be detached additionally needs cluster-wide `list` on `volumeattachments`, which the controller chart grants (it is optional: without it the controller only waits for the pod to be gone).

### Lineage

The lineage is the `AutoscalingRunnerSet` (namespace and name). Snapshots carry the label `actions.github.com/cache-lineage=<hash>` and an owner reference to the `AutoscalingRunnerSet`. The "latest" snapshot is the newest `readyToUse` snapshot of the lineage, ordered by `status.creationTime`; there is no separate pointer to keep in sync, so the loop recovers from controller restarts and races on its own.

### Restore

Before creating the runner pod, the `EphemeralRunner` controller ensures a PVC `<runner-name>-cache` owned by the `EphemeralRunner`, restored from the latest snapshot via `spec.dataSource`. With no snapshot yet, an empty volume is provisioned (cold start). The pod mounts the claim through `persistentVolumeClaim.claimName`. A recreated pod reuses the claim. Snapshots and claims are read with the uncached API reader, so the feature starts no new cluster-wide informers. If the `VolumeSnapshot` API is not served by the cluster, the pod gets an `emptyDir` with the same volume name instead, so runners still start.

### Commit

When an `EphemeralRunner` that finishes in phase `Succeeded` (the runner container exited cleanly, which includes jobs that failed their steps) is deleted, the controller creates a `VolumeSnapshot` of its claim after cleaning up the pod. The snapshot name is derived from the runner name, so creation is idempotent. Before snapshotting, the controller waits a bounded time (60 seconds) for the pod to be gone and for the `VolumeAttachment` of the volume to disappear, so that the snapshot is taken from a detached, cleanly unmounted filesystem; if the controller may not list volume attachments, or the driver has none, only the pod is waited for. The controller does **not** wait for the snapshot to be cut (`readyToUse`), which can take minutes, but it does wait until the snapshot controller has picked the request up (`status.boundVolumeSnapshotContentName` is set, normally well under a second). Picking the request up pins the claim with the snapshot controller's as-source-protection finalizer, which holds it until the snapshot is cut. This wait is required: the claim is owned by the `EphemeralRunner`, so releasing the runner right after creating the `VolumeSnapshot` lets garbage collection delete the claim first, and the snapshot controller then fails the snapshot permanently ("cannot add finalizer on claim because it is being deleted"). This was observed on a real cluster. Snapshotting is best effort: a failure is logged and never blocks the runner's deletion, so it cannot hold scale set capacity. Runners that fail, are killed, or are deleted with a job assigned never commit.

### Garbage collection

A dedicated reconciler, driven by the `AutoscalingRunnerSet`, lists the lineage's snapshots every 30 seconds with the uncached API reader (it polls instead of watching, so enabling the feature needs only namespaced permissions and no `VolumeSnapshot` informer). Snapshots that are not the newest ready one are marked with a `superseded-at` annotation the first time they are seen replaced, and deleted once a one-minute grace period has passed; this covers runners that chose the old snapshot as their restore source just before its replacement became ready. The newest ready snapshot and snapshots still being taken are never deleted; snapshots that report an error are deleted after ten minutes. Snapshot-as-source protection delays deletion of a snapshot that is still being restored from, without affecting correctness.

### Restore timeout

A cache must never keep a runner from starting. If the claim restored from a snapshot is not bound for `restoreTimeout` (default 5 minutes, `0` waits forever) while the runner's pod has not started, the controller deletes the pod and the claim, marks the runner with an annotation, and creates the claim again without the snapshot, so the runner starts with an empty volume. The annotation makes the choice stick for that runner, and the new volume is saved normally, which replaces the snapshot that could not be restored. The clock starts when the cluster begins provisioning the volume (the claim carries the `storage-provisioner` annotation), and is recorded on the runner, because with `WaitForFirstConsumer` a claim is unbound for as long as its pod waits for capacity and counting that would discard good caches. A slow restore is indistinguishable from a stuck one, so the timeout has to exceed the slowest restore expected.

### Observability

The controller exports counters for how cache volumes were initialised (hit, cold, expired, timeout, unavailable), for commits (requested, denied, failed) and for snapshots deleted by garbage collection and why, and gauges for the number of snapshots and the age of the newest ready one. Decisions that would otherwise only be in logs (restore timeout, commit refused by the policy, failed commit, missing snapshot API) are also recorded as Kubernetes events on the runner, which needs `create` and `patch` on `events.k8s.io` events in the scale set namespace.

### Commit policy

All jobs of a scale set share one cache, so a job that writes something harmful to it affects every later job. `commitPolicy` restricts which finished runners may advance the cache: allow-lists of repositories, workflow refs and trigger events, all of which must match. Runners that do not match still restore from the cache and run normally, but their volume is discarded, so the job is never failed because of it. The policy is evaluated when the runner finishes, from the job information the listener records in the `EphemeralRunner` status. Only restoring has to happen before the job is known, which is why a repository- or job-specific *restore* is not possible but a restricted *commit* is.

It fails closed: when a list is set and the matching job property is unknown, the runner does not commit. The listener now also records `status.jobEventName`, which is needed because the ref alone cannot distinguish a `pull_request_target` job (which runs the base branch workflow) from a push. The `refs` list is matched against the ref of the workflow as GitHub reports it in the job workflow reference.

### Retention

Every runner starts from the previous cache and adds to it, so without a limit a cache only grows, until the fixed-size volume is full and jobs fail. Two limits are enforced, both configurable and enabled by default:

- `maxAge`: the time since the current history of snapshots started from an empty volume. Every snapshot inherits the start of its history (annotation `cache-chain-started-at`), so a constantly used cache still reaches it. Once it is exceeded, runners ignore the snapshot and start from an empty volume, which begins a new history, and garbage collection deletes the old snapshot. The price is a cold build every `maxAge`.
- `idleTimeout`: a snapshot that no runner restored from (annotation `cache-last-used-at`, refreshed at most hourly) and no commit replaced is deleted, so a scale set that no longer runs jobs does not keep its storage.

Both are decided from annotations on the snapshots, so they survive controller restarts. The defaults (14 and 7 days) are a judgement, not measurements, and can be disabled with `0`. They do not replace sizing the volume for the largest cache expected between resets, and a free-space guard remains a follow-up.

### Semantics

- Concurrent runners restore from the snapshot that is newest when they are created and commit independently; the last snapshot to become ready wins (matching hosted vendors).
- Runners created ahead of time (`minRunners`) restore at creation and do not see later commits.
- Cache problems never fail a job: a missing snapshot means a cold start.
- The feature is inert unless `cacheVolume` is set. If the `VolumeSnapshot` CRD is not installed, ARC reports a clear condition/event instead of failing.

### Scope

This ADR covers only **runner scale sets** (`AutoscalingRunnerSet`, `EphemeralRunnerSet`, `EphemeralRunner` in `actions.github.com`, installed by the `gha-runner-scale-set` chart). The legacy `actions.summerwind.net` resources (`RunnerDeployment`, `RunnerSet`, `HorizontalRunnerAutoscaler`) are not changed.

### Out of scope (follow-ups)

Free-space guard, retain-on-delete, multiple caches per scale set, `containerMode: kubernetes` (job pods are separate from the runner pod), and transparent `actions/cache` acceleration (requires a patched runner).

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
- Cache contents are shared by every job on the scale set. Operators who run untrusted workflows must restrict commits with `commitPolicy` or use separate scale sets; the policy protects what is written, not what is read.
- Caches are bounded in time by default: a cold build every `maxAge` is the price for bounded growth, and idle caches disappear after `idleTimeout`.
- ARC gains a new optional dependency on the snapshot API group, three optional RBAC rules, and a new field in three CRDs; because runner drift detection compares the whole `EphemeralRunnerSpec`, changing `cacheVolume` rolls idle runners like any other spec change.
