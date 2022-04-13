package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	labelKeyCleanup               = "pending-cleanup"
	labelKeyRunnerStatefulSetName = "runner-statefulset-name"
)

func syncVolumes(ctx context.Context, c client.Client, log logr.Logger, ns string, runnerSet *v1alpha1.RunnerSet, statefulsets []appsv1.StatefulSet) (*ctrl.Result, error) {
	log = log.WithValues("ns", ns)

	for _, t := range runnerSet.Spec.StatefulSetSpec.VolumeClaimTemplates {
		for _, sts := range statefulsets {
			pvcName := fmt.Sprintf("%s-%s-0", t.Name, sts.Name)

			var pvc corev1.PersistentVolumeClaim
			if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: pvcName}, &pvc); err != nil {
				if !kerrors.IsNotFound(err) {
					return nil, err
				}
				continue
			}

			// TODO move this to statefulset reconciler so that we spam this less,
			// by starting the loop only after the statefulset got deletionTimestamp set.
			// Perhaps you can just wrap this in a finalizer here.
			if pvc.Labels[labelKeyRunnerStatefulSetName] == "" {
				updated := pvc.DeepCopy()
				updated.Labels[labelKeyRunnerStatefulSetName] = sts.Name
				if err := c.Update(ctx, updated); err != nil {
					return nil, err
				}
			}
		}
	}

	// PVs are not namespaced hence we don't need client.InNamespace(ns).
	// If we added that, c.List will silently return zero items.
	//
	// This `List` needs to be done in a dedicated reconciler that is registered to the manager via the `For` func.
	// Otherwise the List func might return outdated contents(I saw status.phase being Bound even after K8s updated it to Released, and it lasted minutes).
	//
	// cleanupLabels := map[string]string{
	// 	labelKeyCleanup: runnerSet.Name,
	// }
	// pvList := &corev1.PersistentVolumeList{}
	// if err := c.List(ctx, pvList, client.MatchingLabels(cleanupLabels)); err != nil {
	// 	log.Info("retrying pv listing", "ns", ns, "err", err)
	// 	return nil, err
	// }

	return nil, nil
}

func syncPVC(ctx context.Context, c client.Client, log logr.Logger, ns string, pvc *corev1.PersistentVolumeClaim) (*ctrl.Result, error) {
	stsName := pvc.Labels[labelKeyRunnerStatefulSetName]
	if stsName == "" {
		return nil, nil
	}

	var sts appsv1.StatefulSet
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: stsName}, &sts); err != nil {
		if !kerrors.IsNotFound(err) {
			return nil, err
		}
	} else {
		// We assume that the statefulset is shortly terminated, hence retry forever until it gets removed.
		retry := 10 * time.Second
		log.V(1).Info("Retrying sync until statefulset gets removed", "requeueAfter", retry)
		return &ctrl.Result{RequeueAfter: retry}, nil
	}

	log = log.WithValues("pvc", pvc.Name, "sts", stsName)

	pvName := pvc.Spec.VolumeName

	if pvName != "" {
		// If we deleted PVC before unsetting pv.spec.claimRef,
		// K8s seems to revive the claimRef :thinking:
		// So we need to mark PV for claimRef unset first, and delete PVC, and finally unset claimRef on PV.

		var pv corev1.PersistentVolume
		if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: pvName}, &pv); err != nil {
			if !kerrors.IsNotFound(err) {
				return nil, err
			}
			return nil, nil
		}

		pvCopy := pv.DeepCopy()
		if pvCopy.Labels == nil {
			pvCopy.Labels = map[string]string{}
		}
		pvCopy.Labels[labelKeyCleanup] = stsName

		log.Info("Scheduling to unset PV's claimRef", "pv", pv.Name)

		if err := c.Update(ctx, pvCopy); err != nil {
			return nil, err
		}

		// At this point, the PV is still Bound

		log.Info("Deleting unused pvc")

		if err := c.Delete(ctx, pvc); err != nil {
			return nil, err
		}

		// At this point, the PV becomes Released
	}

	return nil, nil
}

func syncPV(ctx context.Context, c client.Client, log logr.Logger, ns string, pv *corev1.PersistentVolume) (*ctrl.Result, error) {
	log.V(2).Info("checking pv claimRef")

	if pv.Spec.ClaimRef == nil {
		return nil, nil
	}

	log.V(2).Info("checking labels")

	if pv.Labels[labelKeyCleanup] == "" {
		// We assume that the pvc is shortly terminated, hence retry forever until it gets removed.
		retry := 10 * time.Second
		log.V(1).Info("Retrying sync until pvc gets removed", "requeueAfter", retry)
		return &ctrl.Result{RequeueAfter: retry}, nil
	}

	log.V(2).Info("checking pv phase", "phase", pv.Status.Phase)

	if pv.Status.Phase != corev1.VolumeReleased {
		// We assume that the pvc is shortly terminated, hence retry forever until it gets removed.
		retry := 10 * time.Second
		log.V(1).Info("Retrying sync until pvc gets released", "requeueAfter", retry)
		return &ctrl.Result{RequeueAfter: retry}, nil
	}

	// At this point, the PV is still Released

	pvCopy := pv.DeepCopy()
	delete(pvCopy.Labels, labelKeyCleanup)
	pvCopy.Spec.ClaimRef = nil
	log.Info("Unsetting PV's claimRef", "pv", pv.Name)
	if err := c.Update(ctx, pvCopy); err != nil {
		return nil, err
	}

	// At this point, the PV becomes Available, if it's reclaim policy is "Retain".
	// I have not yet tested it with "Delete" but perhaps it's deleted automatically after the update?
	// https://kubernetes.io/docs/concepts/storage/persistent-volumes/#retain

	return nil, nil
}
