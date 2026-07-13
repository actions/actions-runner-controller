# Upgrading gha-runner-scale-set ARC and patching a CVE

This guide covers the GitHub-supported Autoscaling Runner Scale Sets charts:
`gha-runner-scale-set` and `gha-runner-scale-set-controller`. It uses an upgrade
from 0.12.0 to 0.14.2 as the example.

It does not apply to the legacy community ("summerwind") ARC.

## Short answer

- Do not upgrade only the controller image across a minor release. A 0.14.x
  controller deletes runner scale sets still labeled 0.12.0.
- If the CVE fix only changes the Go toolchain, rebuild the 0.12.0 image with
  the fixed toolchain. This removes the CVE without changing ARC APIs, CRDs,
  charts, or runner pods.
- For a full 0.12 to 0.14 upgrade, use the documented uninstall and reinstall
  process. Helm does not upgrade CRDs.
- If you need continuous job assignment during the full upgrade, use two
  clusters and upgrade them one at a time.

## Why this upgrade needs care

The controller chart installs the controller Deployment and the CRDs. The
runner-scale-set chart creates an `AutoscalingRunnerSet` (ARS). Most
installations have one controller release and several runner-scale-set
releases.

The controller image also runs the listener. When ARC creates a listener, it
copies the controller image into `AutoscalingListener.Spec.Image`
(`controllers/actions.github.com/resourcebuilder.go`). Existing listeners do
not pick up a new controller image automatically because `ListenerSpecHash`
does not include the image.

Runner pods use a separate image, usually
`ghcr.io/actions/actions-runner`. Rebuilding the controller does not patch or
roll the runner image.

ARC also enforces version compatibility. The runner chart labels each ARS with
its chart `appVersion`
(`charts/gha-runner-scale-set/templates/_helpers.tpl`). The controller compares
that label with its compiled `build.Version` through `IsVersionAllowed`
(`apis/actions.github.com/v1alpha1/version.go`). It allows an exact match,
`dev`, `canary-*`, or the same major and minor. Otherwise, the controller
deletes the ARS near the start of reconciliation
(`autoscalingrunnerset_controller.go`).

`build.Version` is compiled into `manager` and `ghalistener` through the
Dockerfile linker flag. Helm cannot change it at runtime. This distinction
between an image tag and the compiled version is critical in the patch
playbook.

## Research the release before choosing an upgrade

A version number does not show the operational impact of a release. Review the
whole release range, then isolate the change that fixes the CVE.

### Read every release note in the range

An upgrade from 0.12.0 to 0.14.2 crosses 0.12.1, 0.13.0, 0.13.1, 0.14.0, and
0.14.1. A breaking change may be in any of them. Check the GitHub Releases page
and the release notes in the repository. Scale-set tags use the format
`gha-runner-scale-set-<version>`.

### Diff the paths that affect upgrade safety

```bash
git fetch --tags
FROM=gha-runner-scale-set-0.12.0
TO=gha-runner-scale-set-0.14.2

# Review commits and PR titles across the full range.
git log --oneline "$FROM".."$TO"

# Helm cannot upgrade changes in this directory.
git diff "$FROM".."$TO" -- charts/gha-runner-scale-set-controller/crds

# Review values, rendered workloads, and RBAC.
git diff "$FROM".."$TO" -- \
  charts/gha-runner-scale-set \
  charts/gha-runner-scale-set-controller

# Check the image build and toolchain.
git diff "$FROM".."$TO" -- Dockerfile

# Check API types, compatibility checks, and controller behavior.
git diff "$FROM".."$TO" -- \
  apis/actions.github.com \
  controllers/actions.github.com
```

Classify the results before deciding what to deploy:

| Change | What it means |
| --- | --- |
| Build toolchain only | No intended ARC behavior change. Rebuilding the current source may be enough. |
| Runtime base image | Runtime libraries and image behavior may change. Pin, scan, and test the new digest. |
| CRD schema | A full upgrade needs explicit CRD handling. Check for new, removed, or pruned fields. |
| Chart, values, or RBAC | Render and compare the manifests. Workloads or permissions may change. |
| Controller, listener, or API client | Test in staging because ARC's runtime behavior may change. |

The full release range and the security fix are separate questions. The
0.12-to-0.14 range contains CRD, chart, and controller changes, so a full
upgrade is operationally significant. The 0.14.2 security fix itself updated
the Go builder. Backporting that toolchain change to the 0.12.0 source removes
the CVE without taking the other 0.14 changes.

