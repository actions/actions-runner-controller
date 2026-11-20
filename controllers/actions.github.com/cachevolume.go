package actionsgithubcom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelKeyCacheLineage identifies the runner scale set a cache claim or
	// snapshot belongs to. All snapshots of a lineage form one cache history.
	LabelKeyCacheLineage = "actions.github.com/cache-lineage"

	// AnnotationKeyCacheSourceRunner is the EphemeralRunner a snapshot was taken from.
	AnnotationKeyCacheSourceRunner = "actions.github.com/cache-source-runner"
	// AnnotationKeyCacheBaseSnapshot is the snapshot the source runner restored its volume from.
	AnnotationKeyCacheBaseSnapshot = "actions.github.com/cache-base-snapshot"
	// AnnotationKeyCacheJobRepository, AnnotationKeyCacheJobWorkflowRef and
	// AnnotationKeyCacheWorkflowRunID describe the job that produced a snapshot.
	// They are informational.
	AnnotationKeyCacheJobRepository  = "actions.github.com/cache-job-repository"
	AnnotationKeyCacheJobWorkflowRef = "actions.github.com/cache-job-workflow-ref"
	AnnotationKeyCacheWorkflowRunID  = "actions.github.com/cache-workflow-run-id"
	AnnotationKeyCacheJobEvent       = "actions.github.com/cache-job-event"
	// AnnotationKeyCacheChainStartedAt records when the cache history a snapshot belongs to
	// was started from an empty volume. It is inherited by every snapshot of the chain, and
	// the retention maxAge is measured from it.
	AnnotationKeyCacheChainStartedAt = "actions.github.com/cache-chain-started-at"
	// AnnotationKeyCacheRestoreSkipped marks a runner that gave up waiting for its volume to be
	// restored from the cache snapshot. Its replacement volume starts empty. The value is the reason.
	AnnotationKeyCacheRestoreSkipped = "actions.github.com/cache-restore-skipped"
	// AnnotationKeyCacheRestoreWaitStartedAt records when the cluster was first seen provisioning
	// the restored volume of a runner, which is when the restore timeout starts counting.
	AnnotationKeyCacheRestoreWaitStartedAt = "actions.github.com/cache-restore-wait-started-at"
	// AnnotationKeyCacheLastUsedAt records when a runner last restored its volume from a snapshot.
	AnnotationKeyCacheLastUsedAt = "actions.github.com/cache-last-used-at"
	// AnnotationKeyCacheSupersededAt records when garbage collection first saw
	// a snapshot that a newer ready snapshot replaced.
	AnnotationKeyCacheSupersededAt = "actions.github.com/cache-superseded-at"
)

const (
	// cacheDetachWaitTimeout bounds how long a finished runner waits for its
	// pod to disappear and its volume to be detached before the volume is
	// snapshotted anyway. A snapshot of a detached volume does not miss data
	// the kernel has not flushed yet.
	cacheDetachWaitTimeout = 60 * time.Second
	// cacheDetachPollInterval is how often the detach wait is rechecked.
	cacheDetachPollInterval = 2 * time.Second
	// cacheBindPollInterval is how often a requested snapshot is checked for having been
	// picked up by the snapshot controller.
	cacheBindPollInterval = 500 * time.Millisecond
	// cacheSnapshotBindTimeout bounds how long a finished runner waits for the snapshot
	// controller to pick up the snapshot request before it is released anyway.
	cacheSnapshotBindTimeout = 60 * time.Second
	// cacheSupersededGracePeriod is how long a replaced snapshot is kept after
	// garbage collection first saw it replaced. It covers runners that picked
	// the old snapshot as their restore source just before the new one became ready.
	cacheSupersededGracePeriod = time.Minute
	// cacheFailedSnapshotTTL is how long a snapshot that reports an error is kept.
	cacheFailedSnapshotTTL = 10 * time.Minute
	// DefaultCacheRestoreTimeout applies when a cache volume does not set restoreTimeout.
	DefaultCacheRestoreTimeout = 5 * time.Minute
	// cacheRestorePollInterval is how often a runner whose restored volume is not provisioned yet is checked.
	cacheRestorePollInterval = 5 * time.Second
	// DefaultCacheMaxAge and DefaultCacheIdleTimeout apply when the retention of a cache
	// volume does not set them.
	DefaultCacheMaxAge      = 14 * 24 * time.Hour
	DefaultCacheIdleTimeout = 7 * 24 * time.Hour
	// cacheLastUsedRefresh is how stale the last-used mark of a snapshot has to be before a
	// runner restoring from it refreshes it, so that busy scale sets do not write on every start.
	cacheLastUsedRefresh = time.Hour
	// cacheGCInterval is how often snapshots of a scale set are garbage collected.
	cacheGCInterval = 30 * time.Second
)

