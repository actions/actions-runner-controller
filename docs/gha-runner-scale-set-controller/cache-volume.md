# Cache volumes for runner scale sets

A runner scale set can give every runner a persistent, snapshot-backed cache volume. Build and dependency caches (Go build cache, npm/pnpm stores, cargo registry, Gradle caches, Docker layers, ...) then stay on a volume in their native format. Jobs read them at disk speed, with no archive to download or extract.

> **Status:** experimental. See [the ADR](../adrs/2026-11-20-runner-cache-volume.md) for the design and its limits.

## How it works

1. Every new runner gets its own `PersistentVolumeClaim`, restored from the newest snapshot of the scale set's cache. When there is no snapshot yet, the volume starts empty.
2. The runner pod gets a volume (named `cache` by default) backed by that claim.
3. When the runner finishes cleanly, ARC waits (up to 60 seconds) for the volume to be detached from the node, takes a `VolumeSnapshot` of it, and holds the runner for a moment until the snapshot controller has picked the request up and pinned the volume. It does not wait for the snapshot to be cut.
4. Once the new snapshot is ready, older snapshots of the scale set are deleted after a short grace period. Only the newest snapshot is kept.

Runners never share a live volume. A job's writes become visible to later runners only through the snapshot taken when its runner finishes.

## Requirements

- A StorageClass that supports `ReadWriteOnce` volumes, and a CSI driver with `VolumeSnapshot` support (the snapshot CRDs and the snapshot controller installed). ARC uses only core Kubernetes APIs, so any such storage works.
- `volumeBindingMode: WaitForFirstConsumer` is recommended, especially for zonal storage.
- Restores must work on the node a runner is scheduled to. Drivers whose snapshots are node-local do not work with autoscaling.
- Permissions for the controller to manage claims and snapshots in the scale set namespace. The `gha-runner-scale-set` chart adds them when `cacheVolume` is set. For snapshotting only after the volume is detached, the controller also needs to `list` `volumeattachments.storage.k8s.io` cluster-wide; the `gha-runner-scale-set-controller` chart grants this. Without it, ARC snapshots as soon as the runner pod is gone.

If the cluster has no `VolumeSnapshot` API, runners still start: the cache volume becomes an `emptyDir`, and the controller logs why.

## Configuration

With the `gha-runner-scale-set` chart:

```yaml
cacheVolume:
  storageClassName: fast-ssd
  size: 50Gi
  # volumeName: cache            # name of the volume added to the pod, defaults to "cache"
  # snapshotClassName: csi-snap  # defaults to the cluster default VolumeSnapshotClass

template:
  spec:
    containers:
      - name: runner
        image: ghcr.io/actions/actions-runner:latest
        command: ["/home/runner/run.sh"]
        env:
          - name: GOCACHE
            value: /cache/go-build
          - name: GOMODCACHE
            value: /cache/go-mod
          - name: npm_config_cache
            value: /cache/npm
        volumeMounts:
          - name: cache
            mountPath: /cache
```

ARC only adds the **volume**. Mount it where you need it by adding `volumeMounts` to the containers in `template.spec`. For example, in `dind` mode, mount it at `/var/lib/docker` in the `dind` container to keep Docker layers. Every runner has its own copy, so each Docker daemon owns its storage exclusively.

The size has to be at least the size of the volumes the snapshots were taken from. Changing `cacheVolume` rolls idle runners like any other change to the runner spec.

### Common cache locations

| Tool | Setting |
|---|---|
| Go | `GOCACHE=/cache/go-build`, `GOMODCACHE=/cache/go-mod` |
| npm | `npm_config_cache=/cache/npm` |
| pnpm | `PNPM_HOME=/cache/pnpm` or `npm_config_store_dir=/cache/pnpm-store` |
| Cargo | `CARGO_HOME=/cache/cargo` |
| Gradle | `GRADLE_USER_HOME=/cache/gradle` |
| pip | `PIP_CACHE_DIR=/cache/pip` |

Make sure the user running the job can write to the mount, for example with `securityContext.fsGroup`.

## A restore that cannot finish

A runner never stays blocked because of the cache. If the volume restored from the snapshot is not provisioned, for example because the snapshot is unusable or the storage backend keeps failing, the runner waits for `restoreTimeout` and then starts with an empty volume instead:

```yaml
cacheVolume:
  restoreTimeout: 5m   # default; 0 waits forever
```

The wait starts when the cluster begins provisioning the volume, not when the pod is created. With `volumeBindingMode: WaitForFirstConsumer` a volume stays unbound while the pod waits for capacity, and that is not counted. When the timeout is exceeded the controller deletes the pod and its claim, creates the claim again without the snapshot, and records a `CacheRestoreTimedOut` event on the runner. That runner never tries to restore again, so it starts cold. Its volume is saved as usual, which replaces the snapshot that could not be restored. Set the timeout above the longest restore you expect: a restore that is only slow, not stuck, is abandoned too.

## Protecting the cache from untrusted workflows

By default every runner that exits cleanly saves its volume, so whatever a workflow writes to the cache is read by later workflows, including pull requests. If some workflows are not fully trusted, add a `commitPolicy`. Runners that do not match it still start from the cache, but their changes are discarded:

```yaml
cacheVolume:
  storageClassName: fast-ssd
  size: 50Gi
  commitPolicy:
    repositories: ["my-org/*"]
    events: ["push", "schedule", "workflow_dispatch"]
    refs: ["refs/heads/main"]
```

- `repositories`: patterns for the `owner/repo` that runs the job.
- `events`: names of the events that triggered the workflow, compared exactly. Use this to keep `pull_request` and `pull_request_target` jobs out. The ref alone cannot do that, because a `pull_request_target` job runs the workflow from the base branch ref.
- `refs`: patterns for the ref of the workflow, as GitHub reports it in the job workflow reference. For a workflow that calls a reusable workflow, check what your workflows report before relying on it.

Patterns use `path.Match` syntax, where `*` does not match `/`, so `refs/heads/*` does not match `refs/heads/release/1`. Every list that is set has to match. The policy **fails closed**: if a list is set and the job's property is unknown (the runner never got a job, or the listener did not record it), the runner does not commit. Skipped commits are logged by the controller. The listener has to be on a version that records `status.jobEventName` for `events` to work.

A policy only protects what is written. Anything that is already in the cache is still read by every job, so do not share a scale set between workflows with different trust levels if the cache could expose secrets.

## Limiting how long a cache lives

A cache only grows: every runner starts from the previous cache and adds to it, until the volume is full and jobs fail with "no space left on device". Two limits bound that, and both have defaults:

```yaml
cacheVolume:
  retention:
    maxAge: 336h      # default: after 14 days a new cache history starts from an empty volume
    idleTimeout: 168h # default: a snapshot nobody restores from or replaces is deleted after 7 days
```

- `maxAge` is measured from when the current history started from an empty volume. It is inherited by every snapshot of the history, so it applies even to a cache that is used constantly. When it is exceeded, new runners start empty (a cold build) and the old snapshot is deleted. Pick a value that gives a cold build at an acceptable frequency.
- `idleTimeout` deletes a snapshot that no runner restored from and no runner replaced. It stops a scale set that no longer runs jobs from keeping its storage forever. Deleting it needs no new commit: the snapshot is removed by the controller.
- A duration of `0` disables a limit. Retention does not size the volume: choose `size` for the largest cache you expect between resets.

## Metrics and events

With metrics enabled the controller exports, per scale set (`namespace`, `scale_set`):

| Metric | Meaning |
|---|---|
| `gha_controller_cache_restores_total{result}` | Cache volumes created: `hit` (restored), `cold` (no snapshot), `expired` (retention), `timeout` (gave up restoring), `unavailable` (no snapshot API) |
| `gha_controller_cache_commits_total{result}` | Finished runners considered for saving: `requested`, `denied` (commit policy), `failed` |
| `gha_controller_cache_snapshots_deleted_total{reason}` | Snapshots deleted by garbage collection: `superseded`, `expired`, `failed` |
| `gha_controller_cache_snapshots{state}` | Snapshots of the scale set: `ready` or `pending` |
| `gha_controller_cache_snapshot_age_seconds` | Age of the newest ready snapshot, absent while there is none |

A low `hit` share, a growing `timeout` count, or a snapshot age that keeps growing while jobs run (nothing commits) are the signals to look at first.

Events on the `EphemeralRunner` report the decisions that otherwise only appear in logs: `CacheRestoreTimedOut`, `CacheNotSaved` (commit policy), `CacheCommitFailed` and `CacheUnavailable` (no snapshot API). The controller needs permission to create events (`events.k8s.io`) in the scale set namespace; the chart grants it when `cacheVolume` is set.

## Behavior to know about

- **One cache per scale set.** GitHub assigns a job to any idle runner of the scale set, so the volume is chosen before the job is known. Use separate scale sets for separate caches.
- **Last writer wins.** Runners that run concurrently each restore the snapshot that was newest when they were created and commit independently. The snapshot that becomes ready last is used by new runners.
- **Pre-created runners are not refreshed.** Runners created ahead of time (`minRunners`) restore when created and do not see later snapshots.
- **Only clean exits commit.** A runner that crashes, is killed, or fails to start never advances the cache. The runner phase is `Succeeded` whenever the runner exits cleanly, including for jobs whose steps failed.
- **Snapshots are best effort.** A failure to snapshot is logged and never blocks the runner or the job.
- **The cache is shared by every job of the scale set.** Without a `commitPolicy`, anything a workflow writes to it is read by later workflows, including pull requests. Set a policy, or only use a cache volume on scale sets that run trusted workflows.

## Troubleshooting

- A runner pod stays `Pending` with an unbound claim: check the StorageClass, the snapshot it restores from (`kubectl get volumesnapshot -l actions.github.com/cache-lineage`), and the snapshot controller. After `restoreTimeout` the runner starts with an empty volume on its own and a `CacheRestoreTimedOut` event is recorded.
- No snapshots appear: check the controller logs for `cache snapshot` messages and that the controller role can create `volumesnapshots` in the scale set namespace.
- Snapshots carry the annotations `actions.github.com/cache-source-runner`, `cache-job-repository`, `cache-job-workflow-ref`, `cache-job-event` and `cache-workflow-run-id` that identify the job that produced them, and `cache-chain-started-at` and `cache-last-used-at`, which retention uses.
- Jobs start cold when you expect a warm cache: look for `has expired, starting from an empty volume` in the controller log, and check `retention`.
