/*
Copyright 2020 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actionsgithubcom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// EphemeralRunnerSetFinalizerName is the finalizer name used in EphemeralRunnerSet resource to protect the cleanup process of the child ephemeral runners and proxy secret.
	EphemeralRunnerSetFinalizerName = "ephemeralrunnerset.actions.github.com/finalizer"
)

// EphemeralRunnerSetReconciler reconciles a EphemeralRunnerSet object
type EphemeralRunnerSetReconciler struct {
	client.Client
	Log            logr.Logger
	Scheme         *runtime.Scheme
	PublishMetrics bool
	*ResourceBuilder
	specHashCache sync.Map
}

type specHashCacheEntry struct {
	uid        types.UID
	generation int64
	hash       string
}

// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// The responsibility of this controller is to bring the state to the desired one, but it should
// avoid patching itself, because of the frequent patches that the listener is doing.
// The safe point where we can patch the resource is when we are reacting on finalizer.
// Then, the listener should be deleted first, to allow controller clean up resources without interruptions
//
// The resource should be created with finalizer. To leave it to this controller to add it, we would
// risk the same issue of patching the status. Responsibility of this controller should only
// be to bring the count of EphemeralRunners to the desired one, not to patch this resource
// until it is safe to do so
func (r *EphemeralRunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ephemeralrunnerset", req.NamespacedName)

	var ephemeralRunnerSet v1alpha1.EphemeralRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &ephemeralRunnerSet); err != nil {
		if kerrors.IsNotFound(err) {
			r.clearSpecHashCache(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Requested deletion does not need reconciled.
	if !ephemeralRunnerSet.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("Deleting resources")
		done, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for resources to be deleted")
			return ctrl.Result{}, nil
		}

		done, err = r.cleanUpEphemeralRunnerSetProxySecret(ctx, &ephemeralRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up EphemeralRunnerSet proxy secret")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for proxy secret to be deleted")
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}

		log.Info("Removing finalizer")
		if controllerutil.ContainsFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
			original := ephemeralRunnerSet.DeepCopy()
			controllerutil.RemoveFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName)
			if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to update ephemeral runner set with removed finalizer")
				return ctrl.Result{}, err
			}
		}

		log.Info("Successfully removed finalizer after cleanup")
		r.clearSpecHashCache(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
		log.Info("Adding finalizer")
		original := ephemeralRunnerSet.DeepCopy()
		controllerutil.AddFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName)
		if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update ephemeral runner set with new finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	patchEphemeralRunnerSetProxySecretData := func(proxySecret *corev1.Secret) (bool, error) {
		proxySecretData, err := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
			secret := new(corev1.Secret)
			err := r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: s}, secret)
			return secret, err
		})
		if err != nil {
			return false, fmt.Errorf("failed to convert proxy config to secret data: %w", err)
		}

		if maps.EqualFunc(proxySecret.Data, proxySecretData, bytes.Equal) {
			return false, nil
		}

		desiredProxySecret, err := r.newEphemeralRunnerSetProxySecret(&ephemeralRunnerSet, proxySecretData)
		if err != nil {
			return false, fmt.Errorf("failed to build ephemeralRunnerSet proxy secret: %w", err)
		}

		updatedProxySecret := proxySecret.DeepCopy()
		updatedProxySecret.Data = proxySecretData
		updatedProxySecret.Labels = r.filterAndMergeLabels(proxySecret.Labels, desiredProxySecret.Labels)
		updatedProxySecret.Annotations = r.filterAndMergeAnnotations(proxySecret.Annotations, desiredProxySecret.Annotations)

		log.Info("Updating ephemeralRunnerSet proxy secret")
		if err := r.Patch(ctx, updatedProxySecret, client.MergeFrom(proxySecret)); err != nil {
			return false, fmt.Errorf("failed to update ephemeralRunnerSet proxy secret: %w", err)
		}
		return true, nil
	}

	// If hash spec has changed, delete idle ephemeral runners
	// in order to apply the change to the runners that did not yet receive a job.
	storedIntegrityHash := ephemeralRunnerSet.Annotations[AnnotationKeyIntegrityHash]
	if !r.hasSpecHashCache(req.NamespacedName, ephemeralRunnerSet.UID, ephemeralRunnerSet.Generation, storedIntegrityHash) {
		ephemeralRunnerIntegrityHash := ephemeralRunnerSetIntegrityHash(&ephemeralRunnerSet)
		if storedIntegrityHash != ephemeralRunnerIntegrityHash {
			log.Info("EphemeralRunnerSpec has changed, deleting idle ephemeral runners to apply the new spec")
			if _, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
				log.Error(err, "Failed to clean up EphemeralRunners")
				return ctrl.Result{}, err
			}

			if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy != nil {
				var proxySecret corev1.Secret
				err := r.Get(
					ctx,
					types.NamespacedName{
						Namespace: ephemeralRunnerSet.Namespace,
						Name:      proxyEphemeralRunnerSetSecretName(&ephemeralRunnerSet),
					},
					&proxySecret,
				)
				switch {
				case err == nil:
					updated, err := patchEphemeralRunnerSetProxySecretData(&proxySecret)
					if err != nil {
						return ctrl.Result{}, err
					}
					if updated {
						return ctrl.Result{RequeueAfter: 1}, nil
					}
				case kerrors.IsNotFound(err):
					log.Info("Creating a ephemeralRunnerSet proxy secret for the runner pods")
					if err := r.createProxySecret(ctx, &ephemeralRunnerSet, log); err != nil {
						return ctrl.Result{}, fmt.Errorf("failed to create ephemeralRunnerSet proxy secret: %w", err)
					}
				default:
					log.Error(err, "Unable to get ephemeralRunnerSet proxy secret", "namespace", ephemeralRunnerSet.Namespace, "name", proxyEphemeralRunnerSetSecretName(&ephemeralRunnerSet))
					return ctrl.Result{}, err
				}
			}

			log.Info("Updating EphemeralRunnerSet with new spec hash")
			original := ephemeralRunnerSet.DeepCopy()
			if ephemeralRunnerSet.Annotations == nil {
				ephemeralRunnerSet.Annotations = make(map[string]string)
			}
			ephemeralRunnerSet.Annotations[AnnotationKeyIntegrityHash] = ephemeralRunnerIntegrityHash
			if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to update ephemeral runner set with new spec hash")
				return ctrl.Result{}, err
			}
			r.setSpecHashCache(req.NamespacedName, ephemeralRunnerSet.UID, ephemeralRunnerSet.Generation, ephemeralRunnerIntegrityHash)

			log.Info("Updated ephemeral runner set with new spec hash")
			return ctrl.Result{}, nil
		}

		r.setSpecHashCache(req.NamespacedName, ephemeralRunnerSet.UID, ephemeralRunnerSet.Generation, ephemeralRunnerIntegrityHash)
	}

	if ephemeralRunnerSet.Status.Phase == v1alpha1.EphemeralRunnerSetPhaseOutdated {
		if _, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	var proxySecret *corev1.Secret
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy != nil {
		var secret corev1.Secret
		err := r.Get(
			ctx,
			types.NamespacedName{
				Namespace: ephemeralRunnerSet.Namespace,
				Name:      proxyEphemeralRunnerSetSecretName(&ephemeralRunnerSet),
			},
			&secret,
		)
		proxySecret = &secret
		switch {
		case err == nil:
			updated, err := patchEphemeralRunnerSetProxySecretData(proxySecret)
			if err != nil {
				return ctrl.Result{}, err
			}
			if updated {
				return ctrl.Result{RequeueAfter: 1}, nil
			}
		case kerrors.IsNotFound(err):
			// Create a compiled secret for the runner pods in the runnerset namespace
			log.Info("Creating a ephemeralRunnerSet proxy secret for the runner pods")
			if err := r.createProxySecret(ctx, &ephemeralRunnerSet, log); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create ephemeralRunnerSet proxy secret: %w", err)
			}
			return ctrl.Result{}, nil
		default:
			log.Error(err, "Unable to get ephemeralRunnerSet proxy secret", "namespace", ephemeralRunnerSet.Namespace, "name", proxyEphemeralRunnerSetSecretName(&ephemeralRunnerSet))
			return ctrl.Result{}, err
		}
	}

	// Find all EphemeralRunner with matching namespace and own by this EphemeralRunnerSet.
	var ephemeralRunnerList v1alpha1.EphemeralRunnerList
	if err := r.List(
		ctx,
		&ephemeralRunnerList,
		client.InNamespace(req.Namespace),
		client.MatchingFields{resourceOwnerKey: req.Name},
	); err != nil {
		log.Error(err, "Unable to list child ephemeral runners")
		return ctrl.Result{}, err
	}

	runnerState := newEphemeralRunnersByStates(&ephemeralRunnerList, false, ephemeralRunnerSet.Spec.PatchID != 0)

	log.Info(
		"Ephemeral runner counts",
		"outdated", runnerState.outdatedCount,
		"pending", runnerState.pendingCount,
		"running", runnerState.runningCount,
		"finished", runnerState.finishedCount,
		"failed", runnerState.failedCount,
		"deleting", runnerState.deletingCount,
	)

	if r.PublishMetrics {
		githubConfigURL := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.GitHubConfigURL
		parsedURL, err := actions.ParseGitHubConfigFromURL(githubConfigURL)
		if err != nil {
			log.Error(err, "Github Config URL is invalid", "URL", githubConfigURL)
			// stop reconciling on this object
			return ctrl.Result{}, nil
		}

		metrics.SetEphemeralRunnerCountsByStatus(
			metrics.CommonLabels{
				Name:         ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetName],
				Namespace:    ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetNamespace],
				Repository:   parsedURL.Repository,
				Organization: parsedURL.Organization,
				Enterprise:   parsedURL.Enterprise,
			},
			runnerState.pendingCount,
			runnerState.runningCount,
			runnerState.failedCount,
		)
	}

	total := runnerState.scaleTotal()
	if ephemeralRunnerSet.Spec.PatchID == 0 || ephemeralRunnerSet.Spec.PatchID != runnerState.latestPatchID {
		var fullState *ephemeralRunnersByState
		getFullState := func() *ephemeralRunnersByState {
			if fullState == nil {
				fullState = newEphemeralRunnersByStates(&ephemeralRunnerList, true, true)
			}
			return fullState
		}

		if runnerState.finishedCount > 0 {
			defer func() {
				if err := r.cleanupFinishedEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
					log.Error(err, "failed to cleanup finished ephemeral runners")
				}
			}()
		}

		log.Info("Scaling comparison", "current", total, "desired", ephemeralRunnerSet.Spec.Replicas)
		switch {
		case total < ephemeralRunnerSet.Spec.Replicas: // Handle scale up
			count := ephemeralRunnerSet.Spec.Replicas - total
			log.Info("Creating new ephemeral runners (scale up)", "count", count)
			if err := r.createEphemeralRunners(ctx, &ephemeralRunnerSet, count, log); err != nil {
				log.Error(err, "failed to make ephemeral runner")
				return ctrl.Result{}, err
			}

		case ephemeralRunnerSet.Spec.PatchID > 0 && total >= ephemeralRunnerSet.Spec.Replicas: // Handle scale down scenario.
			// If ephemeral runner did not yet update the phase to succeeded, but the scale down
			// request is issued, we should ignore the scale down request.
			// Eventually, the ephemeral runner will be cleaned up on the next patch request, which happens
			// on the next batch
		case ephemeralRunnerSet.Spec.PatchID == 0 && total > ephemeralRunnerSet.Spec.Replicas:
			stateWithSlices := getFullState()
			count := total - ephemeralRunnerSet.Spec.Replicas
			log.Info("Deleting ephemeral runners (scale down)", "count", count)
			if err := r.deleteIdleEphemeralRunners(
				ctx,
				&ephemeralRunnerSet,
				stateWithSlices.pending,
				stateWithSlices.running,
				count,
				log,
			); err != nil {
				log.Error(err, "failed to delete idle runners")
				return ctrl.Result{}, err
			}
		}
	}

	if err := r.updateStatus(ctx, &ephemeralRunnerSet, runnerState, log); err != nil {
		return ctrl.Result{}, err
	}

	if proxySecret != nil {
		expectedScaleSetName := ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetName]
		expectedScaleSetNamespace := ephemeralRunnerSet.Labels[LabelKeyGitHubScaleSetNamespace]
		labelsChanged := proxySecret.Labels[LabelKeyGitHubScaleSetName] != expectedScaleSetName || proxySecret.Labels[LabelKeyGitHubScaleSetNamespace] != expectedScaleSetNamespace

		if labelsChanged {
			updatedProxySecret := proxySecret.DeepCopy()
			updatedProxySecret.Labels = r.filterAndMergeLabels(proxySecret.Labels, map[string]string{
				LabelKeyGitHubScaleSetName:      expectedScaleSetName,
				LabelKeyGitHubScaleSetNamespace: expectedScaleSetNamespace,
			})

			log.Info("Updating ephemeralRunnerSet proxy secret metadata")
			if err := r.Patch(ctx, updatedProxySecret, client.MergeFrom(proxySecret)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update ephemeralRunnerSet proxy secret metadata: %w", err)
			}
			return ctrl.Result{RequeueAfter: 1}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerSetReconciler) setSpecHashCache(namespacedName types.NamespacedName, uid types.UID, generation int64, hash string) {
	r.specHashCache.Store(namespacedName, specHashCacheEntry{uid: uid, generation: generation, hash: hash})
}

func (r *EphemeralRunnerSetReconciler) hasSpecHashCache(namespacedName types.NamespacedName, uid types.UID, generation int64, hash string) bool {
	entry, ok := r.specHashCache.Load(namespacedName)
	if !ok {
		return false
	}

	specHashEntry, ok := entry.(specHashCacheEntry)
	if !ok {
		return false
	}

	return specHashEntry.uid == uid && specHashEntry.generation == generation && specHashEntry.hash == hash
}

func (r *EphemeralRunnerSetReconciler) clearSpecHashCache(namespacedName types.NamespacedName) {
	r.specHashCache.Delete(namespacedName)
}

func (r *EphemeralRunnerSetReconciler) updateStatus(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, state *ephemeralRunnersByState, log logr.Logger) error {
	total := state.scaleTotal()
	var phase v1alpha1.EphemeralRunnerSetPhase
	switch {
	case state.outdatedCount > 0:
		phase = v1alpha1.EphemeralRunnerSetPhaseOutdated
	case ephemeralRunnerSet.Status.Phase == "":
		phase = v1alpha1.EphemeralRunnerSetPhaseRunning
	default:
		phase = ephemeralRunnerSet.Status.Phase
	}
	desiredStatus := v1alpha1.EphemeralRunnerSetStatus{
		CurrentReplicas:         total,
		Phase:                   phase,
		PendingEphemeralRunners: state.pendingCount,
		RunningEphemeralRunners: state.runningCount,
		FailedEphemeralRunners:  state.failedCount,
	}

	// Update the status if needed.
	if ephemeralRunnerSet.Status != desiredStatus {
		updated := ephemeralRunnerSet.DeepCopy()
		updated.Status = desiredStatus
		if err := r.Status().Patch(ctx, updated, client.MergeFrom(ephemeralRunnerSet)); err != nil {
			log.Error(err, "Failed to update EphemeralRunnerSet status")
			return err
		}
		log.Info("Updated EphemeralRunnerSet status", "status", updated.Status)

	}
	return nil
}

func (r *EphemeralRunnerSetReconciler) cleanupFinishedEphemeralRunners(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) error {
	log.Info("Deleting finished ephemeral runners")
	if err := r.DeleteAllOf(
		ctx,
		&v1alpha1.EphemeralRunner{},
		client.InNamespace(ephemeralRunnerSet.Namespace),
		client.MatchingLabels{
			LabelKeyEphemeralRunnerSetUID: string(ephemeralRunnerSet.UID),
		},
		client.MatchingFields{"status.phase": string(v1alpha1.EphemeralRunnerPhaseSucceeded)},
	); err != nil {
		return fmt.Errorf("failed to delete finished ephemeral runners: %w", err)
	}
	return nil
}

func (r *EphemeralRunnerSetReconciler) cleanUpProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) error {
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy == nil {
		return nil
	}
	log.Info("Deleting proxy secret")

	proxySecret := new(corev1.Secret)
	proxySecret.Namespace = ephemeralRunnerSet.Namespace
	proxySecret.Name = proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet)

	if err := r.Delete(ctx, proxySecret); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete proxy secret: %w", err)
	}

	log.Info("Deleted proxy secret")

	return nil
}

func (r *EphemeralRunnerSetReconciler) cleanUpEphemeralRunners(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (bool, error) {
	ephemeralRunnerList := new(v1alpha1.EphemeralRunnerList)
	err := r.List(ctx, ephemeralRunnerList, client.InNamespace(ephemeralRunnerSet.Namespace), client.MatchingFields{resourceOwnerKey: ephemeralRunnerSet.Name})
	if err != nil {
		return false, fmt.Errorf("failed to list child ephemeral runners: %w", err)
	}

	// only if there are no ephemeral runners left, return true
	if len(ephemeralRunnerList.Items) == 0 {
		err := r.cleanUpProxySecret(ctx, ephemeralRunnerSet, log)
		if err != nil {
			return false, err
		}
		log.Info("All ephemeral runners are deleted")
		return true, nil
	}

	ephemeralRunnerState := newEphemeralRunnersByStates(ephemeralRunnerList, true, false)

	log.Info(
		"Clean up runner counts",
		"pending", ephemeralRunnerState.pendingCount,
		"running", ephemeralRunnerState.runningCount,
		"finished", ephemeralRunnerState.finishedCount,
		"failed", ephemeralRunnerState.failedCount,
		"deleting", ephemeralRunnerState.deletingCount,
		"outdated", ephemeralRunnerState.outdatedCount,
	)

	log.Info("Cleanup terminated ephemeral runners")
	var errs []error
	for _, ephemeralRunner := range ephemeralRunnerState.finished {
		log.Info("Deleting ephemeral runner", "name", ephemeralRunner.Name)
		if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}

	for _, ephemeralRunner := range ephemeralRunnerState.failed {
		log.Info("Deleting ephemeral runner", "name", ephemeralRunner.Name)
		if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}

	for _, ephemeralRunner := range ephemeralRunnerState.outdated {
		log.Info("Deleting ephemeral runner", "name", ephemeralRunner.Name)
		if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		mergedErrs := multierr.Combine(errs...)
		log.Error(mergedErrs, "Failed to delete ephemeral runners")
		return false, mergedErrs
	}

	// avoid fetching the client if we have nothing left to do
	if len(ephemeralRunnerState.running) == 0 && len(ephemeralRunnerState.pending) == 0 {
		return false, nil
	}

	actionsClient, err := r.GetActionsService(ctx, ephemeralRunnerSet)
	if err != nil {
		return false, err
	}

	log.Info("Cleanup pending or running ephemeral runners")
	errs = errs[0:0]
	for _, ephemeralRunner := range ephemeralRunnerState.pending {
		log.Info("Removing the ephemeral runner from the service", "name", ephemeralRunner.Name)
		_, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
	}

	for _, ephemeralRunner := range ephemeralRunnerState.running {
		if ephemeralRunner.HasJob() {
			log.Info(
				"Skipping ephemeral runner since it is running a job",
				"name", ephemeralRunner.Name,
				"workflowRunId", ephemeralRunner.Status.WorkflowRunID,
				"jobId", ephemeralRunner.Status.JobID,
			)
			continue
		}

		log.Info("Removing the idle ephemeral runner from the service", "name", ephemeralRunner.Name)
		_, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		mergedErrs := multierr.Combine(errs...)
		log.Error(mergedErrs, "Failed to remove ephemeral runners from the service")
		return false, mergedErrs
	}

	return false, nil
}

func (r *EphemeralRunnerSetReconciler) cleanUpEphemeralRunnerSetProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (done bool, err error) {
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy == nil {
		return true, nil
	}
	var proxySecret corev1.Secret
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: ephemeralRunnerSet.Namespace,
			Name:      proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
		},
		&proxySecret,
	)
	switch {
	case err == nil:
		log.Info("Deleting proxy secret")
		if err := r.Delete(ctx, &proxySecret); err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to delete proxy secret")
			return false, err
		}
		log.Info("Deleted proxy secret")
		return false, nil
	case kerrors.IsNotFound(err):
		log.Info("Proxy secret already deleted")
		return true, nil
	default:
		log.Error(
			err,
			"Unable to get ephemeralRunnerSet proxy secret",
			"namespace",
			ephemeralRunnerSet.Namespace,
			"name",
			proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
		)
		return false, err
	}
}

// createEphemeralRunners provisions `count` number of v1alpha1.EphemeralRunner resources in the cluster.
func (r *EphemeralRunnerSetReconciler) createEphemeralRunners(ctx context.Context, runnerSet *v1alpha1.EphemeralRunnerSet, count int, log logr.Logger) error {
	// Track multiple errors at once and return the bundle.
	errs := make([]error, 0)
	for i := range count {
		ephemeralRunner, err := r.newEphemeralRunner(runnerSet)
		if err != nil {
			log.Error(err, "failed to build ephemeral runner")
			errs = append(errs, err)
			continue
		}
		if runnerSet.Spec.EphemeralRunnerSpec.Proxy != nil {
			ephemeralRunner.Spec.ProxySecretRef = proxyEphemeralRunnerSetSecretName(runnerSet)
		}

		if err := r.Create(ctx, ephemeralRunner); err != nil {
			log.Error(err, "failed to make ephemeral runner")
			errs = append(errs, err)
			continue
		}

		log.Info("Created new ephemeral runner", "progress", i+1, "total", count, "runner", ephemeralRunner.Name)
	}

	return multierr.Combine(errs...)
}

func (r *EphemeralRunnerSetReconciler) createProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) error {
	proxySecretData, err := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
		secret := new(corev1.Secret)
		err := r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: s}, secret)
		return secret, err
	})
	if err != nil {
		return fmt.Errorf("failed to convert proxy config to secret data: %w", err)
	}

	runnerPodProxySecret, err := r.newEphemeralRunnerSetProxySecret(ephemeralRunnerSet, proxySecretData)
	if err != nil {
		log.Error(err, "failed to build proxy secret")
		return err
	}

	log.Info("Creating new proxy secret")
	if err := r.Create(ctx, runnerPodProxySecret); err != nil {
		log.Error(err, "failed to create proxy secret")
		return err
	}

	log.Info("Created new proxy secret")
	return nil
}

// deleteIdleEphemeralRunners try to deletes `count` number of v1alpha1.EphemeralRunner resources in the cluster.
// It will only delete `v1alpha1.EphemeralRunner` that has registered with Actions service
// which has a `v1alpha1.EphemeralRunner.Status.RunnerId` set.
// So, it is possible that this function will not delete enough ephemeral runners
// if there are not enough ephemeral runners that have registered with Actions service.
// When this happens, the next reconcile loop will try to delete the remaining ephemeral runners
// after we get notified by any of the `v1alpha1.EphemeralRunner.Status` updates.
func (r *EphemeralRunnerSetReconciler) deleteIdleEphemeralRunners(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, pendingEphemeralRunners, runningEphemeralRunners []*v1alpha1.EphemeralRunner, count int, log logr.Logger) error {
	if count <= 0 {
		return nil
	}
	runners := newEphemeralRunnerStepper(pendingEphemeralRunners, runningEphemeralRunners)
	if runners.len() == 0 {
		log.Info("No pending or running ephemeral runners running at this time for scale down")
		return nil
	}
	actionsClient, err := r.GetActionsService(ctx, ephemeralRunnerSet)
	if err != nil {
		return fmt.Errorf("failed to create actions client for ephemeral runner replica set: %w", err)
	}
	var errs []error
	deletedCount := 0
	for runners.next() {
		ephemeralRunner := runners.object()
		isDone := ephemeralRunner.IsDone()
		if !isDone && ephemeralRunner.Status.RunnerID == 0 {
			log.Info("Skipping ephemeral runner since it is not registered yet", "name", ephemeralRunner.Name)
			continue
		}

		if !isDone && ephemeralRunner.HasJob() {
			log.Info(
				"Skipping ephemeral runner since it is running a job",
				"name", ephemeralRunner.Name,
				"workflowRunId", ephemeralRunner.Status.WorkflowRunID,
				"jobId", ephemeralRunner.Status.JobID,
			)
			continue
		}

		log.Info("Removing the idle ephemeral runner", "name", ephemeralRunner.Name)
		ok, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
		if !ok {
			continue
		}

		deletedCount++
		if deletedCount == count {
			break
		}
	}

	return multierr.Combine(errs...)
}

func (r *EphemeralRunnerSetReconciler) deleteEphemeralRunnerWithActionsClient(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, actionsClient multiclient.Client, log logr.Logger) (bool, error) {
	if err := actionsClient.RemoveRunner(ctx, int64(ephemeralRunner.Status.RunnerID)); err != nil {
		if errors.Is(err, scaleset.JobStillRunningError) {
			log.Info("Runner is still running a job, skipping deletion", "name", ephemeralRunner.Name, "runnerId", ephemeralRunner.Status.RunnerID)
			return false, nil
		}

		return false, err
	}

	log.Info("Deleting ephemeral runner after removing from the service", "name", ephemeralRunner.Name, "runnerId", ephemeralRunner.Status.RunnerID)
	if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
		return false, err
	}

	log.Info("Deleted ephemeral runner", "name", ephemeralRunner.Name, "runnerId", ephemeralRunner.Status.RunnerID)
	return true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager, opts ...Option) error {
	r.setSchemeIfUnset(r.Scheme)

	return builderWithOptions(
		ctrl.NewControllerManagedBy(mgr).
			For(&v1alpha1.EphemeralRunnerSet{}).
			Owns(&v1alpha1.EphemeralRunner{}).
			WithEventFilter(predicate.ResourceVersionChangedPredicate{}),
		opts,
	).Complete(r)
}

type ephemeralRunnerStepper struct {
	buckets     [][]*v1alpha1.EphemeralRunner
	bucketIndex int
	itemIndex   int
}

func newEphemeralRunnerStepper(primary []*v1alpha1.EphemeralRunner, othersOrdered ...[]*v1alpha1.EphemeralRunner) *ephemeralRunnerStepper {
	sort.Slice(primary, func(i, j int) bool {
		return primary[i].GetCreationTimestamp().Time.Before(primary[j].GetCreationTimestamp().Time)
	})

	buckets := make([][]*v1alpha1.EphemeralRunner, 0, len(othersOrdered)+1)
	buckets = append(buckets, primary)

	for _, bucket := range othersOrdered {
		sort.Slice(bucket, func(i, j int) bool {
			return bucket[i].GetCreationTimestamp().Time.Before(bucket[j].GetCreationTimestamp().Time)
		})
		buckets = append(buckets, bucket)
	}

	return &ephemeralRunnerStepper{
		buckets:     buckets,
		bucketIndex: 0,
		itemIndex:   -1,
	}
}

func (s *ephemeralRunnerStepper) next() bool {
	for s.bucketIndex < len(s.buckets) {
		if s.itemIndex+1 < len(s.buckets[s.bucketIndex]) {
			s.itemIndex++
			return true
		}

		s.bucketIndex++
		s.itemIndex = -1
	}

	return false
}

func (s *ephemeralRunnerStepper) object() *v1alpha1.EphemeralRunner {
	if s.bucketIndex >= 0 && s.bucketIndex < len(s.buckets) && s.itemIndex >= 0 && s.itemIndex < len(s.buckets[s.bucketIndex]) {
		return s.buckets[s.bucketIndex][s.itemIndex]
	}

	return nil
}

func (s *ephemeralRunnerStepper) len() int {
	var total int
	for i := range s.buckets {
		total += len(s.buckets[i])
	}

	return total
}

type ephemeralRunnersByState struct {
	pending  []*v1alpha1.EphemeralRunner
	running  []*v1alpha1.EphemeralRunner
	finished []*v1alpha1.EphemeralRunner
	failed   []*v1alpha1.EphemeralRunner
	deleting []*v1alpha1.EphemeralRunner
	outdated []*v1alpha1.EphemeralRunner

	pendingCount  int
	runningCount  int
	finishedCount int
	failedCount   int
	deletingCount int
	outdatedCount int

	latestPatchID int
}

func newEphemeralRunnersByStates(ephemeralRunnerList *v1alpha1.EphemeralRunnerList, includeSlices bool, trackLatestPatchID bool) *ephemeralRunnersByState {
	var ephemeralRunnerState ephemeralRunnersByState

	for i := range ephemeralRunnerList.Items {
		r := &ephemeralRunnerList.Items[i]
		if trackLatestPatchID {
			patchID, err := strconv.Atoi(r.Annotations[AnnotationKeyPatchID])
			if err == nil && patchID > ephemeralRunnerState.latestPatchID {
				ephemeralRunnerState.latestPatchID = patchID
			}
		}
		if !r.DeletionTimestamp.IsZero() {
			ephemeralRunnerState.deletingCount++
			continue
		}

		switch r.Status.Phase {
		case v1alpha1.EphemeralRunnerPhaseRunning:
			ephemeralRunnerState.runningCount++
		case v1alpha1.EphemeralRunnerPhaseSucceeded:
			ephemeralRunnerState.finishedCount++
		case v1alpha1.EphemeralRunnerPhaseFailed:
			ephemeralRunnerState.failedCount++
		case v1alpha1.EphemeralRunnerPhaseOutdated:
			ephemeralRunnerState.outdatedCount++
		default:
			// Pending or no phase should be considered as pending.
			//
			// If field is not set, that means that the EphemeralRunner
			// did not yet have chance to update the Status.Phase field.
			ephemeralRunnerState.pendingCount++
		}
	}

	if !includeSlices {
		return &ephemeralRunnerState
	}

	ephemeralRunnerState.pending = make([]*v1alpha1.EphemeralRunner, 0, ephemeralRunnerState.pendingCount)
	ephemeralRunnerState.running = make([]*v1alpha1.EphemeralRunner, 0, ephemeralRunnerState.runningCount)
	ephemeralRunnerState.finished = make([]*v1alpha1.EphemeralRunner, 0, ephemeralRunnerState.finishedCount)
	ephemeralRunnerState.failed = make([]*v1alpha1.EphemeralRunner, 0, ephemeralRunnerState.failedCount)
	ephemeralRunnerState.deleting = make([]*v1alpha1.EphemeralRunner, 0, ephemeralRunnerState.deletingCount)
	ephemeralRunnerState.outdated = make([]*v1alpha1.EphemeralRunner, 0, ephemeralRunnerState.outdatedCount)

	for i := range ephemeralRunnerList.Items {
		r := &ephemeralRunnerList.Items[i]
		if !r.DeletionTimestamp.IsZero() {
			ephemeralRunnerState.deleting = append(ephemeralRunnerState.deleting, r)
			continue
		}

		switch r.Status.Phase {
		case v1alpha1.EphemeralRunnerPhaseRunning:
			ephemeralRunnerState.running = append(ephemeralRunnerState.running, r)
		case v1alpha1.EphemeralRunnerPhaseSucceeded:
			ephemeralRunnerState.finished = append(ephemeralRunnerState.finished, r)
		case v1alpha1.EphemeralRunnerPhaseFailed:
			ephemeralRunnerState.failed = append(ephemeralRunnerState.failed, r)
		case v1alpha1.EphemeralRunnerPhaseOutdated:
			ephemeralRunnerState.outdated = append(ephemeralRunnerState.outdated, r)
		default:
			ephemeralRunnerState.pending = append(ephemeralRunnerState.pending, r)
		}
	}

	return &ephemeralRunnerState
}

func (s *ephemeralRunnersByState) scaleTotal() int {
	return s.pendingCount + s.runningCount + s.failedCount
}