// errCacheClaimTerminating is returned while the claim of a runner is still being deleted,
// as after a restore timeout, and the runner has to wait for the name to be free again.
var errCacheClaimTerminating = errors.New("the cache claim of the runner is being deleted")

// cacheLineage returns the label value identifying the cache of a scale set.
// It is a digest because namespace and name together can exceed the label length limit.
func cacheLineage(namespace, scaleSetName string) string {
	sum := sha256.Sum256([]byte(namespace + "/" + scaleSetName))
	return hex.EncodeToString(sum[:])[:20]
}

// cacheClaimName is the name of the claim, and of the snapshot taken from it, of a runner.
func cacheClaimName(runner *v1alpha1.EphemeralRunner) string {
	return runner.Name + "-cache"
}

// cacheScaleSetName returns the name of the AutoscalingRunnerSet a runner belongs to.
// The EphemeralRunnerSet carries the name of its AutoscalingRunnerSet.
func cacheScaleSetName(runner *v1alpha1.EphemeralRunner) string {
	if owner := metav1.GetControllerOf(runner); owner != nil && owner.Kind == "EphemeralRunnerSet" {
		return owner.Name
	}
	return runner.Labels[LabelKeyGitHubScaleSetName]
}

// snapshotAPIAvailable reports whether the VolumeSnapshot API is served by the cluster.
func snapshotAPIAvailable(c client.Client) bool {
	_, err := c.RESTMapper().RESTMapping(
		schema.GroupKind{Group: snapshotv1.GroupName, Kind: "VolumeSnapshot"},
		snapshotv1.SchemeGroupVersion.Version,
	)
	return err == nil
}

// snapshotReady reports whether a snapshot can be restored from.
func snapshotReady(s *snapshotv1.VolumeSnapshot) bool {
	return s.DeletionTimestamp.IsZero() &&
		s.Status != nil &&
		s.Status.ReadyToUse != nil && *s.Status.ReadyToUse
}

// snapshotTime is the time the snapshot was cut, falling back to the creation
// of the object while the storage system has not reported it yet.
func snapshotTime(s *snapshotv1.VolumeSnapshot) time.Time {
	if s.Status != nil && s.Status.CreationTime != nil {
		return s.Status.CreationTime.Time
	}
	return s.CreationTimestamp.Time
}

// newestReadySnapshot returns the most recently cut snapshot that can be
// restored from, or nil. Ties are broken by name so the choice is stable.
func newestReadySnapshot(snapshots []snapshotv1.VolumeSnapshot) *snapshotv1.VolumeSnapshot {
	var newest *snapshotv1.VolumeSnapshot
	for i := range snapshots {
		s := &snapshots[i]
		if !snapshotReady(s) {
			continue
		}
		if newest == nil {
			newest = s
			continue
		}
		switch ts, nts := snapshotTime(s), snapshotTime(newest); {
		case ts.After(nts), ts.Equal(nts) && s.Name > newest.Name:
			newest = s
		}
	}
	return newest
}

