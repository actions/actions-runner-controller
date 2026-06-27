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
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	ResourceBuilder
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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	original := ephemeralRunnerSet.DeepCopy()

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
		if controllerutil.RemoveFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
			if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to update ephemeral runner set with removed finalizer")
				return ctrl.Result{}, err
			}
		}

		log.Info("Successfully removed finalizer after cleanup")
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if controllerutil.AddFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
		log.Info("Adding finalizer")
		if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update ephemeral runner set with new finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	// If hash spec has changed, delete idle ephemeral runners
	// in order to apply the change to the runners that did not yet receive a job.
	ephemeralRunnerIntegrityHash := ephemeralRunnerSetIntegrityHash(&ephemeralRunnerSet)
	if ephemeralRunnerSet.Annotations[annotationKeyIntegrityHash] != ephemeralRunnerIntegrityHash {
		log.Info("EphemeralRunnerSpec has changed, deleting idle ephemeral runners to apply the new spec")
		if _, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}

		if _, _, err := r.reconcileEphemeralRunnerSetProxySecret(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to update EphemeralRunnerSet proxy secret")
			return ctrl.Result{}, err
		}

		log.Info("Updating EphemeralRunnerSet with new spec hash")
		original := ephemeralRunnerSet.DeepCopy()
		if ephemeralRunnerSet.Annotations == nil {
			ephemeralRunnerSet.Annotations = make(map[string]string)
		}
		ephemeralRunnerSet.Annotations[annotationKeyIntegrityHash] = ephemeralRunnerIntegrityHash
		if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update ephemeral runner set with new spec hash")
			return ctrl.Result{}, err
		}

		log.Info("Updated ephemeral runner set with new spec hash")
		return ctrl.Result{}, nil
	}

	if ephemeralRunnerSet.Status.Phase == v1alpha1.EphemeralRunnerSetPhaseOutdated {
		if _, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Create or update proxy secret if needed
	if _, updated, err := r.reconcileEphemeralRunnerSetProxySecret(ctx, &ephemeralRunnerSet, log); err != nil {
		log.Error(err, "Unable to reconcile ephemeralRunnerSet proxy secret", "namespace", ephemeralRunnerSet.Namespace, "name", proxyEphemeralRunnerSetSecretName(&ephemeralRunnerSet))
		return ctrl.Result{}, err
	} else if updated {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
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

	ephemeralRunnersByState := newEphemeralRunnersByStates(&ephemeralRunnerList)

	log.Info(
		"Ephemeral runner counts",
		"outdated", len(ephemeralRunnersByState.outdated),
		"pending", len(ephemeralRunnersByState.pending),
		"running", len(ephemeralRunnersByState.running),
		"finished", len(ephemeralRunnersByState.finished),
		"failed", len(ephemeralRunnersByState.failed),
		"deleting", len(ephemeralRunnersByState.deleting),
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
			len(ephemeralRunnersByState.pending),
			len(ephemeralRunnersByState.running),
			len(ephemeralRunnersByState.failed),
		)
	}

	total := ephemeralRunnersByState.scaleTotal()
	if ephemeralRunnerSet.Spec.PatchID == 0 || ephemeralRunnerSet.Spec.PatchID != ephemeralRunnersByState.latestPatchID {
		defer func() {
			if err := r.cleanupFinishedEphemeralRunners(ctx, ephemeralRunnersByState.finished, log); err != nil {
				log.Error(err, "failed to cleanup finished ephemeral runners")
			}
		}()
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
			count := total - ephemeralRunnerSet.Spec.Replicas
			log.Info("Deleting ephemeral runners (scale down)", "count", count)
			if err := r.deleteIdleEphemeralRunners(
				ctx,
				&ephemeralRunnerSet,
				ephemeralRunnersByState.pending,
				ephemeralRunnersByState.running,
				count,
				log,
			); err != nil {
				log.Error(err, "failed to delete idle runners")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, r.updateStatus(ctx, &ephemeralRunnerSet, ephemeralRunnersByState, log)
}

func (r *EphemeralRunnerSetReconciler) updateStatus(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, state *ephemeralRunnersByState, log logr.Logger) error {
	original := ephemeralRunnerSet.DeepCopy()
	total := state.scaleTotal()
	var phase v1alpha1.EphemeralRunnerSetPhase
	switch {
	case len(state.outdated) > 0:
		phase = v1alpha1.EphemeralRunnerSetPhaseOutdated
	case ephemeralRunnerSet.Status.Phase == "":
		phase = v1alpha1.EphemeralRunnerSetPhaseRunning
	default:
		phase = ephemeralRunnerSet.Status.Phase
	}
	ephemeralRunnerSet.Status.CurrentReplicas = total
	ephemeralRunnerSet.Status.Phase = phase
	ephemeralRunnerSet.Status.PendingEphemeralRunners = len(state.pending)
	ephemeralRunnerSet.Status.RunningEphemeralRunners = len(state.running)
	ephemeralRunnerSet.Status.FailedEphemeralRunners = len(state.failed)
	ephemeralRunnerSet.Status.ObservedGeneration = ephemeralRunnerSet.Generation

	readyStatus := metav1.ConditionTrue
	message := "EphemeralRunnerSet is running"
	if phase == v1alpha1.EphemeralRunnerSetPhaseOutdated {
		readyStatus = metav1.ConditionFalse
		message = "EphemeralRunnerSet contains outdated ephemeral runners"
	}
	setReadyCondition(
		&ephemeralRunnerSet.Status.Conditions,
		ephemeralRunnerSet.Generation,
		readyStatus,
		string(phase),
		message,
	)

	// Update the status if needed.
	if !apiequality.Semantic.DeepEqual(original.Status, ephemeralRunnerSet.Status) {
		if err := r.Status().Patch(ctx, ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update EphemeralRunnerSet status")
			return err
		}
		log.Info("Updated EphemeralRunnerSet status", "status", ephemeralRunnerSet.Status)

	}
	return nil
}

func (r *EphemeralRunnerSetReconciler) cleanupFinishedEphemeralRunners(ctx context.Context, finishedEphemeralRunners []*v1alpha1.EphemeralRunner, log logr.Logger) error {
	// cleanup finished runners and proceed
	var errs []error
	for i := range finishedEphemeralRunners {
		log.Info("Deleting finished ephemeral runner", "name", finishedEphemeralRunners[i].Name)
		if err := r.Delete(ctx, finishedEphemeralRunners[i]); err != nil {
			if !kerrors.IsNotFound(err) {
				errs = append(errs, err)
			}
		}
	}

	return multierr.Combine(errs...)
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

	ephemeralRunnerState := newEphemeralRunnersByStates(ephemeralRunnerList)

	log.Info(
		"Clean up runner counts",
		"pending", len(ephemeralRunnerState.pending),
		"running", len(ephemeralRunnerState.running),
		"finished", len(ephemeralRunnerState.finished),
		"failed", len(ephemeralRunnerState.failed),
		"deleting", len(ephemeralRunnerState.deleting),
		"outdated", len(ephemeralRunnerState.outdated),
	)

	log.Info("Cleanup terminated ephemeral runners")
	var errs []error
	for _, ephemeralRunner := range ephemeralRunnerState.terminated() {
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

func (r *EphemeralRunnerSetReconciler) reconcileEphemeralRunnerSetProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (secret *corev1.Secret, updated bool, err error) {
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy == nil {
		return nil, false, nil
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
		proxySecretData, err := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
			secret := new(corev1.Secret)
			err := r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: s}, secret)
			return secret, err
		})
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert proxy config to secret data: %w", err)
		}

		desiredRunnerSetProxy, err := r.newEphemeralRunnerSetProxySecret(ephemeralRunnerSet, proxySecretData)
		if err != nil {
			return nil, false, fmt.Errorf("failed to build desired ephemeralRunnerSet proxy secret: %w", err)
		}

		updatedProxySecret := proxySecret.DeepCopy()
		var shouldUpdate bool
		if !maps.EqualFunc(proxySecret.Data, desiredRunnerSetProxy.Data, bytes.Equal) {
			updatedProxySecret.Data = desiredRunnerSetProxy.Data
			shouldUpdate = true
		}
		desiredLabels := r.filterAndMergeLabels(proxySecret.Labels, desiredRunnerSetProxy.Labels)
		if !maps.Equal(proxySecret.Labels, desiredLabels) {
			updatedProxySecret.Labels = desiredLabels
			shouldUpdate = true
		}
		desiredAnnotations := r.mergeAnnotations(proxySecret.Annotations, desiredRunnerSetProxy.Annotations)
		if !maps.Equal(proxySecret.Annotations, desiredAnnotations) {
			updatedProxySecret.Annotations = desiredAnnotations
			shouldUpdate = true
		}
		if shouldUpdate {
			log.Info("Updating ephemeralRunnerSet proxy secret")
			if err := r.Update(ctx, updatedProxySecret); err != nil {
				return nil, false, fmt.Errorf("failed to update ephemeralRunnerSet proxy secret: %w", err)
			}
			return updatedProxySecret, true, nil
		}
		return &proxySecret, false, nil
	case kerrors.IsNotFound(err):
		// Create a compiled secret for the runner pods in the runnerset namespace
		log.Info("Creating a ephemeralRunnerSet proxy secret for the runner pods")
		if err := r.createProxySecret(ctx, ephemeralRunnerSet, log); err != nil {
			return nil, false, fmt.Errorf("failed to create ephemeralRunnerSet proxy secret: %w", err)
		}
		return nil, false, nil
	default:
		return nil, false, err
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

		log.Info("Creating new ephemeral runner", "progress", i+1, "total", count)
		if err := r.Create(ctx, ephemeralRunner); err != nil {
			log.Error(err, "failed to make ephemeral runner")
			errs = append(errs, err)
			continue
		}

		log.Info("Created new ephemeral runner", "runner", ephemeralRunner.Name)
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
	items []*v1alpha1.EphemeralRunner
	index int
}