Confirm this before relying on the patch:

1. Check that the security release attributes the fix to the Go toolchain.
2. Inspect the security-fix commit and the Dockerfile diff.
3. Rebuild and scan the result. Run `govulncheck` if it is part of your
   security process.

If the CVE is in a Go module, the runner image, or the runtime base image,
patch that component instead.

## Upgrade risks, especially CRDs

1. **Helm does not upgrade or delete CRDs.** It installs files from a chart's
   `crds/` directory only when the CRDs do not already exist. A controller
   chart upgrade leaves the old definitions in place.

2. **Old CRDs can silently remove new fields.** These CRDs use structural
   schemas with pruning. A 0.14 chart applied against 0.12 CRDs may lose fields
   the old schema does not recognize. Client-side validation may reject other
   fields instead. Both outcomes are possible: a hard error or a feature that
   quietly does nothing.

3. **Deleting a CRD deletes all of its custom resources.** This includes every
   `AutoscalingRunnerSet`, `EphemeralRunnerSet`, `EphemeralRunner`, and
   `AutoscalingListener` in the cluster. Never use `kubectl replace --force`
   on these CRDs because it deletes and recreates them.

4. **Finalizers can delay an uninstall.** ARC custom resources have
   type-specific finalizers. Chart-managed Secrets, Roles, RoleBindings, and
   ServiceAccounts use `actions.github.com/cleanup-protection`. Cleanup also
   calls the GitHub API. Keep the authentication secret available until all
   ARC resources are gone.

5. **The 0.12-to-0.14 CRD changes are additive but still need handling.** All
   four CRDs remain on `v1alpha1`; there is no storage-version migration or
   conversion webhook. Most of the diff comes from regenerated
   `PodTemplateSpec` schemas and new fields such as `ResourceMeta`,
   `runnerScaleSetLabels`, and `kubernetes-novolume`.

6. **Other changes can affect workloads.** The 0.14.0 listener uses the
   `actions/scaleset` client. The dind template changed and can roll runner
   pods. Names containing underscores change because 0.14 replaces `_` with
   `-`. Kubernetes-mode RBAC also changed. Test these paths in staging.

## Why a controller-only minor upgrade is unsafe

Do not run a 0.14.2 controller against 0.12.0 runner-scale-set releases.

The runner chart labels each ARS with
`app.kubernetes.io/version: 0.12.0`. A 0.14.2 controller evaluates
`IsVersionAllowed("0.12.0", "0.14.2")` as false and deletes the ARS. That
deletion cascades to its listener and runner resources.

With plain Helm, the ARS stays deleted until the runner release is reconciled
again. With Flux or Argo CD, the GitOps controller may recreate the 0.12.0 ARS
and ARC may delete it again, creating a loop that never stabilizes.

Patch releases within the same minor are allowed. A jump from 0.12 to 0.14 is
not. A full upgrade must move the controller and every runner-scale-set release
to the same minor and handle the CRDs separately.

## Emergency CVE patch playbook

Use this playbook only after confirming that the CVE is fixed by rebuilding the
controller with a newer Go toolchain. The source, chart, APIs, CRDs, and
compiled ARC version remain at 0.12.0.

This is a self-supported bridge to a later full upgrade, not a replacement for
staying current.

### 1. Confirm where the CVE lives

Scan the exact image digest running in the cluster, not only its tag:

```bash
trivy image \
  ghcr.io/actions/gha-runner-scale-set-controller@sha256:<live-digest>

# Scan the runner and any custom dind, init, or sidecar images too.
trivy image ghcr.io/actions/actions-runner@sha256:<live-digest>
```

Use the result to choose the fix:

- Go standard library or toolchain in the controller binaries: continue with
  this playbook.
- Go module dependency: update the module in the 0.12.0 source and rebuild.
- Runner image: update the runner image instead.
- Distroless base: update and pin the base image digest.

### 2. Inventory the installation

```bash
helm list -A
kubectl get \
  autoscalingrunnersets,ephemeralrunnersets,ephemeralrunners,autoscalinglisteners \
  -A

kubectl -n <controller-ns> get deploy <controller> \
  -o jsonpath='{.spec.template.spec.containers[0].args}'
```

Record the controller and runner release versions, namespaces,
`containerMode`, custom RBAC or PVCs, and scale set names. Check every
`listenerTemplate` for a pinned listener image or `imagePullSecrets`; those
values override the image and pull secrets inherited from the controller.