// injectCacheVolume adds the cache volume to the pod spec, replacing a volume of
// the same name. An empty claim name falls back to an emptyDir, so containers
// that mount the volume still start when the cache cannot be provided.
func injectCacheVolume(spec *corev1.PodSpec, volumeName, claimName string) {
	volume := corev1.Volume{Name: volumeName}
	if claimName != "" {
		volume.VolumeSource.PersistentVolumeClaim = &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claimName}
	} else {
		volume.VolumeSource.EmptyDir = &corev1.EmptyDirVolumeSource{}
	}

	// The slice is shared with the runner spec the pod spec was copied from.
	volumes := slices.Clone(spec.Volumes)
	if i := slices.IndexFunc(volumes, func(v corev1.Volume) bool { return v.Name == volumeName }); i >= 0 {
		volumes[i] = volume
	} else {
		volumes = append(volumes, volume)
	}
	spec.Volumes = volumes
}

func (r *EphemeralRunnerReconciler) cacheReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func newCacheClaim(runner *v1alpha1.EphemeralRunner, lineage string, restoreFrom *snapshotv1.VolumeSnapshot, now time.Time) *corev1.PersistentVolumeClaim {
	cv := runner.Spec.CacheVolume
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cacheClaimName(runner),
			Namespace: runner.Namespace,
			Labels: map[string]string{
				LabelKeyCacheLineage:        lineage,
				LabelKeyKubernetesComponent: "cache",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: ptr.To(cv.StorageClassName),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: cv.Size},
			},
		},
	}
	chainStart := now
	if restoreFrom != nil {
		chainStart = cacheChainStart(restoreFrom)
	}
	claim.Annotations = map[string]string{AnnotationKeyCacheChainStartedAt: chainStart.UTC().Format(time.RFC3339)}
	if restoreFrom != nil {
		claim.Annotations[AnnotationKeyCacheBaseSnapshot] = restoreFrom.Name
		claim.Spec.DataSource = &corev1.TypedLocalObjectReference{
			APIGroup: ptr.To(snapshotv1.GroupName),
			Kind:     "VolumeSnapshot",
			Name:     restoreFrom.Name,
		}
	}
	return claim
}

func newCacheSnapshot(runner *v1alpha1.EphemeralRunner, claim *corev1.PersistentVolumeClaim, lineage string) *snapshotv1.VolumeSnapshot {
	annotations := map[string]string{AnnotationKeyCacheSourceRunner: runner.Name}
	if base := claim.Annotations[AnnotationKeyCacheBaseSnapshot]; base != "" {
		annotations[AnnotationKeyCacheBaseSnapshot] = base
	}
	if started := claim.Annotations[AnnotationKeyCacheChainStartedAt]; started != "" {
		annotations[AnnotationKeyCacheChainStartedAt] = started
	}
	if v := runner.Status.JobRepositoryName; v != "" {
		annotations[AnnotationKeyCacheJobRepository] = v
	}
	if v := runner.Status.JobWorkflowRef; v != "" {
		annotations[AnnotationKeyCacheJobWorkflowRef] = v
	}
	if v := runner.Status.JobEventName; v != "" {
		annotations[AnnotationKeyCacheJobEvent] = v
	}
	if v := runner.Status.WorkflowRunID; v != 0 {
		annotations[AnnotationKeyCacheWorkflowRunID] = strconv.FormatInt(v, 10)
	}

	return &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:        claim.Name,
			Namespace:   claim.Namespace,
			Labels:      map[string]string{LabelKeyCacheLineage: lineage},
			Annotations: annotations,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source:                  snapshotv1.VolumeSnapshotSource{PersistentVolumeClaimName: ptr.To(claim.Name)},
			VolumeSnapshotClassName: runner.Spec.CacheVolume.SnapshotClassName,
		},
	}
}

