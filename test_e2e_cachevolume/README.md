# Cache volume end-to-end test

Runs the runner cache volume feature against a real kind cluster with a CSI driver that supports
snapshots. The real `EphemeralRunner` and cache snapshot controllers run inside the test process; only
the GitHub Actions service is faked, and runner pods are busybox containers.

The test restores and commits a cache over three sequential jobs, checks that each job sees the previous
job's data, that garbage collection keeps only the newest snapshot, that concurrent jobs end with exactly
one winner, and that claims of finished runners are removed. It logs timings for each step.

It is skipped unless `ARC_CACHE_E2E=1` and refuses to run unless the current kubeconfig context starts
with `kind-`.

```shell
kind create cluster --name arc-cache --kubeconfig /tmp/arc-cache.kubeconfig
export KUBECONFIG=/tmp/arc-cache.kubeconfig

# VolumeSnapshot CRDs and snapshot controller (kubernetes-csi/external-snapshotter), then
# kubernetes-csi/csi-driver-host-path (deploy/kubernetes-latest/deploy.sh).
# Add a StorageClass for hostpath.csi.k8s.io (volumeBindingMode: WaitForFirstConsumer)
# named csi-hostpath-sc, and make its VolumeSnapshotClass the default.

kubectl apply --server-side -f config/crd/bases/actions.github.com_*.yaml
ARC_CACHE_E2E=1 go test ./test_e2e_cachevolume/ -v -count=1 -timeout 25m
```

`ARC_CACHE_E2E_STORAGE_CLASS` selects another StorageClass, and `ARC_CACHE_E2E_KEEP=1` keeps the test
namespace for debugging. Note that `csi-driver-host-path` snapshots are node-local, so use a single node.