Also note the controller's update strategy. The default is `immediate`.
`eventual` changes how long listener recreation can take in step 7.

### 3. Check out the exact source

```bash
git fetch --tags
git checkout --detach gha-runner-scale-set-0.12.0
```

### 4. Update the Go toolchain

Use the fixed Go release named in the advisory. Pin both builder and runtime
images by digest so the rebuild is reproducible.

```diff
-FROM --platform=$BUILDPLATFORM golang:1.24.3 AS builder
+FROM --platform=$BUILDPLATFORM golang:1.26.3@sha256:<pinned-digest> AS builder
 ...
-FROM gcr.io/distroless/static:nonroot
+FROM gcr.io/distroless/static:nonroot@sha256:<pinned-digest>
```

Do not refresh unrelated dependencies unless the CVE requires it.

### 5. Build, scan, and sign the image

Build for every architecture used by your nodes. Stamp the compiled version
with `VERSION=0.12.0`. The registry tag may include the CVE number, but the
compiled `VERSION` is what the compatibility guard reads.

```bash
docker buildx build \
  --pull \
  --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=0.12.0 \
  --build-arg COMMIT_SHA="$(git rev-parse HEAD)" \
  -t <registry>/gha-runner-scale-set-controller:0.12.0-cveNNNN \
  --push .
```

`IsVersionAllowed("0.12.0", "0.12.0")` is an exact match. Do not rely on the
image tag to set `build.Version`; Helm cannot correct a bad compiled value.

Before deployment:

- Scan both architecture manifests.
- Confirm the finding is gone from `manager`, `ghalistener`,
  `github-webhook-server`, `actions-metrics-server`, and `sleep`.
- Sign and attest the immutable manifest digest.
- Keep the old image available for rollback.

For a private registry, create the pull secret in the controller's effective
namespace and attach it through the controller chart's `imagePullSecrets`
value. Existing listeners keep their current `spec.imagePullSecrets` until
they are recreated. A listener template can override them.

### 6. Roll the controller

Keep the controller chart on 0.12.0 and change only the image:

```bash
helm upgrade <controller-release> \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller \
  --version 0.12.0 \
  -n <helm-release-ns> \
  --reuse-values \
  --set image.repository=<registry>/gha-runner-scale-set-controller \
  --set-string image.tag=0.12.0-cveNNNN \
  --wait --timeout 5m
```

Use `--atomic` with Helm 3 or `--rollback-on-failure` with Helm 4 if that
matches your deployment policy.

Under GitOps, update the managed values or suspend reconciliation first.
Manual Helm changes will otherwise be reverted.

ARC 0.12 has no manager liveness or readiness probes, so Helm success is not
enough. Check the resolved image ID:

```bash
kubectl -n <controller-ns> get pods \
  -l app.kubernetes.io/instance=<controller-release> \
  -o jsonpath='{range .items[*]}{.metadata.name}{"  "}{.status.containerStatuses[0].imageID}{"\n"}{end}'
```

Runner pods are not rolled because their image and ARS spec did not change.

### 7. Update the listeners

Changing the controller image does not update existing listeners. Their image
is stored in `AutoscalingListener.Spec.Image`, and the listener hash excludes
that field.

Update one scale set at a time with one of these methods.

#### Recreate the listener resource

```bash
kubectl -n <controller-ns> delete autoscalinglistener <listener-name>
```

ARC removes and recreates the listener pod, secrets, and RBAC. With the default
`immediate` strategy, it recreates the listener promptly. With `eventual`, ARC
waits for that scale set's running and pending runners to reach zero. Long jobs
can therefore delay new job assignment for hours.

#### Recycle only the listener pod

For the shortest gap, patch the live listener image and delete only its pod:

```bash
kubectl -n <controller-ns> patch autoscalinglistener <listener-name> \
  --type merge \
  -p '{"spec":{"image":"<registry>/gha-runner-scale-set-controller:0.12.0-cveNNNN"}}'

kubectl -n <controller-ns> delete pod <listener-name>
```

The listener controller rebuilds the pod without deleting its RBAC or secrets,
and this path does not wait for an `eventual` drain.

If `listenerTemplate` pins the listener image, patch the live
`AutoscalingListener.spec.template` too. Do not change the Helm value during
this pod-only rollout: that changes the listener hash and triggers full
listener recreation.

### 8. Verify the patch

- Confirm the controller and every listener report the patched `imageID`.
- Confirm ARS UIDs are unchanged and no scale set is caught in version-guard
  deletion.