func listCacheSnapshots(ctx context.Context, reader client.Reader, namespace, lineage string) ([]snapshotv1.VolumeSnapshot, error) {
	var list snapshotv1.VolumeSnapshotList
	if err := reader.List(ctx, &list, client.InNamespace(namespace), client.MatchingLabels{LabelKeyCacheLineage: lineage}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ensureCacheClaim makes sure the claim backing the cache volume of the runner
// exists, restored from the newest snapshot of the scale set if there is one.
//
// It returns the name of the claim, or an empty name when the cache cannot be
// provided and the pod should get an ephemeral volume instead. A cache that
// cannot be provided never fails the runner.
func (r *EphemeralRunnerReconciler) ensureCacheClaim(ctx context.Context, runner *v1alpha1.EphemeralRunner, log logr.Logger) (string, error) {
	scaleSet := cacheScaleSetName(runner)
	if scaleSet == "" {
		log.Info("Cannot tell which scale set the runner belongs to, running without a persistent cache volume")
		return "", nil
	}
	if !snapshotAPIAvailable(r.Client) {
		log.Info("The VolumeSnapshot API is not available in the cluster, running without a persistent cache volume")
		r.cacheEvent(runner, corev1.EventTypeWarning, "CacheUnavailable", "The VolumeSnapshot API is not available in the cluster, the runner starts with an empty non-persistent cache volume")
		r.recordCacheRestore(runner, scaleSet, metrics.CacheRestoreUnavailable)
		return "", nil
	}

	name := cacheClaimName(runner)
	reader := r.cacheReader()
	var existing corev1.PersistentVolumeClaim
	err := reader.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: name}, &existing)
	switch {
	case err == nil && !existing.DeletionTimestamp.IsZero():
		return "", errCacheClaimTerminating
	case err == nil:
		return name, nil
	case !kerrors.IsNotFound(err):
		return "", fmt.Errorf("failed to get the cache claim: %w", err)
	}

	lineage := cacheLineage(runner.Namespace, scaleSet)
	snapshots, err := listCacheSnapshots(ctx, reader, runner.Namespace, lineage)
	if err != nil {
		return "", fmt.Errorf("failed to list cache snapshots: %w", err)
	}
	restoreFrom := newestReadySnapshot(snapshots)

	now := time.Now()
	result := metrics.CacheRestoreHit
	switch {
	case restoreFrom == nil:
		result = metrics.CacheRestoreCold
	case runner.Annotations[AnnotationKeyCacheRestoreSkipped] != "":
		log.Info("Not restoring the cache snapshot, restoring it failed before", "snapshot", restoreFrom.Name, "reason", runner.Annotations[AnnotationKeyCacheRestoreSkipped])
		restoreFrom, result = nil, metrics.CacheRestoreTimeout
	default:
		maxAge, idleTimeout := cacheRetentionLimits(runner.Spec.CacheVolume)
		if expired, reason := cacheExpired(restoreFrom, maxAge, idleTimeout, now); expired {
			log.Info("The newest cache snapshot has expired, starting from an empty volume", "snapshot", restoreFrom.Name, "reason", reason)
			restoreFrom, result = nil, metrics.CacheRestoreExpired
		} else {
			r.touchCacheSnapshot(ctx, restoreFrom, now, log)
		}
	}

	claim := newCacheClaim(runner, lineage, restoreFrom, now)
	if err := r.setControllerReference(runner, claim); err != nil {
		return "", fmt.Errorf("failed to set controller reference for the cache claim: %w", err)
	}
	if err := r.Create(ctx, claim); err != nil && !kerrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("failed to create the cache claim: %w", err)
	}
	r.recordCacheRestore(runner, scaleSet, result)

	if restoreFrom != nil {
		log.Info("Created cache claim restored from snapshot", "claim", name, "snapshot", restoreFrom.Name)
	} else {
		log.Info("Created empty cache claim", "claim", name, "reason", result)
	}
	return name, nil
}

func (r *EphemeralRunnerReconciler) recordCacheRestore(runner *v1alpha1.EphemeralRunner, scaleSet, result string) {
	if r.PublishMetrics {
		metrics.CacheRestore(runner.Namespace, scaleSet, result)
	}
}

func (r *EphemeralRunnerReconciler) recordCacheCommit(runner *v1alpha1.EphemeralRunner, result string) {
	if r.PublishMetrics {
		metrics.CacheCommit(runner.Namespace, cacheScaleSetName(runner), result)
	}
}