func newEphemeralRunnerStepper(primary []*v1alpha1.EphemeralRunner, othersOrdered ...[]*v1alpha1.EphemeralRunner) *ephemeralRunnerStepper {
	sort.Slice(primary, func(i, j int) bool {
		return primary[i].GetCreationTimestamp().Time.Before(primary[j].GetCreationTimestamp().Time)
	})
	for _, bucket := range othersOrdered {
		sort.Slice(bucket, func(i, j int) bool {
			return bucket[i].GetCreationTimestamp().Time.Before(bucket[j].GetCreationTimestamp().Time)
		})
	}

	for _, bucket := range othersOrdered {
		primary = append(primary, bucket...)
	}

	return &ephemeralRunnerStepper{
		items: primary,
		index: -1,
	}
}

func (s *ephemeralRunnerStepper) next() bool {
	if s.index+1 < len(s.items) {
		s.index++
		return true
	}
	return false
}

func (s *ephemeralRunnerStepper) object() *v1alpha1.EphemeralRunner {
	if s.index >= 0 && s.index < len(s.items) {
		return s.items[s.index]
	}
	return nil
}

func (s *ephemeralRunnerStepper) len() int {
	return len(s.items)
}

type ephemeralRunnersByState struct {
	pending  []*v1alpha1.EphemeralRunner
	running  []*v1alpha1.EphemeralRunner
	finished []*v1alpha1.EphemeralRunner
	failed   []*v1alpha1.EphemeralRunner
	deleting []*v1alpha1.EphemeralRunner
	outdated []*v1alpha1.EphemeralRunner

	latestPatchID int
}

func newEphemeralRunnersByStates(ephemeralRunnerList *v1alpha1.EphemeralRunnerList) *ephemeralRunnersByState {
	var ephemeralRunnerState ephemeralRunnersByState

	for i := range ephemeralRunnerList.Items {
		r := &ephemeralRunnerList.Items[i]
		patchID, err := strconv.Atoi(r.Annotations[AnnotationKeyPatchID])
		if err == nil && patchID > ephemeralRunnerState.latestPatchID {
			ephemeralRunnerState.latestPatchID = patchID
		}
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
			// Pending or no phase should be considered as pending.
			//
			// If field is not set, that means that the EphemeralRunner
			// did not yet have chance to update the Status.Phase field.
			ephemeralRunnerState.pending = append(ephemeralRunnerState.pending, r)
		}
	}
	return &ephemeralRunnerState
}

func (s *ephemeralRunnersByState) terminated() []*v1alpha1.EphemeralRunner {
	return append(s.finished, append(s.failed, s.outdated...)...)
}

func (s *ephemeralRunnersByState) scaleTotal() int {
	return len(s.pending) + len(s.running) + len(s.failed)
}
