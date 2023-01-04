# Using custom volumes

## Custom Volume mounts

You can configure your own custom volume mounts. For example to have the work/docker data in memory or on NVME SSD, for
i/o intensive builds. Other custom volume mounts should be possible as well, see [kubernetes documentation](https://kubernetes.io/docs/concepts/storage/volumes/)

### RAM Disk

Example how to place the runner work dir, docker sidecar and /tmp within the runner onto a ramdisk.
```yaml
kind: RunnerDeployment
spec:
  template:
    spec:
      dockerVolumeMounts:
        - mountPath: /var/lib/docker
          name: docker
      volumeMounts:
        - mountPath: /tmp
          name: tmp
      volumes:
        - name: docker
          emptyDir:
            medium: Memory
        - name: work # this volume gets automatically used up for the workdir
          emptyDir:
            medium: Memory
        - name: tmp
          emptyDir:
            medium: Memory
      ephemeral: true # recommended to not leak data between builds.
```

### NVME SSD

In this example we provide NVME backed storage for the workdir, docker sidecar and /tmp within the runner.
Here we use a working example on GKE, which will provide the NVME disk at /mnt/disks/ssd0.  We will be placing the respective volumes in subdirs here and in order to be able to run multiple runners we will use the pod name as a prefix for subdirectories. Also the disk will fill up over time and disk space will not be freed until the node is removed.

**Beware** that running these persistent backend volumes **leave data behind** between 2 different jobs on the workdir and `/tmp` with `ephemeral: false`.

```yaml
kind: RunnerDeployment
spec:
  template:
    spec:
      env:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      dockerVolumeMounts:
      - mountPath: /var/lib/docker
        name: docker
        subPathExpr: $(POD_NAME)-docker
      - mountPath: /runner/_work
        name: work
        subPathExpr: $(POD_NAME)-work
      volumeMounts:
      - mountPath: /runner/_work
        name: work
        subPathExpr: $(POD_NAME)-work
      - mountPath: /tmp
        name: tmp
        subPathExpr: $(POD_NAME)-tmp
      dockerEnv:
      - name: POD_NAME
        valueFrom:
          fieldRef:
            fieldPath: metadata.name
      volumes:
      - hostPath:
          path: /mnt/disks/ssd0
        name: docker
      - hostPath:
          path: /mnt/disks/ssd0
        name: work
      - hostPath:
          path: /mnt/disks/ssd0
        name: tmp
    ephemeral: true # VERY important. otherwise data inside the workdir and /tmp is not cleared between builds
```

### Docker image layers caching

> **Note**: Ensure that the volume mount is added to the container that is running the Docker daemon.

`docker` stores pulled and built image layers in the [daemon's (not client)](https://docs.docker.com/get-started/overview/#docker-architecture) [local storage area](https://docs.docker.com/storage/storagedriver/#sharing-promotes-smaller-images) which is usually at `/var/lib/docker`.

By leveraging RunnerSet's dynamic PV provisioning feature and your CSI driver, you can let ARC maintain a pool of PVs that are
reused across runner pods to retain `/var/lib/docker`.

_Be sure to add the volume mount to the container that is supposed to run the docker daemon._

_Be sure to trigger several workflow runs before checking if the cache is effective. ARC requires an `Available` PV to be reused for the new runner pod, and a PV becomes `Available` only after some time after the previous runner pod that was using the PV terminated. See [the related discussion](https://github.com/actions/actions-runner-controller/discussions/1605)._

By default, ARC creates a sidecar container named `docker` within the runner pod for running the docker daemon. In that case,
it's where you need the volume mount so that the manifest looks like:

```yaml
kind: RunnerSet
metadata:
  name: example
spec:
  template:
    spec:
      containers:
      - name: docker
        volumeMounts:
        - name: var-lib-docker
          mountPath: /var/lib/docker
  volumeClaimTemplates:
  - metadata:
      name: var-lib-docker
    spec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 10Mi
      storageClassName: var-lib-docker
```

With `dockerdWithinRunnerContainer: true`, you need to add the volume mount to the `runner` container.

### Go module and build caching

`Go` is known to cache builds under `$HOME/.cache/go-build` and downloaded modules under `$HOME/pkg/mod`.
The module cache dir can be customized by setting `GOMOD_CACHE` so by setting it to somewhere under `$HOME/.cache`,
we can have a single PV to host both build and module cache, which might improve Go module downloading and building time.

_Be sure to trigger several workflow runs before checking if the cache is effective. ARC requires an `Available` PV to be reused for the new runner pod, and a PV becomes `Available` only after some time after the previous runner pod that was using the PV terminated. See [the related discussion](https://github.com/actions/actions-runner-controller/discussions/1605)._

```yaml
kind: RunnerSet
metadata:
  name: example
spec:
  template:
    spec:
      containers:
      - name: runner
        env:
        - name: GOMODCACHE
          value: "/home/runner/.cache/go-mod"
        volumeMounts:
        - name: cache
          mountPath: "/home/runner/.cache"
  volumeClaimTemplates:
  - metadata:
      name: cache
    spec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 10Mi
      storageClassName: cache
```

### PV-backed runner work directory

ARC works by automatically creating runner pods for running [`actions/runner`](https://github.com/actions/runner) and [running `config.sh`](https://docs.github.com/en/actions/hosting-your-own-runners/adding-self-hosted-runners#adding-a-self-hosted-runner-to-a-repository) which you had to ran manually without ARC.

`config.sh` is the script provided by `actions/runner` to pre-configure the runner process before being started. One of the options provided by `config.sh` is `--work`,
which specifies the working directory where the runner runs your workflow jobs in.

The volume and the partition that hosts the work directory should have several or dozens of GBs free space that might be used by your workflow jobs.

By default, ARC uses `/runner/_work` as work directory, which is powered by Kubernetes's `emptyDir`. [`emptyDir` is usually backed by a directory created within a host's volume](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir), somewhere under `/var/lib/kuberntes/pods`. Therefore
your host's volume that is backing `/var/lib/kubernetes/pods` must have enough free space to serve all the concurrent runner pods that might be deployed onto your host at the same time.

So, in case you see a job failure seemingly due to "disk full", it's very likely you need to reconfigure your host to have more free space.

In case you can't rely on host's volume, consider using `RunnerSet` and backing the work directory with a ephemeral PV.

Kubernetes 1.23 or greater provides the support for [generic ephemeral volumes](https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes), which is designed to support this exact use-case. It's defined in the Pod spec API so it isn't currently available for `RunnerDeployment`. `RunnerSet` is based on Kubernetes' `StatefulSet` which mostly embeds the Pod spec under `spec.template.spec`, so there you go.

```yaml
kind: RunnerSet
metadata:
  name: example
spec:
  template:
    spec:
      containers:
      - name: runner
        volumeMounts:
        - mountPath: /runner/_work
          name: work
      - name: docker
        volumeMounts:
        - mountPath: /runner/_work
          name: work
      volumes:
      - name: work
        ephemeral:
          volumeClaimTemplate:
            spec:
              accessModes: [ "ReadWriteOnce" ]
              storageClassName: "runner-work-dir"
              resources:
                requests:
                  storage: 10Gi
```