// cacheEvent records a Kubernetes event on the runner. Without a recorder it does nothing.
func (r *EphemeralRunnerReconciler) cacheEvent(runner *v1alpha1.EphemeralRunner, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(runner, nil, eventType, reason, "Cache", "%s", message)
	}
}

// cacheRestoreTimeout returns how long a runner waits for its restored volume. Zero waits forever.
func cacheRestoreTimeout(cv *v1alpha1.CacheVolume) time.Duration {
	if cv == nil || cv.RestoreTimeout == nil {
		return DefaultCacheRestoreTimeout
	}
	return max(cv.RestoreTimeout.Duration, 0)
}

// cacheProvisioningStarted reports whether the cluster began provisioning the volume of a
// claim. A claim that waits for its first consumer stays unbound while the pod waits for
// capacity, which has nothing to do with the restore, so the restore timeout must not run then.
func cacheProvisioningStarted(claim *corev1.PersistentVolumeClaim) bool {
	return claim.Annotations["volume.kubernetes.io/storage-provisioner"] != "" ||
		claim.Annotations["volume.beta.kubernetes.io/storage-provisioner"] != ""
}

// enforceCacheRestoreTimeout makes sure a cache snapshot that cannot be restored does not keep a
// runner from starting. While the pod of a runner has not started and the claim restored from a
// snapshot is not bound, it waits for restoreTimeout once the cluster began provisioning the
// volume. After that it deletes the pod and the claim and marks the runner so that the
// claim is created again without the snapshot: the runner then starts with an empty volume.
//
// It returns how soon the runner has to be looked at again, and whether it fell back.
func (r *EphemeralRunnerReconciler) enforceCacheRestoreTimeout(ctx context.Context, runner *v1alpha1.EphemeralRunner, pod *corev1.Pod, log logr.Logger) (time.Duration, bool, error) {
	cv := runner.Spec.CacheVolume
	timeout := cacheRestoreTimeout(cv)
	if cv == nil || timeout == 0 || pod.Status.Phase != corev1.PodPending || runner.Annotations[AnnotationKeyCacheRestoreSkipped] != "" {
		return 0, false, nil
	}

	var claim corev1.PersistentVolumeClaim
	if err := r.cacheReader().Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: cacheClaimName(runner)}, &claim); err != nil {
		return 0, false, client.IgnoreNotFound(err)
	}
	if claim.Spec.DataSource == nil || claim.Status.Phase == corev1.ClaimBound || !claim.DeletionTimestamp.IsZero() {
		return 0, false, nil
	}
	if !cacheProvisioningStarted(&claim) {
		return cacheRestorePollInterval, false, nil
	}

	now := time.Now()
	started, ok := time.Time{}, false
	if raw := runner.Annotations[AnnotationKeyCacheRestoreWaitStartedAt]; raw != "" {
		started, ok = annotationTimeOf(raw)
	}
	if !ok {
		patch := client.MergeFrom(runner.DeepCopy())
		if runner.Annotations == nil {
			runner.Annotations = map[string]string{}
		}
		runner.Annotations[AnnotationKeyCacheRestoreWaitStartedAt] = now.UTC().Format(time.RFC3339)
		if err := r.Patch(ctx, runner, patch); err != nil {
			return 0, false, fmt.Errorf("failed to record when the cache restore started: %w", err)
		}
		return min(timeout, cacheRestorePollInterval), false, nil
	}
	if remaining := timeout - now.Sub(started); remaining > 0 {
		return min(remaining, cacheRestorePollInterval), false, nil
	}

	log.Info("Restoring the cache snapshot took too long, starting the runner with an empty cache volume",
		"claim", claim.Name, "snapshot", claim.Spec.DataSource.Name, "timeout", timeout)
	patch := client.MergeFrom(runner.DeepCopy())
	runner.Annotations[AnnotationKeyCacheRestoreSkipped] = "restore timed out after " + timeout.String()
	if err := r.Patch(ctx, runner, patch); err != nil {
		return 0, false, fmt.Errorf("failed to mark the runner as having skipped its cache restore: %w", err)
	}
	if err := r.Delete(ctx, pod, r.deletePodOptions(pod)...); client.IgnoreNotFound(err) != nil {
		return 0, false, fmt.Errorf("failed to delete the pod waiting for its cache volume: %w", err)
	}
	if err := r.Delete(ctx, &claim); client.IgnoreNotFound(err) != nil {
		return 0, false, fmt.Errorf("failed to delete the cache claim that cannot be restored: %w", err)
	}
	r.cacheEvent(runner, corev1.EventTypeWarning, "CacheRestoreTimedOut",
		fmt.Sprintf("Restoring the cache snapshot %q did not finish within %v, the runner starts with an empty cache volume", claim.Spec.DataSource.Name, timeout))
	r.recordCacheRestore(runner, cacheScaleSetName(runner), metrics.CacheRestoreTimeout)
	return 0, true, nil
}

