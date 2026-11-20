package actionsgithubcom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
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
	// cacheGCInterval is how often snapshots of a scale set are garbage collected.
	cacheGCInterval = 30 * time.Second
)

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

func newCacheClaim(runner *v1alpha1.EphemeralRunner, lineage string, restoreFrom *snapshotv1.VolumeSnapshot) *corev1.PersistentVolumeClaim {
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
	if restoreFrom != nil {
		claim.Annotations = map[string]string{AnnotationKeyCacheBaseSnapshot: restoreFrom.Name}
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
	if v := runner.Status.JobRepositoryName; v != "" {
		annotations[AnnotationKeyCacheJobRepository] = v
	}
	if v := runner.Status.JobWorkflowRef; v != "" {
		annotations[AnnotationKeyCacheJobWorkflowRef] = v
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
		return "", nil
	}

	name := cacheClaimName(runner)
	reader := r.cacheReader()
	err := reader.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: name}, new(corev1.PersistentVolumeClaim))
	switch {
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

	claim := newCacheClaim(runner, lineage, restoreFrom)
	if err := r.setControllerReference(runner, claim); err != nil {
		return "", fmt.Errorf("failed to set controller reference for the cache claim: %w", err)
	}
	if err := r.Create(ctx, claim); err != nil && !kerrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("failed to create the cache claim: %w", err)
	}

	if restoreFrom != nil {
		log.Info("Created cache claim restored from snapshot", "claim", name, "snapshot", restoreFrom.Name)
	} else {
		log.Info("Created empty cache claim, no snapshot to restore from yet", "claim", name)
	}
	return name, nil
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
