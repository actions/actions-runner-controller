package actionsgithubcom

import (
	"context"
	"fmt"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CacheSnapshotReconciler keeps only the newest cache snapshot of every
// AutoscalingRunnerSet that has a cache volume.
//
// It lists snapshots with the API reader and polls, instead of watching
// VolumeSnapshots, so enabling the feature needs no cluster-wide permissions
// and nothing is cached for clusters without the snapshot API.
type CacheSnapshotReconciler struct {
	client.Client
	APIReader client.Reader
	Log       logr.Logger
	Scheme    *runtime.Scheme

	// Now returns the current time. It defaults to time.Now.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;create;patch;delete

func (r *CacheSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("autoscalingrunnerset", req.NamespacedName)

	var scaleSet v1alpha1.AutoscalingRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &scaleSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if scaleSet.Spec.CacheVolume == nil || !scaleSet.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if !snapshotAPIAvailable(r.Client) {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	snapshots, err := listCacheSnapshots(ctx, reader, scaleSet.Namespace, cacheLineage(scaleSet.Namespace, scaleSet.Name))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list cache snapshots: %w", err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	plan := planCacheGC(snapshots, now())

	for _, s := range plan.markSuperseded {
		patch := client.MergeFrom(s.DeepCopy())
		if s.Annotations == nil {
			s.Annotations = map[string]string{}
		}
		s.Annotations[AnnotationKeyCacheSupersededAt] = now().UTC().Format(time.RFC3339)
		if err := r.Patch(ctx, s, patch); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("failed to mark cache snapshot %q as superseded: %w", s.Name, err)
		}
	}
	for _, s := range plan.delete {
		uid := s.UID
		if err := r.Delete(ctx, s, client.Preconditions{UID: &uid}); client.IgnoreNotFound(err) != nil && !kerrors.IsConflict(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete cache snapshot %q: %w", s.Name, err)
		}
		log.Info("Deleted cache snapshot", "snapshot", s.Name)
	}

	return ctrl.Result{RequeueAfter: cacheGCInterval}, nil
}

type cacheGCPlan struct {
	// markSuperseded are ready snapshots a newer ready snapshot replaced that
	// have not been seen replaced before.
	markSuperseded []*snapshotv1.VolumeSnapshot
	// delete are snapshots to remove now.
	delete []*snapshotv1.VolumeSnapshot
}

// planCacheGC decides which snapshots of one scale set to remove. It never
// removes the newest ready snapshot, nor a snapshot that is still being taken.
//
// A replaced snapshot is only deleted a grace period after it was first seen
// replaced, so a runner that chose it as its restore source just before its
// replacement became ready can still create its volume from it.
func planCacheGC(snapshots []snapshotv1.VolumeSnapshot, now time.Time) cacheGCPlan {
	var plan cacheGCPlan
	newest := newestReadySnapshot(snapshots)

	for i := range snapshots {
		s := &snapshots[i]
		if !s.DeletionTimestamp.IsZero() || s == newest {
			continue
		}

		if !snapshotReady(s) {
			if s.Status != nil && s.Status.Error != nil && now.Sub(s.CreationTimestamp.Time) >= cacheFailedSnapshotTTL {
				plan.delete = append(plan.delete, s)
			}
			continue
		}

		// Ready, and not the newest.
		raw, marked := s.Annotations[AnnotationKeyCacheSupersededAt]
		if !marked {
			plan.markSuperseded = append(plan.markSuperseded, s)
			continue
		}
		supersededAt, err := time.Parse(time.RFC3339, raw)
		if err != nil || now.Sub(supersededAt) >= cacheSupersededGracePeriod {
			plan.delete = append(plan.delete, s)
		}
	}
	return plan
}

func (r *CacheSnapshotReconciler) SetupWithManager(mgr ctrl.Manager, opts ...Option) error {
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}
	return builderWithOptions(
		ctrl.NewControllerManagedBy(mgr).
			Named("autoscalingrunnerset-cache-snapshots").
			For(&v1alpha1.AutoscalingRunnerSet{}),
		opts,
	).Complete(r)
}