func annotationTimeOf(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	return t, err == nil
}

// commitCacheVolume snapshots the cache volume of a runner that finished
// cleanly, which makes the snapshot the newest of the scale set.
//
// It does not wait for the snapshot to be cut, which can take minutes, but it
// does wait until the snapshot controller has picked the request up. Picking it
// up pins the claim with a finalizer that holds it until the snapshot is cut.
// Without that wait the runner would be released first, the claim, which the
// runner owns, would be garbage collected, and the snapshot controller would
// find it gone or being deleted and fail the snapshot for good.
//
// Before requesting the snapshot it waits, for a bounded time, for the volume to be detached.
func (r *EphemeralRunnerReconciler) commitCacheVolume(ctx context.Context, runner *v1alpha1.EphemeralRunner, log logr.Logger) (ctrl.Result, error) {
	if runner.Spec.CacheVolume == nil || runner.Status.Phase != v1alpha1.EphemeralRunnerPhaseSucceeded {
		return ctrl.Result{}, nil
	}
	if ok, reason := cacheCommitAllowed(runner.Spec.CacheVolume.CommitPolicy, &runner.Status); !ok {
		log.Info("Not saving the cache volume of the runner, the commit policy does not allow it", "reason", reason)
		r.cacheEvent(runner, corev1.EventTypeNormal, "CacheNotSaved", "The commit policy does not allow saving the cache volume: "+reason)
		r.recordCacheCommit(runner, metrics.CacheCommitDenied)
		return ctrl.Result{}, nil
	}
	scaleSet := cacheScaleSetName(runner)
	if scaleSet == "" || !snapshotAPIAvailable(r.Client) {
		return ctrl.Result{}, nil
	}

	reader := r.cacheReader()
	name := cacheClaimName(runner)
	key := types.NamespacedName{Namespace: runner.Namespace, Name: name}

	claim := new(corev1.PersistentVolumeClaim)
	if err := reader.Get(ctx, key, claim); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get the cache claim: %w", err)
	}
	if claim.Status.Phase != corev1.ClaimBound || !claim.DeletionTimestamp.IsZero() {
		log.Info("Cache claim is not bound, nothing to snapshot", "claim", name, "phase", claim.Status.Phase)
		return ctrl.Result{}, nil
	}

	var existing snapshotv1.VolumeSnapshot
	err := reader.Get(ctx, key, &existing)
	switch {
	case err == nil:
		return awaitSnapshotBound(&existing, log), nil
	case !kerrors.IsNotFound(err):
		return ctrl.Result{}, fmt.Errorf("failed to get the cache snapshot: %w", err)
	}

	if r.cacheVolumeInUse(ctx, runner, claim, log) {
		return ctrl.Result{RequeueAfter: cacheDetachPollInterval}, nil
	}

	var scaleSetObj v1alpha1.AutoscalingRunnerSet
	if err := r.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: scaleSet}, &scaleSetObj); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get the autoscaling runner set: %w", err)
	}
	if !scaleSetObj.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	snapshot := newCacheSnapshot(runner, claim, cacheLineage(runner.Namespace, scaleSet))
	if err := r.setControllerReference(&scaleSetObj, snapshot); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference for the cache snapshot: %w", err)
	}
	if err := r.Create(ctx, snapshot); err != nil && !kerrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("failed to create the cache snapshot: %w", err)
	}
	log.Info("Requested cache snapshot", "snapshot", snapshot.Name)
	r.recordCacheCommit(runner, metrics.CacheCommitRequested)
	return ctrl.Result{RequeueAfter: cacheBindPollInterval}, nil
}

