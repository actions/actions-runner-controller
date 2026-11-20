# Cache volumes for runner scale sets

A runner scale set can give every runner a persistent, snapshot-backed cache volume. Build and dependency caches (Go build cache, npm/pnpm stores, cargo registry, Gradle caches, Docker layers, ...) then stay on a volume in their native format. Jobs read them at disk speed, with no archive to download or extract.

> **Status:** experimental. See [the ADR](../adrs/2026-11-20-runner-cache-volume.md) for the design and its limits.

## How it works

1. Every new runner gets its own `PersistentVolumeClaim`, restored from the newest snapshot of the scale set's cache. When there is no snapshot yet, the volume starts empty.
2. The runner pod gets a volume (named `cache` by default) backed by that claim.
3. When the runner finishes cleanly, ARC waits (up to 60 seconds) for the volume to be detached from the node and takes a `VolumeSnapshot` of it.
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

## Behavior to know about

- **One cache per scale set.** GitHub assigns a job to any idle runner of the scale set, so the volume is chosen before the job is known. Use separate scale sets for separate caches.
- **Last writer wins.** Runners that run concurrently each restore the snapshot that was newest when they were created and commit independently. The snapshot that becomes ready last is used by new runners.
- **Pre-created runners are not refreshed.** Runners created ahead of time (`minRunners`) restore when created and do not see later snapshots.
- **Only clean exits commit.** A runner that crashes, is killed, or fails to start never advances the cache. The runner phase is `Succeeded` whenever the runner exits cleanly, including for jobs whose steps failed.
- **Snapshots are best effort.** A failure to snapshot is logged and never blocks the runner or the job.
- **The cache is shared by every job of the scale set.** Anything a workflow writes to it is read by later workflows, including pull requests. Only use a cache volume on scale sets that run trusted workflows.

## Troubleshooting

- A runner pod stays `Pending` with an unbound claim: check the StorageClass, the snapshot it restores from (`kubectl get volumesnapshot -l actions.github.com/cache-lineage`), and the snapshot controller. A volume cannot be restored if the snapshot was deleted in the meantime.
- No snapshots appear: check the controller logs for `cache snapshot` messages and that the controller role can create `volumesnapshots` in the scale set namespace.
- Snapshots carry the annotations `actions.github.com/cache-source-runner`, `cache-job-repository`, `cache-job-workflow-ref` and `cache-workflow-run-id` that identify the job that produced them.