- Run a workflow that forces scale-up from zero. A job using an existing idle
  runner does not prove that the listener path works.

### Roll back

Before listener migration, restore the controller with:

```bash
helm -n <helm-release-ns> rollback <controller-release>
```

After listener migration, roll back the controller and then restore listeners
one at a time with the same step-7 method. A Helm rollback alone does not
change their stored image.

If a bad compiled `build.Version` starts deleting ARSs, stop the controller
immediately:

```bash
kubectl -n <controller-ns> scale deploy/<controller> --replicas=0
```

Suspend GitOps first if it would restore the replicas. Scaling down prevents
further deletions, but it cannot rescue an ARS that already has a
`deletionTimestamp`. Reconcile the affected runner release after deploying the
correct image.

### Tradeoffs

- You own the custom image and must rebuild it for later Go CVEs.
- You need a trusted registry, multi-architecture builds, digest pinning, and
  signing or attestation.
- You remain on 0.12.0 and still need a full upgrade later.
- This playbook does not fix a CVE in the runner image or an unrelated base
  image.

## Full upgrade options

### Supported uninstall and reinstall

This is the documented single-cluster path:

1. Drain jobs and pause new work.
2. Uninstall every runner-scale-set release and wait for ARC cleanup.
3. Confirm that no ARC custom resources or runner/listener pods remain.
4. Uninstall the controller.
5. If the CRDs changed, delete them only after all custom resources are gone.
6. Install the 0.14.2 controller chart. A fresh install creates the 0.14.2
   CRDs.
7. Reinstall each runner-scale-set release at 0.14.2.

No new jobs are assigned during this window. Keep the GitHub authentication
secret until cleanup finishes.

### In-place upgrade

This path is shorter but nonstandard. Helm releases cannot move atomically, so
you must prevent either controller minor from reconciling the other minor's
ARSs:

1. Suspend GitOps reconciliation.
2. Scale every ARC controller in the cluster to zero.
3. Replace the existing CRDs with the 0.14.2 definitions using
   `kubectl replace`. Do not use client-side `kubectl apply`; the
   autoscalingrunnersets CRD exceeds the annotation-size limit. Never use
   `replace --force`.
4. Upgrade every runner-scale-set release to 0.14.2.
5. Confirm that every desired ARS is labeled 0.14.2.
6. Upgrade the controller chart to 0.14.2 and restore its replicas.

If any runner release fails, leave the controllers stopped until the versions
are consistent. The `eventual` strategy does not protect this procedure
because the compatibility guard runs before rollout logic.

### Two-cluster active-active

For continuous job assignment, use two clusters with matching scale-set names
in separate runner groups. Upgrade one cluster while the other accepts jobs.

Two replicas in one controller release are leader-elected active/passive. A
second controller release uses a different lease and may reconcile the same
resources at the same time. Neither setup replaces the documented two-cluster
design.

### Graceful runner drain

`--update-strategy=eventual` drains the old runner set before ARC creates the
new one. This avoids temporary overprovisioning and protects running jobs, but
new job assignment pauses until the drain finishes. It does not bypass the
version guard or replace the CRD step.

### Stage first

Rehearse the chosen path in a representative staging cluster. Do not canary by
starting a second controller release against the same scale sets; both
controllers can be active.

## What causes downtime

- Recreating a listener pauses new assignments for that scale set. Queued jobs
  remain in GitHub and are assigned when the listener returns.
- ARC asks the Actions service before removing a runner. If the runner still
  has a job, the service returns `JobStillRunningError` and ARC leaves it
  alone.
- `immediate` creates the new runner set before cleaning up the old one.
  `eventual` drains first and pauses new assignments.
- Deleting runner pods, namespaces, CRDs, or finalizers outside normal
  reconciliation can interrupt jobs.
- Restarting the controller does not stop existing listeners or runners.
  Leader election is enabled automatically only when one controller release
  has `replicaCount > 1`.

## Pre-flight checklist

- Record all controller and runner releases, namespaces, versions, and values.
- List every ARC custom resource and note its UID.
- Scan the exact live controller, listener, runner, dind, init, and sidecar
  image digests.
- Record the update strategy, `containerMode`, listener template overrides,
  custom RBAC/PVCs, and scale set names containing underscores.
- Before deleting CRDs, confirm that no ARC custom resources or runner/listener
  pods remain.
- Keep the GitHub authentication secret until finalizers complete.
- Keep the current chart values and images ready for rollback.