// snapshotBound reports whether the snapshot controller picked the snapshot up, which
// means it holds the source claim until the snapshot is cut.
func snapshotBound(s *snapshotv1.VolumeSnapshot) bool {
	return s.Status != nil && s.Status.BoundVolumeSnapshotContentName != nil && *s.Status.BoundVolumeSnapshotContentName != ""
}

// awaitSnapshotBound returns a zero result once the claim is pinned by the snapshot
// controller, or when waiting any longer is pointless.
func awaitSnapshotBound(s *snapshotv1.VolumeSnapshot, log logr.Logger) ctrl.Result {
	if snapshotBound(s) {
		return ctrl.Result{}
	}
	if !s.CreationTimestamp.IsZero() && time.Since(s.CreationTimestamp.Time) >= cacheSnapshotBindTimeout {
		log.Info("The snapshot controller did not pick up the cache snapshot in time, releasing the runner", "snapshot", s.Name)
		return ctrl.Result{}
	}
	return ctrl.Result{RequeueAfter: cacheBindPollInterval}
}

// cacheVolumeInUse reports whether the runner pod still exists or the volume is
// still attached to a node. Once the wait exceeds cacheDetachWaitTimeout the
// volume is reported as free, because a crash-consistent snapshot of a cache is
// better than none.
func (r *EphemeralRunnerReconciler) cacheVolumeInUse(ctx context.Context, runner *v1alpha1.EphemeralRunner, claim *corev1.PersistentVolumeClaim, log logr.Logger) bool {
	if deleted := runner.DeletionTimestamp; deleted != nil && time.Since(deleted.Time) >= cacheDetachWaitTimeout {
		log.Info("Timed out waiting for the cache volume to be detached, snapshotting it anyway")
		return false
	}

	reader := r.cacheReader()
	err := reader.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: runner.Name}, new(corev1.Pod))
	if err == nil {
		return true
	}
	if !kerrors.IsNotFound(err) {
		log.Error(err, "Failed to check whether the runner pod is gone, not waiting for the volume to be detached")
		return false
	}

	if claim.Spec.VolumeName == "" {
		return false
	}
	var attachments storagev1.VolumeAttachmentList
	if err := reader.List(ctx, &attachments); err != nil {
		// Not every deployment may read volume attachments, and drivers that do
		// not attach volumes have none. Neither is a reason to hold the runner.
		log.V(1).Info("Cannot list volume attachments, not waiting for the volume to be detached", "error", err.Error())
		return false
	}
	for i := range attachments.Items {
		if pv := attachments.Items[i].Spec.Source.PersistentVolumeName; pv != nil && *pv == claim.Spec.VolumeName {
			return true
		}
	}
	return false
}

// cacheCommitAllowed reports whether the job a runner ran may advance the cache, and
// why not. A nil policy allows everything. Every list that is set must match, and a
// list that is set never matches a property of the job that is not known.
func cacheCommitAllowed(policy *v1alpha1.CacheCommitPolicy, status *v1alpha1.EphemeralRunnerStatus) (bool, string) {
	if policy == nil {
		return true, ""
	}

	if len(policy.Repositories) > 0 && !matchAny(policy.Repositories, status.JobRepositoryName) {
		return false, fmt.Sprintf("repository %q is not allowed to commit", status.JobRepositoryName)
	}
	if len(policy.Refs) > 0 {
		ref := workflowRefRef(status.JobWorkflowRef)
		if !matchAny(policy.Refs, ref) {
			return false, fmt.Sprintf("ref %q is not allowed to commit", ref)
		}
	}
	if len(policy.Events) > 0 && (status.JobEventName == "" || !slices.Contains(policy.Events, status.JobEventName)) {
		return false, fmt.Sprintf("event %q is not allowed to commit", status.JobEventName)
	}
	return true, ""
}

// workflowRefRef extracts the git ref from a job workflow reference of the form
// "owner/repo/.github/workflows/file.yaml@refs/heads/main". It is empty if there is none.
func workflowRefRef(jobWorkflowRef string) string {
	i := strings.LastIndex(jobWorkflowRef, "@")
	if i < 0 {
		return ""
	}
	return jobWorkflowRef[i+1:]
}

// matchAny reports whether value, which must not be empty, matches one of the patterns.
// Patterns that are not valid never match.
func matchAny(patterns []string, value string) bool {
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, value); err == nil && ok {
			return true
		}
	}
	return false
}

// cacheRetentionLimits returns the effective retention of a cache volume. A zero
// duration means the limit is disabled.
func cacheRetentionLimits(cv *v1alpha1.CacheVolume) (maxAge, idleTimeout time.Duration) {
	maxAge, idleTimeout = DefaultCacheMaxAge, DefaultCacheIdleTimeout
	if cv == nil || cv.Retention == nil {
		return maxAge, idleTimeout
	}
	if d := cv.Retention.MaxAge; d != nil {
		maxAge = max(d.Duration, 0)
	}
	if d := cv.Retention.IdleTimeout; d != nil {
		idleTimeout = max(d.Duration, 0)
	}
	return maxAge, idleTimeout
}

func annotationTime(s *snapshotv1.VolumeSnapshot, key string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s.Annotations[key])
	return t, err == nil
}

// cacheChainStart is when the history the snapshot belongs to started from an empty volume.
func cacheChainStart(s *snapshotv1.VolumeSnapshot) time.Time {
	if t, ok := annotationTime(s, AnnotationKeyCacheChainStartedAt); ok {
		return t
	}
	return snapshotTime(s)
}

// cacheLastUsed is when the snapshot was last restored from, or cut if that is later.
func cacheLastUsed(s *snapshotv1.VolumeSnapshot) time.Time {
	used := snapshotTime(s)
	if t, ok := annotationTime(s, AnnotationKeyCacheLastUsedAt); ok && t.After(used) {
		used = t
	}
	return used
}

// cacheExpired reports whether the snapshot has outlived the retention, and why.
func cacheExpired(s *snapshotv1.VolumeSnapshot, maxAge, idleTimeout time.Duration, now time.Time) (bool, string) {
	if maxAge > 0 && now.Sub(cacheChainStart(s)) >= maxAge {
		return true, fmt.Sprintf("the cache history started more than %v ago", maxAge)
	}
	if idleTimeout > 0 && now.Sub(cacheLastUsed(s)) >= idleTimeout {
		return true, fmt.Sprintf("the cache was not used for more than %v", idleTimeout)
	}
	return false, ""
}

// touchCacheSnapshot records that a runner restores from the snapshot. It is best effort and
// skipped while the existing mark is recent.
func (r *EphemeralRunnerReconciler) touchCacheSnapshot(ctx context.Context, s *snapshotv1.VolumeSnapshot, now time.Time, log logr.Logger) {
	if t, ok := annotationTime(s, AnnotationKeyCacheLastUsedAt); ok && now.Sub(t) < cacheLastUsedRefresh {
		return
	}
	patch := client.MergeFrom(s.DeepCopy())
	if s.Annotations == nil {
		s.Annotations = map[string]string{}
	}
	s.Annotations[AnnotationKeyCacheLastUsedAt] = now.UTC().Format(time.RFC3339)
	if err := r.Patch(ctx, s, patch); err != nil && !kerrors.IsNotFound(err) {
		log.V(1).Info("Could not record the use of the cache snapshot", "snapshot", s.Name, "error", err.Error())
	}
}
