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
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	autoscalingRunnerSetFinalizerName = "autoscalingrunnerset.actions.github.com/finalizer"
	runnerScaleSetIDAnnotationKey     = "runner-scale-set-id"
)

// AutoscalingRunnerSetReconciler reconciles a AutoscalingRunnerSet object
type AutoscalingRunnerSetReconciler struct {
	client.Client
	Log                                           logr.Logger
	Scheme                                        *runtime.Scheme
	ControllerNamespace                           string
	DefaultRunnerScaleSetListenerImage            string
	DefaultRunnerScaleSetListenerImagePullSecrets []string
	ResourceBuilder
}

// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalingrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalingrunnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalingrunnersets/finalizers,verbs=update
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners/status,verbs=get;update;patch

// Reconcile a AutoscalingRunnerSet resource to meet its desired spec.
func (r *AutoscalingRunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("autoscalingrunnerset", req.NamespacedName)

	var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &autoscalingRunnerSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	original := autoscalingRunnerSet.DeepCopy()

	if !autoscalingRunnerSet.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&autoscalingRunnerSet, autoscalingRunnerSetFinalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("Deleting resources")
		done, err := r.cleanUpResources(ctx, &autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up resources during deletion")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for resources to be cleaned up before removing finalizer")
			return ctrl.Result{
				RequeueAfter: 5 * time.Second,
			}, nil
		}

		if err := r.removeFinalizersFromDependentResources(ctx, &autoscalingRunnerSet, log); err != nil {
			log.Error(err, "Failed to remove finalizers on dependent resources")
			return ctrl.Result{}, err
		}

		if controllerutil.RemoveFinalizer(&autoscalingRunnerSet, autoscalingRunnerSetFinalizerName) {
			log.Info("Removing finalizer")
			if err := r.Patch(ctx, &autoscalingRunnerSet, client.MergeFrom(original)); err != nil && !kerrors.IsNotFound(err) {
				log.Error(err, "Failed to update autoscaling runner set without finalizer")
				return ctrl.Result{}, err
			}
		}

		log.Info("Successfully removed finalizer after cleanup")
		return ctrl.Result{}, nil
	}

	if !v1alpha1.IsVersionAllowed(autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion], build.Version) {
		if err := r.Delete(ctx, &autoscalingRunnerSet); err != nil {
			log.Error(
				err, "Failed to delete autoscaling runner set on version mismatch",
				"buildVersion", build.Version,
				"autoscalingRunnerSetVersion", autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
			)
			return ctrl.Result{}, nil
		}

		log.Info(
			"Autoscaling runner set version doesn't match the build version. Deleting the resource.",
			"buildVersion", build.Version,
			"autoscalingRunnerSetVersion", autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
		)
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(&autoscalingRunnerSet, autoscalingRunnerSetFinalizerName) {
		log.Info("Adding finalizer")

		if err := r.Patch(ctx, &autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update autoscaling runner set with finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	// Something has changed, we need to re-apply the pending phase and change hash annotation to trigger the update of runner scale set and listener.
	if targetHash := autoscalingRunnerSet.Hash(); autoscalingRunnerSet.Annotations[annotationKeyIntegrityHash] != targetHash {
		// TODO: apply the version label
		original := autoscalingRunnerSet.DeepCopy()
		if autoscalingRunnerSet.Annotations == nil {
			autoscalingRunnerSet.Annotations = map[string]string{}
		}
		autoscalingRunnerSet.Annotations[annotationKeyIntegrityHash] = targetHash
		if err := r.Patch(ctx, &autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update autoscaling runner set with new change hash and pending phase")
			return ctrl.Result{}, err
		}

		original = autoscalingRunnerSet.DeepCopy()
		autoscalingRunnerSet.Status.Phase = v1alpha1.AutoscalingRunnerSetPhasePending
		if err := r.Status().Patch(ctx, &autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update autoscaling runner set status with pending phase")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	outdated := autoscalingRunnerSet.Status.Phase == v1alpha1.AutoscalingRunnerSetPhaseOutdated
	if outdated {
		log.Info("Autoscaling runner set is in outdated phase, removing the listener")
		done, err := r.cleanupListener(ctx, &autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up listener")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for listener to be cleaned up for the outdated runner set")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		var ephemeralRunnerSet v1alpha1.EphemeralRunnerSet
		err = r.Get(
			ctx,
			types.NamespacedName{
				Namespace: autoscalingRunnerSet.Namespace,
				Name:      autoscalingRunnerSet.Name,
			},
			&ephemeralRunnerSet,
		)
		switch {
		case kerrors.IsNotFound(err):
			// If the ephemeral runner set is not found, something removed the ephemeral runner set. The ephemeral runner set should
			// not be removed by the controller once it is outdated. However, if the ephemeral runner set is removed, it means no ephemeral
			// runners should be running (or at least no ephemeral runners associated with the ephemeral runner set).
			// Therefore, this state is acceptable, because the update to the autoscaling runner set will trigger the loop
			// that will eventually create a new ephemeral runner set.
			log.Info("Ephemeral runner set is not found. Ignoring the state until the autoscaling runner set is updated")
			return ctrl.Result{}, nil
		case err != nil:
			log.Error(err, "Failed to get ephemeral runner set for the outdated runner set")
			return ctrl.Result{}, err
		default:
			if !ephemeralRunnerSet.DeletionTimestamp.IsZero() {
				// Same as NotFound case, ignore.
				return ctrl.Result{}, nil
			}

			original := ephemeralRunnerSet.DeepCopy()
			ephemeralRunnerSet.Spec.Replicas = 0
			ephemeralRunnerSet.Spec.PatchID = 0
			if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to patch ephemeral runner set with 0 replicas and reset patch ID for the outdated runner set")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	}

	if shouldCreateScaleSet(&autoscalingRunnerSet) {
		log.Info("Creating runner scale set")
		return r.createRunnerScaleSet(ctx, &autoscalingRunnerSet, log)
	}

	// A multi-variant AutoscalingRunnerSet fans out to one EphemeralRunnerSet per
	// variant behind a single listener. It manages its own per-variant scale sets,
	// so the scalar runner group / name reconciliation below (a single-scale-set
	// concept) does not apply. The single (default) variant keeps the classic
	// one-ERS path below, byte-for-byte unchanged.
	if isMultiVariant(&autoscalingRunnerSet) {
		return r.reconcileMultiVariant(ctx, &autoscalingRunnerSet, log)
	}

	// Make sure the runner group of the scale set is up to date
	currentRunnerGroupName, ok := autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName]
	if !ok || (len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 && !strings.EqualFold(currentRunnerGroupName, autoscalingRunnerSet.Spec.RunnerGroup)) {
		log.Info("AutoScalingRunnerSet runner group changed. Updating the runner scale set.")
		return r.updateRunnerScaleSetRunnerGroup(ctx, &autoscalingRunnerSet, log)
	}

	// Make sure the runner scale set name is up to date
	currentRunnerScaleSetName, ok := autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName]
	if !ok || (len(autoscalingRunnerSet.Spec.RunnerScaleSetName) > 0 && !strings.EqualFold(currentRunnerScaleSetName, autoscalingRunnerSet.Spec.RunnerScaleSetName)) {
		log.Info("AutoScalingRunnerSet runner scale set name changed. Updating the runner scale set.")
		return r.updateRunnerScaleSetName(ctx, &autoscalingRunnerSet, log)
	}

	var ephemeralRunnerSet v1alpha1.EphemeralRunnerSet
	err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: autoscalingRunnerSet.Namespace,
			Name:      autoscalingRunnerSet.Name,
		},
		&ephemeralRunnerSet,
	)
	switch {
	case kerrors.IsNotFound(err):
		log.Info("Creating ephemeral runner set")
		return r.createEphemeralRunnerSet(ctx, &autoscalingRunnerSet, log)
	case err != nil:
		log.Error(err, "Failed to get ephemeral runner")
		return ctrl.Result{}, err
	case ephemeralRunnerSet.Status.Phase == v1alpha1.EphemeralRunnerSetPhaseOutdated && autoscalingRunnerSet.Status.Phase == v1alpha1.AutoscalingRunnerSetPhaseRunning:
		// Runners are outdated. We need to stop the listener so it stops getting new jobs.
		log.Info("Ephemeral runner set is outdated. Cleaning up resources for the outdated runner set")
		done, err := r.cleanupListener(ctx, &autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up listener for outdated ephemeral runner set")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for listener to be cleaned up for the outdated ephemeral runner set")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// Then, we need to remove the ephemeral runner set to force scale-down. The ephemeral runner set
		// will eventually remove all runners as soon as possible.
		//
		// The scale set should not be removed yet, since user did not explicitly remove the scale set (or the autoscaling runner set)
		// Therefore, the autoscaling runner set should stay in outdated state until the spec is updated,
		// or until the autoscaling runner set is removed.
		done, err = r.cleanupEphemeralRunnerSet(ctx, &autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up ephemeral runner set for outdated runner set")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for ephemeral runner set to be cleaned up for the outdated runner set")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		log.Info("Successfully cleaned up resources for the outdated runner set")

		return ctrl.Result{}, nil

	default:
		desired, err := r.newEphemeralRunnerSet(&autoscalingRunnerSet)
		if err != nil {
			log.Error(err, "Failed to generate ephemeral runner set spec")
			return ctrl.Result{}, nil
		}

		if ephemeralRunnerSet.Annotations[annotationKeyIntegrityHash] != desired.Annotations[annotationKeyIntegrityHash] {
			original := ephemeralRunnerSet.DeepCopy()
			ephemeralRunnerSet.Spec.EphemeralRunnerMetadata = desired.Spec.EphemeralRunnerMetadata
			ephemeralRunnerSet.Spec.EphemeralRunnerSpec = desired.Spec.EphemeralRunnerSpec
			ephemeralRunnerSet.Labels = r.filterAndMergeLabels(ephemeralRunnerSet.Labels, desired.Labels)
			ephemeralRunnerSet.Annotations = r.mergeAnnotations(ephemeralRunnerSet.Annotations, desired.Annotations)

			log.Info("Updating ephemeral runner set spec to match the desired spec")
			if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to patch ephemeral runner set to match the desired spec")
				return ctrl.Result{}, err
			}

			log.Info("Successfully patched ephemeral runner set spec")
			return ctrl.Result{}, nil
		}

		ephemeralRunnerMetadataModified := !cmp.Equal(ephemeralRunnerSet.Spec.EphemeralRunnerMetadata, desired.Spec.EphemeralRunnerMetadata)
		ephemeralRunnerLabelsModified := !maps.Equal(ephemeralRunnerSet.Labels, desired.Labels)
		ephemeralRunnerAnnotationsModified := !maps.Equal(ephemeralRunnerSet.Annotations, desired.Annotations)

		if ephemeralRunnerLabelsModified || ephemeralRunnerAnnotationsModified || ephemeralRunnerMetadataModified {
			original := ephemeralRunnerSet.DeepCopy()
			ephemeralRunnerSet.Labels = r.filterAndMergeLabels(ephemeralRunnerSet.Labels, desired.Labels)
			ephemeralRunnerSet.Annotations = r.mergeAnnotations(ephemeralRunnerSet.Annotations, desired.Annotations)
			ephemeralRunnerSet.Spec.EphemeralRunnerMetadata = desired.Spec.EphemeralRunnerMetadata
			log.Info("Updating ephemeral runner set metadata to match desired labels and annotations")
			if err := r.Patch(ctx, &ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to patch ephemeral runner set metadata to match desired labels and annotations")
				return ctrl.Result{}, err
			}

			log.Info("Successfully patched ephemeral runner set metadata")
			return ctrl.Result{}, nil
		}
	}

	var listener v1alpha1.AutoscalingListener
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: r.ControllerNamespace,
			Name:      scaleSetListenerName(&autoscalingRunnerSet),
		},
		&listener,
	)
	switch {
	case kerrors.IsNotFound(err):
		log.Info("AutoscalingListener does not exist, creating autoscaling listener")
		return r.createAutoScalingListenerForRunnerSet(ctx, &autoscalingRunnerSet, &ephemeralRunnerSet, log)
	case err != nil:
		log.Error(err, "Failed to get AutoscalingListener resource")
		return ctrl.Result{}, err
	default:
		desired, err := r.newAutoscalingListener(
			&autoscalingRunnerSet,
			&ephemeralRunnerSet,
			r.ControllerNamespace,
			r.DefaultRunnerScaleSetListenerImage,
			nil, // TODO: remove
		)
		if err != nil {
			log.Error(err, "Failed to generate AutoscalingListener spec")
			return ctrl.Result{}, nil
		}

		if !cmp.Equal(listener.Spec, desired.Spec) ||
			!cmp.Equal(listener.Labels, desired.Labels) ||
			!cmp.Equal(listener.Annotations, desired.Annotations) {
			log.Info("Deleting AutoscalingListener to re-create with updated spec")
			if err := r.Delete(ctx, &listener); err != nil {
				log.Error(err, "Failed to delete AutoscalingListener for re-creation")
				return ctrl.Result{}, err
			}
			log.Info("Deleted AutoscalingListener, will re-create on next reconcile")
			return ctrl.Result{}, nil
		}
	}

	log.Info("Autoscaling runner set is up to date and ready")
	if err := r.updateStatus(
		ctx,
		&autoscalingRunnerSet,
		v1alpha1.AutoscalingRunnerSetPhaseRunning,
		log,
	); err != nil {
		log.Error(err, "Failed to update autoscaling runner set status to running")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) cleanUpResources(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, log logr.Logger) (bool, error) {
	log.Info("Deleting the listener")
	done, err := r.cleanupListener(ctx, autoscalingRunnerSet, log)
	if err != nil {
		log.Error(err, "Failed to clean up listener")
		return false, err
	}

	if !done {
		log.Info("Waiting for listener to be deleted")
		return false, nil
	}

	log.Info("deleting ephemeral runner sets")
	done, err = r.cleanupEphemeralRunnerSet(ctx, autoscalingRunnerSet, log)
	if err != nil {
		log.Error(err, "Failed to clean up ephemeral runner sets")
		return false, err
	}
	if !done {
		log.Info("Waiting for ephemeral runner sets to be deleted")
		return false, nil
	}

	log.Info("deleting runner scale set")
	err = r.deleteRunnerScaleSet(ctx, autoscalingRunnerSet, log)
	if err != nil {
		log.Error(err, "Failed to delete runner scale set")
		return false, err
	}

	return true, nil
}

// Update the status of autoscaling runner set if necessary
func (r *AutoscalingRunnerSetReconciler) updateStatus(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, phase v1alpha1.AutoscalingRunnerSetPhase, log logr.Logger) error {
	phaseDiff := phase != autoscalingRunnerSet.Status.Phase
	if !phaseDiff {
		return nil
	}

	original := autoscalingRunnerSet.DeepCopy()
	autoscalingRunnerSet.Status.Phase = phase

	if err := r.Status().Patch(ctx, autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
		log.Error(err, "Failed to patch autoscaling runner set status")
		return err
	}

	return nil
}

func (r *AutoscalingRunnerSetReconciler) cleanupListener(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (done bool, err error) {
	logger.Info("Cleaning up the listener")
	var listener v1alpha1.AutoscalingListener
	err = r.Get(
		ctx,
		client.ObjectKey{
			Namespace: r.ControllerNamespace,
			Name:      scaleSetListenerName(autoscalingRunnerSet),
		},
		&listener,
	)
	switch {
	case err == nil:
		if listener.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener")
			if err := r.Delete(ctx, &listener); err != nil {
				return false, fmt.Errorf("failed to delete listener: %w", err)
			}
		}
		return false, nil
	case !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener: %w", err)
	}

	logger.Info("Listener is deleted")
	return true, nil
}

func (r *AutoscalingRunnerSetReconciler) cleanupEphemeralRunnerSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (done bool, err error) {
	logger.Info("Cleaning up ephemeral runner set")

	// A multi-variant scale set owns one EphemeralRunnerSet per variant, none of
	// them named after the AutoscalingRunnerSet itself. List every owned set by
	// label so no variant is left orphaned on delete. The single (default) set
	// matches the same labels, so this path also covers the classic case.
	var ersList v1alpha1.EphemeralRunnerSetList
	if err := r.List(
		ctx,
		&ersList,
		client.InNamespace(autoscalingRunnerSet.Namespace),
		client.MatchingLabels{
			LabelKeyGitHubScaleSetName:      autoscalingRunnerSet.Name,
			LabelKeyGitHubScaleSetNamespace: autoscalingRunnerSet.Namespace,
		},
	); err != nil {
		return false, fmt.Errorf("failed to list ephemeral runner sets: %w", err)
	}

	if len(ersList.Items) == 0 {
		logger.Info("Ephemeral runner set is deleted")
		return true, nil
	}

	for i := range ersList.Items {
		ers := &ersList.Items[i]
		if ers.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the ephemeral runner set", "name", ers.Name)
			if err := r.Delete(ctx, ers); err != nil && !kerrors.IsNotFound(err) {
				return false, fmt.Errorf("failed to delete ephemeral runner set %s: %w", ers.Name, err)
			}
		}
	}
	return false, nil
}

func (r *AutoscalingRunnerSetReconciler) removeFinalizersFromDependentResources(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) error {
	c := autoscalingRunnerSetFinalizerDependencyCleaner{
		client:               r.Client,
		autoscalingRunnerSet: autoscalingRunnerSet,
		logger:               logger,
	}

	c.removeKubernetesModeRoleBindingFinalizer(ctx)
	c.removeKubernetesModeRoleFinalizer(ctx)
	c.removeKubernetesModeServiceAccountFinalizer(ctx)
	c.removeNoPermissionServiceAccountFinalizer(ctx)
	c.removeGitHubSecretFinalizer(ctx)
	c.removeManagerRoleBindingFinalizer(ctx)
	c.removeManagerRoleFinalizer(ctx)

	return c.Err()
}

func (r *AutoscalingRunnerSetReconciler) createRunnerScaleSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	original := autoscalingRunnerSet.DeepCopy()
	logger.Info("Creating a new runner scale set")
	actionsClient, err := r.GetActionsService(ctx, autoscalingRunnerSet)
	if len(autoscalingRunnerSet.Spec.RunnerScaleSetName) == 0 {
		autoscalingRunnerSet.Spec.RunnerScaleSetName = autoscalingRunnerSet.Name
	}
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for creating a new runner scale set", "error", err.Error())
		return ctrl.Result{}, err
	}

	runnerGroupID := 1
	if len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 {
		runnerGroup, err := actionsClient.GetRunnerGroupByName(ctx, autoscalingRunnerSet.Spec.RunnerGroup)
		if err != nil {
			logger.Error(err, "Failed to get runner group by name", "runnerGroup", autoscalingRunnerSet.Spec.RunnerGroup)
			return ctrl.Result{}, err
		}

		runnerGroupID = int(runnerGroup.ID)
	}

	variants := autoscalingRunnerSet.Spec.EffectiveVariants()

	// variantIDs collects the registered id for each named variant. It is only
	// written to the annotation when there is more than one variant, so a single
	// (default) scale set keeps just the scalar runner-scale-set-id annotation.
	variantIDs := map[string]int{}
	var defaultRunnerScaleSet *scaleset.RunnerScaleSet

	for _, variant := range variants {
		scaleSetName := variantRunnerScaleSetName(autoscalingRunnerSet, variant)

		runnerScaleSet, err := r.getOrCreateRunnerScaleSet(ctx, actionsClient, runnerGroupID, scaleSetName, variant.RunnerScaleSetLabels, logger)
		if err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Created/Reused a runner scale set", "variant", variant.Name, "id", runnerScaleSet.ID, "runnerGroupName", runnerScaleSet.RunnerGroupName)

		if variant.Name == "" {
			defaultRunnerScaleSet = runnerScaleSet
		} else {
			variantIDs[variant.Name] = runnerScaleSet.ID
			// Mirror the first variant's scale set into the scalar annotations so
			// every classic scalar-reading path (runner group / name update,
			// listener config, delete) keeps working for a multi-variant set. The
			// per-variant ids still live in the variant id map.
			if defaultRunnerScaleSet == nil {
				defaultRunnerScaleSet = runnerScaleSet
			}
		}
	}

	// SystemInfo tracks the first (default) scale set id, exactly as before. For
	// a multi-variant set this is the default variant; each variant's listener
	// session sets its own id from the scale set client.
	if defaultRunnerScaleSet != nil {
		info := actionsClient.SystemInfo()
		info.ScaleSetID = defaultRunnerScaleSet.ID
		actionsClient.SetSystemInfo(info)
	}

	if autoscalingRunnerSet.Annotations == nil {
		autoscalingRunnerSet.Annotations = map[string]string{}
	}
	if autoscalingRunnerSet.Labels == nil {
		autoscalingRunnerSet.Labels = map[string]string{}
	}

	if defaultRunnerScaleSet != nil {
		autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = defaultRunnerScaleSet.Name
		autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey] = strconv.Itoa(defaultRunnerScaleSet.ID)
		autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName] = defaultRunnerScaleSet.RunnerGroupName
	}
	if len(variantIDs) > 0 {
		encoded, err := encodeRunnerScaleSetIDs(variantIDs)
		if err != nil {
			logger.Error(err, "Failed to encode runner scale set ids annotation")
			return ctrl.Result{}, err
		}
		autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetIDs] = encoded
	}
	if err := applyGitHubURLLabels(autoscalingRunnerSet.Spec.GitHubConfigUrl, autoscalingRunnerSet.Labels); err != nil { // should never happen
		logger.Error(err, "Failed to apply GitHub URL labels")
		return ctrl.Result{}, err
	}

	logger.Info("Adding runner scale set ID, name and runner group name as an annotation and url labels")
	if err = r.Patch(ctx, autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to add runner scale set ID, name and runner group name as an annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated with runner scale set IDs, names and runner group name as annotations")
	return ctrl.Result{}, nil
}

// getOrCreateRunnerScaleSet looks up a runner scale set by name and creates it
// if it does not exist, applying the given extra runner labels. It is the
// per-variant registration step; a single (default) variant calls it once with
// the top-level name and labels, exactly as the classic code did.
func (r *AutoscalingRunnerSetReconciler) getOrCreateRunnerScaleSet(ctx context.Context, actionsClient multiclient.Client, runnerGroupID int, scaleSetName string, runnerScaleSetLabels []string, logger logr.Logger) (*scaleset.RunnerScaleSet, error) {
	runnerScaleSet, err := actionsClient.GetRunnerScaleSet(ctx, runnerGroupID, scaleSetName)
	if err != nil {
		logger.Error(err, "Failed to get runner scale set from Actions service",
			"runnerGroupId", strconv.Itoa(runnerGroupID),
			"runnerScaleSetName", scaleSetName)
		return nil, err
	}
	if runnerScaleSet != nil {
		return runnerScaleSet, nil
	}

	labels := []scaleset.Label{
		{
			Name: scaleSetName,
			Type: "System",
		},
	}
	if labelCount := len(runnerScaleSetLabels); labelCount > 0 {
		unique := make(map[string]bool, labelCount+1)
		unique[scaleSetName] = true

		for _, label := range runnerScaleSetLabels {
			if _, exists := unique[label]; exists {
				logger.Info("Duplicate label found. Skipping adding duplicate label to runner scale set", "label", label)
				continue
			}
			labels = append(labels, scaleset.Label{
				Name: label,
				Type: "System",
			})
			unique[label] = true
		}
	}
	runnerScaleSet, err = actionsClient.CreateRunnerScaleSet(
		ctx,
		&scaleset.RunnerScaleSet{
			Name:          scaleSetName,
			RunnerGroupID: runnerGroupID,
			Labels:        labels,
			RunnerSetting: scaleset.RunnerSetting{
				DisableUpdate: true,
			},
		},
	)
	if err != nil {
		logger.Error(err, "Failed to create a new runner scale set on Actions service")
		return nil, err
	}
	return runnerScaleSet, nil
}

// variantRunnerScaleSetName is the GitHub runner scale set name to register for
// one variant. The default variant uses the ARS runner scale set name (its
// classic value); a named variant registers a distinct name so GitHub tracks it
// as its own scale set.
func variantRunnerScaleSetName(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, variant v1alpha1.EffectiveVariant) string {
	if variant.Name == "" {
		return autoscalingRunnerSet.Spec.RunnerScaleSetName
	}
	return autoscalingRunnerSet.Spec.RunnerScaleSetName + "-" + variant.Name
}

func (r *AutoscalingRunnerSetReconciler) updateRunnerScaleSetRunnerGroup(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	runnerScaleSetID, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey])
	if err != nil {
		logger.Error(err, "Failed to parse runner scale set ID")
		return ctrl.Result{}, err
	}

	actionsClient, err := r.GetActionsService(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for updating a existing runner scale set")
		return ctrl.Result{}, err
	}

	runnerGroupID := 1
	if len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 {
		runnerGroup, err := actionsClient.GetRunnerGroupByName(ctx, autoscalingRunnerSet.Spec.RunnerGroup)
		if err != nil {
			logger.Error(err, "Failed to get runner group by name", "runnerGroup", autoscalingRunnerSet.Spec.RunnerGroup)
			return ctrl.Result{}, err
		}

		runnerGroupID = int(runnerGroup.ID)
	}

	updatedRunnerScaleSet, err := actionsClient.UpdateRunnerScaleSet(ctx, runnerScaleSetID, &scaleset.RunnerScaleSet{RunnerGroupID: runnerGroupID})
	if err != nil {
		logger.Error(err, "Failed to update runner scale set", "runnerScaleSetId", runnerScaleSetID)
		return ctrl.Result{}, err
	}

	logger.Info("Updating runner scale set name and runner group name as annotations")
	original := autoscalingRunnerSet.DeepCopy()
	autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName] = updatedRunnerScaleSet.RunnerGroupName
	autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = updatedRunnerScaleSet.Name
	if err := r.Patch(ctx, autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to update runner group name and runner scale set name annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated runner scale set with match runner group", "runnerGroup", updatedRunnerScaleSet.RunnerGroupName)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) updateRunnerScaleSetName(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	runnerScaleSetID, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey])
	if err != nil {
		logger.Error(err, "Failed to parse runner scale set ID")
		return ctrl.Result{}, err
	}

	if len(autoscalingRunnerSet.Spec.RunnerScaleSetName) == 0 {
		logger.Info("Runner scale set name is not specified, skipping")
		return ctrl.Result{}, nil
	}

	actionsClient, err := r.GetActionsService(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for updating a existing runner scale set")
		return ctrl.Result{}, err
	}

	updatedRunnerScaleSet, err := actionsClient.UpdateRunnerScaleSet(ctx, runnerScaleSetID, &scaleset.RunnerScaleSet{Name: autoscalingRunnerSet.Spec.RunnerScaleSetName})
	if err != nil {
		logger.Error(err, "Failed to update runner scale set", "runnerScaleSetId", runnerScaleSetID)
		return ctrl.Result{}, err
	}

	logger.Info("Updating runner scale set name as an annotation")
	original := autoscalingRunnerSet.DeepCopy()
	autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = updatedRunnerScaleSet.Name
	if err := r.Patch(ctx, autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to update runner scale set name annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated runner scale set with match name", "name", updatedRunnerScaleSet.Name)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) deleteRunnerScaleSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) error {
	scaleSetID, ok := autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey]
	if !ok {
		// Annotation not being present can occur in 3 scenarios
		// 1. Scale set is never created.
		//    In this case, we don't need to fetch the actions client to delete the scale set that does not exist
		//
		// 2. The scale set has been deleted by the controller.
		//    In that case, the controller will clean up annotation because the scale set does not exist anymore.
		//    Removal of the scale set id is also useful because permission cleanup will later lose permission
		//    assigned to it on a GitHub secret, causing actions client from secret to result in permission denied
		//
		// 3. Annotation is removed manually.
		//    In this case, the controller will treat this as if the scale set is being removed from the actions service
		//    Then, manual deletion of the scale set is required.
		return nil
	}
	logger.Info("Deleting the runner scale set from Actions service")
	runnerScaleSetID, err := strconv.Atoi(scaleSetID)
	if err != nil {
		// If the annotation is not set correctly, we are going to get stuck in a loop trying to parse the scale set id.
		// If the configuration is invalid (secret does not exist for example), we never got to the point to create runner set.
		// But then, manual cleanup would get stuck finalizing the resource trying to parse annotation indefinitely
		logger.Info("autoscaling runner set does not have annotation describing scale set id. Skip deletion", "err", err.Error())
		return nil
	}

	actionsClient, err := r.GetActionsService(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for updating a existing runner scale set")
		return err
	}

	err = actionsClient.DeleteRunnerScaleSet(ctx, runnerScaleSetID)
	if err != nil {
		logger.Error(err, "Failed to delete runner scale set", "runnerScaleSetId", runnerScaleSetID)
		return err
	}

	// Delete every additional variant scale set registered for a multi-variant
	// set. A single (default) set has no such map, so this loop is a no-op and
	// the behaviour is unchanged.
	variantIDs, err := decodeRunnerScaleSetIDs(autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetIDs])
	if err != nil {
		logger.Error(err, "Failed to decode runner scale set ids annotation, skipping variant scale set deletion")
	} else {
		for variantName, variantID := range variantIDs {
			if err := actionsClient.DeleteRunnerScaleSet(ctx, variantID); err != nil {
				logger.Error(err, "Failed to delete variant runner scale set", "variant", variantName, "runnerScaleSetId", variantID)
				return err
			}
		}
	}

	original := autoscalingRunnerSet.DeepCopy()
	delete(autoscalingRunnerSet.Annotations, runnerScaleSetIDAnnotationKey)
	delete(autoscalingRunnerSet.Annotations, AnnotationKeyGitHubRunnerScaleSetIDs)

	if err := r.Patch(ctx, autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to remove runner scale set ID annotation after deleting the runner scale set", "runnerScaleSetId", runnerScaleSetID)
		return err
	}

	logger.Info("Deleted the runner scale set from Actions service")
	return nil
}

func (r *AutoscalingRunnerSetReconciler) createEphemeralRunnerSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, log logr.Logger) (ctrl.Result, error) {
	desiredRunnerSet, err := r.newEphemeralRunnerSet(autoscalingRunnerSet)
	if err != nil {
		log.Error(err, "Could not create EphemeralRunnerSet")
		return ctrl.Result{}, err
	}

	log.Info("Creating a new EphemeralRunnerSet resource")
	if err := r.Create(ctx, desiredRunnerSet); err != nil {
		log.Error(err, "Failed to create EphemeralRunnerSet resource")
		return ctrl.Result{}, err
	}

	log.Info("Created a new EphemeralRunnerSet resource", "name", desiredRunnerSet.Name)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) createAutoScalingListenerForRunnerSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (ctrl.Result, error) {
	var imagePullSecrets []corev1.LocalObjectReference
	for _, imagePullSecret := range r.DefaultRunnerScaleSetListenerImagePullSecrets {
		imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{
			Name: imagePullSecret,
		})
	}

	autoscalingListener, err := r.newAutoscalingListener(
		autoscalingRunnerSet,
		ephemeralRunnerSet,
		r.ControllerNamespace,
		r.DefaultRunnerScaleSetListenerImage,
		imagePullSecrets,
	)
	if err != nil {
		log.Error(err, "Could not create AutoscalingListener spec")
		return ctrl.Result{}, err
	}

	log.Info("Creating a new AutoscalingListener resource", "name", autoscalingListener.Name, "namespace", autoscalingListener.Namespace)
	if err := r.Create(ctx, autoscalingListener); err != nil {
		log.Error(err, "Failed to create AutoscalingListener resource")
		return ctrl.Result{}, err
	}

	log.Info("Created a new AutoscalingListener resource", "name", autoscalingListener.Name, "namespace", autoscalingListener.Namespace)
	return ctrl.Result{}, nil
}

// isMultiVariant reports whether the AutoscalingRunnerSet declares any runner
// variants. When it does, the reconciler takes the fan-out path (one named
// EphemeralRunnerSet per variant behind a single listener). A set with NO
// variants uses the classic single-set path, byte-for-byte unchanged, which the
// golden hash tests pin. Even a single named variant is a distinct construct
// (named child ERS, variant label, id map), so it takes the fan-out path too;
// this also lets orphan cleanup run when a set drops from many variants to one.
func isMultiVariant(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) bool {
	return len(autoscalingRunnerSet.Spec.RunnerVariants) > 0
}

// reconcileMultiVariant drives the fan-out path: one EphemeralRunnerSet per
// variant, orphan cleanup for removed variants, and exactly one listener that
// services every variant. It runs only when isMultiVariant is true, so the
// classic single-set path is never affected.
func (r *AutoscalingRunnerSetReconciler) reconcileMultiVariant(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, log logr.Logger) (ctrl.Result, error) {
	variants := autoscalingRunnerSet.Spec.EffectiveVariants()

	// List every EphemeralRunnerSet owned by this scale set so we can create the
	// missing ones, patch drifted ones, and delete orphans left by a removed
	// variant.
	var childList v1alpha1.EphemeralRunnerSetList
	if err := r.List(
		ctx,
		&childList,
		client.InNamespace(autoscalingRunnerSet.Namespace),
		client.MatchingLabels{
			LabelKeyGitHubScaleSetName:      autoscalingRunnerSet.Name,
			LabelKeyGitHubScaleSetNamespace: autoscalingRunnerSet.Namespace,
		},
	); err != nil {
		log.Error(err, "Failed to list ephemeral runner sets for multi-variant scale set")
		return ctrl.Result{}, err
	}

	existing := make(map[string]*v1alpha1.EphemeralRunnerSet, len(childList.Items))
	for i := range childList.Items {
		existing[childList.Items[i].Name] = &childList.Items[i]
	}

	expected := make(map[string]struct{}, len(variants))
	for _, variant := range variants {
		name := ephemeralRunnerSetName(autoscalingRunnerSet, variant)
		expected[name] = struct{}{}

		desired, err := r.newEphemeralRunnerSetForVariant(autoscalingRunnerSet, variant)
		if err != nil {
			log.Error(err, "Failed to build ephemeral runner set for variant", "variant", variant.Name)
			return ctrl.Result{}, err
		}

		current, ok := existing[name]
		if !ok {
			log.Info("Creating ephemeral runner set for variant", "variant", variant.Name, "name", name)
			if err := r.Create(ctx, desired); err != nil {
				log.Error(err, "Failed to create ephemeral runner set for variant", "variant", variant.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		if current.Annotations[annotationKeyIntegrityHash] != desired.Annotations[annotationKeyIntegrityHash] {
			original := current.DeepCopy()
			current.Spec.EphemeralRunnerMetadata = desired.Spec.EphemeralRunnerMetadata
			current.Spec.EphemeralRunnerSpec = desired.Spec.EphemeralRunnerSpec
			current.Labels = r.filterAndMergeLabels(current.Labels, desired.Labels)
			current.Annotations = r.mergeAnnotations(current.Annotations, desired.Annotations)
			log.Info("Updating ephemeral runner set for variant to match desired spec", "variant", variant.Name)
			if err := r.Patch(ctx, current, client.MergeFrom(original)); err != nil {
				log.Error(err, "Failed to patch ephemeral runner set for variant", "variant", variant.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Delete orphan ephemeral runner sets left by a removed variant.
	for name, ers := range existing {
		if _, keep := expected[name]; keep {
			continue
		}
		if !ers.DeletionTimestamp.IsZero() {
			continue
		}
		log.Info("Deleting orphan ephemeral runner set for removed variant", "name", name)
		if err := r.Delete(ctx, ers); err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to delete orphan ephemeral runner set", "name", name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileMultiVariantListener(ctx, autoscalingRunnerSet, variants, log)
}

// reconcileMultiVariantListener ensures exactly one AutoscalingListener exists
// for a multi-variant scale set, carrying the per-variant scale set tuples on
// the out-of-band annotation so the listener spec and hash stay stable.
func (r *AutoscalingRunnerSetReconciler) reconcileMultiVariantListener(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, variants []v1alpha1.EffectiveVariant, log logr.Logger) (ctrl.Result, error) {
	tuples, err := r.listenerScaleSetTuples(autoscalingRunnerSet, variants)
	if err != nil {
		log.Error(err, "Failed to build listener scale set tuples")
		return ctrl.Result{}, err
	}

	// The listener spec is built from the first variant's EphemeralRunnerSet,
	// exactly like a single-set listener is built from its one ERS; the extra
	// variants ride on the annotation. Fetch that ERS so the listener owner
	// references and scalar fields line up. A multi-variant set has no ERS named
	// after the AutoscalingRunnerSet itself, so key off the first variant.
	if len(variants) == 0 {
		return ctrl.Result{}, nil
	}
	firstERSName := ephemeralRunnerSetName(autoscalingRunnerSet, variants[0])
	var defaultERS v1alpha1.EphemeralRunnerSet
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: firstERSName}, &defaultERS); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info("First ephemeral runner set not found yet, requeuing", "name", firstERSName)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to get ephemeral runner set for listener")
		return ctrl.Result{}, err
	}

	var listener v1alpha1.AutoscalingListener
	err = r.Get(ctx, types.NamespacedName{Namespace: r.ControllerNamespace, Name: scaleSetListenerName(autoscalingRunnerSet)}, &listener)
	switch {
	case kerrors.IsNotFound(err):
		return r.createMultiVariantListener(ctx, autoscalingRunnerSet, &defaultERS, tuples, log)
	case err != nil:
		log.Error(err, "Failed to get AutoscalingListener resource")
		return ctrl.Result{}, err
	default:
		desired, err := r.newMultiVariantListener(autoscalingRunnerSet, &defaultERS, tuples, nil)
		if err != nil {
			log.Error(err, "Failed to generate AutoscalingListener spec")
			return ctrl.Result{}, nil
		}
		if !cmp.Equal(listener.Spec, desired.Spec) ||
			!cmp.Equal(listener.Labels, desired.Labels) ||
			!cmp.Equal(listener.Annotations, desired.Annotations) {
			log.Info("Recreating the listener for multi-variant scale set to match desired state")
			if _, err := r.cleanupListener(ctx, autoscalingRunnerSet, log); err != nil {
				log.Error(err, "Failed to clean up listener before recreation")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	return ctrl.Result{}, nil
}

// listenerScaleSetTuples builds the ordered scale set tuples for a multi-variant
// listener from the registered variant ids and per-variant bounds.
func (r *AutoscalingRunnerSetReconciler) listenerScaleSetTuples(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, variants []v1alpha1.EffectiveVariant) ([]v1alpha1.ListenerScaleSet, error) {
	tuples := make([]v1alpha1.ListenerScaleSet, 0, len(variants))
	for _, variant := range variants {
		id, err := variantScaleSetID(autoscalingRunnerSet, variant)
		if err != nil {
			return nil, err
		}
		minRunners := 0
		maxRunners := 0
		if variant.MinRunners != nil {
			minRunners = *variant.MinRunners
		}
		if variant.MaxRunners != nil {
			maxRunners = *variant.MaxRunners
		}
		tuples = append(tuples, v1alpha1.ListenerScaleSet{
			VariantName:            variant.Name,
			RunnerScaleSetID:       id,
			EphemeralRunnerSetName: ephemeralRunnerSetName(autoscalingRunnerSet, variant),
			MinRunners:             minRunners,
			MaxRunners:             maxRunners,
		})
	}
	return tuples, nil
}

// newMultiVariantListener builds the listener object for a multi-variant scale
// set: the classic single-set listener plus the out-of-band scale set tuples.
func (r *AutoscalingRunnerSetReconciler) newMultiVariantListener(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, defaultERS *v1alpha1.EphemeralRunnerSet, tuples []v1alpha1.ListenerScaleSet, imagePullSecrets []corev1.LocalObjectReference) (*v1alpha1.AutoscalingListener, error) {
	listener, err := r.newAutoscalingListener(autoscalingRunnerSet, defaultERS, r.ControllerNamespace, r.DefaultRunnerScaleSetListenerImage, imagePullSecrets)
	if err != nil {
		return nil, err
	}
	if err := stampListenerScaleSets(listener, tuples); err != nil {
		return nil, err
	}
	return listener, nil
}

func (r *AutoscalingRunnerSetReconciler) createMultiVariantListener(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, defaultERS *v1alpha1.EphemeralRunnerSet, tuples []v1alpha1.ListenerScaleSet, log logr.Logger) (ctrl.Result, error) {
	var imagePullSecrets []corev1.LocalObjectReference
	for _, imagePullSecret := range r.DefaultRunnerScaleSetListenerImagePullSecrets {
		imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: imagePullSecret})
	}

	listener, err := r.newMultiVariantListener(autoscalingRunnerSet, defaultERS, tuples, imagePullSecrets)
	if err != nil {
		log.Error(err, "Could not create AutoscalingListener spec")
		return ctrl.Result{}, err
	}

	log.Info("Creating a new AutoscalingListener resource for multi-variant scale set", "name", listener.Name, "variants", len(tuples))
	if err := r.Create(ctx, listener); err != nil {
		log.Error(err, "Failed to create AutoscalingListener resource")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// shouldCreateScaleSet reports whether any variant still needs a registered
// runner scale set id. For the single (default) variant this is exactly the
// classic scalar-annotation check. When more than one variant is declared, it
// also requires an id for every named variant in the runner-scale-set-ids map,
// so adding a variant to an existing set re-enters the registration path.
func shouldCreateScaleSet(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) bool {
	variants := autoscalingRunnerSet.Spec.EffectiveVariants()

	ids, err := decodeRunnerScaleSetIDs(autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetIDs])
	if err != nil {
		return true
	}

	for _, variant := range variants {
		if variant.Name == "" {
			scaleSetIDRaw, ok := autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey]
			if !ok {
				return true
			}
			id, err := strconv.Atoi(scaleSetIDRaw)
			if err != nil || id <= 0 {
				return true
			}
			continue
		}
		if id, ok := ids[variant.Name]; !ok || id <= 0 {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutoscalingRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager, opts ...Option) error {
	r.ResourceBuilder.setSchemeIfUnset(r.Scheme)

	return builderWithOptions(
		ctrl.NewControllerManagedBy(mgr).
			For(&v1alpha1.AutoscalingRunnerSet{}).
			Owns(&v1alpha1.EphemeralRunnerSet{}).
			Watches(&v1alpha1.AutoscalingListener{}, handler.EnqueueRequestsFromMapFunc(
				func(_ context.Context, o client.Object) []reconcile.Request {
					autoscalingListener := o.(*v1alpha1.AutoscalingListener)
					return []reconcile.Request{
						{
							NamespacedName: types.NamespacedName{
								Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
								Name:      autoscalingListener.Spec.AutoscalingRunnerSetName,
							},
						},
					}
				},
			)).
			WithEventFilter(predicate.ResourceVersionChangedPredicate{}),
		opts,
	).Complete(r)
}

type autoscalingRunnerSetFinalizerDependencyCleaner struct {
	// configuration fields
	client               client.Client
	autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	logger               logr.Logger

	err error
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) Err() error {
	return c.err
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeKubernetesModeRoleBindingFinalizer(ctx context.Context) {
	if c.err != nil {
		c.logger.Info("Skipping cleaning up kubernetes mode service account")
		return
	}

	roleBindingName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyKubernetesModeRoleBindingName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up kubernetes mode service account",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyKubernetesModeRoleBindingName),
		)
		return
	}

	c.logger.Info("Removing finalizer from container mode kubernetes role binding", "name", roleBindingName)

	roleBinding := new(rbacv1.RoleBinding)
	err := c.client.Get(ctx, types.NamespacedName{Name: roleBindingName, Namespace: c.autoscalingRunnerSet.Namespace}, roleBinding)
	switch {
	case err == nil:
		if !controllerutil.ContainsFinalizer(roleBinding, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("Kubernetes mode role binding finalizer has already been removed", "name", roleBindingName)
			return
		}

		original := roleBinding.DeepCopy()
		if controllerutil.RemoveFinalizer(roleBinding, AutoscalingRunnerSetCleanupFinalizerName) {
			if err = c.client.Patch(ctx, roleBinding, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch kubernetes mode role binding without finalizer: %w", err)
				return
			}
		}
		c.logger.Info("Removed finalizer from container mode kubernetes role binding", "name", roleBindingName)
		return
	case !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch kubernetes mode role binding: %w", err)
		return
	default:
		c.logger.Info("Container mode kubernetes role binding has already been deleted", "name", roleBindingName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeKubernetesModeRoleFinalizer(ctx context.Context) {
	if c.err != nil {
		return
	}

	roleName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyKubernetesModeRoleName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up kubernetes mode role",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyKubernetesModeRoleName),
		)
		return
	}

	c.logger.Info("Removing finalizer from container mode kubernetes role", "name", roleName)
	role := new(rbacv1.Role)
	err := c.client.Get(ctx, types.NamespacedName{Name: roleName, Namespace: c.autoscalingRunnerSet.Namespace}, role)
	switch {
	case err == nil:
		if !controllerutil.ContainsFinalizer(role, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("Kubernetes mode role finalizer has already been removed", "name", roleName)
			return
		}
		original := role.DeepCopy()
		if controllerutil.RemoveFinalizer(role, AutoscalingRunnerSetCleanupFinalizerName) {
			if err = c.client.Patch(ctx, role, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch kubernetes mode role without finalizer: %w", err)
				return
			}
		}
		c.logger.Info("Removed finalizer from container mode kubernetes role")
		return
	case kerrors.IsNotFound(err):
		c.logger.Info("Container mode kubernetes role has already been deleted", "name", roleName)
		return
	default:
		c.err = fmt.Errorf("failed to fetch kubernetes mode role: %w", err)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeKubernetesModeServiceAccountFinalizer(ctx context.Context) {
	if c.err != nil {
		return
	}

	serviceAccountName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyKubernetesModeServiceAccountName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up kubernetes mode role binding",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyKubernetesModeServiceAccountName),
		)
		return
	}

	c.logger.Info("Removing finalizer from container mode kubernetes service account", "name", serviceAccountName)

	serviceAccount := new(corev1.ServiceAccount)
	err := c.client.Get(ctx, types.NamespacedName{Name: serviceAccountName, Namespace: c.autoscalingRunnerSet.Namespace}, serviceAccount)
	switch {
	case err == nil:
		if !controllerutil.ContainsFinalizer(serviceAccount, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("Kubernetes mode service account finalizer has already been removed", "name", serviceAccountName)
			return
		}
		original := serviceAccount.DeepCopy()
		if controllerutil.RemoveFinalizer(serviceAccount, AutoscalingRunnerSetCleanupFinalizerName) {
			if err = c.client.Patch(ctx, serviceAccount, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch kubernetes mode service account without finalizer: %w", err)
				return
			}
		}

		c.logger.Info("Removed finalizer from container mode kubernetes service account")
		return
	case kerrors.IsNotFound(err):
		c.logger.Info("Container mode kubernetes service account has already been deleted", "name", serviceAccountName)
		return
	default:
		c.err = fmt.Errorf("failed to fetch kubernetes mode service account: %w", err)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeNoPermissionServiceAccountFinalizer(ctx context.Context) {
	if c.err != nil {
		return
	}

	serviceAccountName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyNoPermissionServiceAccountName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up no permission service account",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyNoPermissionServiceAccountName),
		)
		return
	}

	c.logger.Info("Removing finalizer from no permission service account", "name", serviceAccountName)

	serviceAccount := new(corev1.ServiceAccount)
	err := c.client.Get(
		ctx,
		types.NamespacedName{
			Name:      serviceAccountName,
			Namespace: c.autoscalingRunnerSet.Namespace,
		},
		serviceAccount,
	)
	switch {
	case err == nil:
		if !controllerutil.ContainsFinalizer(serviceAccount, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("No permission service account finalizer has already been removed", "name", serviceAccountName)
			return
		}
		original := serviceAccount.DeepCopy()

		if controllerutil.RemoveFinalizer(serviceAccount, AutoscalingRunnerSetCleanupFinalizerName) {
			if err = c.client.Patch(ctx, serviceAccount, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch no permission service account without finalizer: %w", err)
				return
			}
		}

		c.logger.Info("Removed finalizer from no permission service account", "name", serviceAccountName)
		return
	case kerrors.IsNotFound(err):
		c.logger.Info("No permission service account has already been deleted", "name", serviceAccountName)
		return
	default:
		c.err = fmt.Errorf("failed to fetch service account: %w", err)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeGitHubSecretFinalizer(ctx context.Context) {
	if c.err != nil {
		return
	}

	githubSecretName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyGitHubSecretName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up no permission service account",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyGitHubSecretName),
		)
		return
	}

	c.logger.Info("Removing finalizer from GitHub secret", "name", githubSecretName)

	githubSecret := new(corev1.Secret)
	err := c.client.Get(ctx, types.NamespacedName{Name: githubSecretName, Namespace: c.autoscalingRunnerSet.Namespace}, githubSecret)
	switch {
	case err == nil:
		if !controllerutil.ContainsFinalizer(githubSecret, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("GitHub secret finalizer has already been removed", "name", githubSecretName)
			return
		}
		original := githubSecret.DeepCopy()

		if controllerutil.RemoveFinalizer(githubSecret, AutoscalingRunnerSetCleanupFinalizerName) {
			if err = c.client.Patch(ctx, githubSecret, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch GitHub secret without finalizer: %w", err)
				return
			}
		}
		c.logger.Info("Removed finalizer from GitHub secret", "name", githubSecretName)
		return
	case kerrors.IsNotFound(err) || kerrors.IsForbidden(err):
		c.logger.Info("GitHub secret has already been deleted", "name", githubSecretName)
		return
	default:
		c.err = fmt.Errorf("failed to fetch GitHub secret: %w", err)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeManagerRoleBindingFinalizer(ctx context.Context) {
	if c.err != nil {
		return
	}

	managerRoleBindingName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyManagerRoleBindingName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up manager role binding",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyManagerRoleBindingName),
		)
		return
	}

	c.logger.Info("Removing finalizer from manager role binding", "name", managerRoleBindingName)

	roleBinding := new(rbacv1.RoleBinding)
	err := c.client.Get(ctx, types.NamespacedName{Name: managerRoleBindingName, Namespace: c.autoscalingRunnerSet.Namespace}, roleBinding)
	switch {
	case err == nil:
		original := roleBinding.DeepCopy()
		if controllerutil.RemoveFinalizer(roleBinding, AutoscalingRunnerSetCleanupFinalizerName) {
			if err = c.client.Patch(ctx, roleBinding, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch manager role binding without finalizer: %w", err)
				return
			}
		}

		c.logger.Info("Removed finalizer from manager role binding", "name", managerRoleBindingName)
		return
	case kerrors.IsNotFound(err):
		c.logger.Info("Manager role binding has already been deleted", "name", managerRoleBindingName)
		return
	default:
		c.err = fmt.Errorf("failed to fetch manager role binding: %w", err)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeManagerRoleFinalizer(ctx context.Context) {
	if c.err != nil {
		return
	}

	managerRoleName, ok := c.autoscalingRunnerSet.Annotations[AnnotationKeyManagerRoleName]
	if !ok {
		c.logger.Info(
			"Skipping cleaning up manager role",
			"reason",
			fmt.Sprintf("annotation key %q not present", AnnotationKeyManagerRoleName),
		)
		return
	}

	c.logger.Info("Removing finalizer from manager role", "name", managerRoleName)

	role := new(rbacv1.Role)
	err := c.client.Get(ctx, types.NamespacedName{Name: managerRoleName, Namespace: c.autoscalingRunnerSet.Namespace}, role)
	switch {
	case err == nil:
		original := role.DeepCopy()
		if controllerutil.RemoveFinalizer(role, AutoscalingRunnerSetCleanupFinalizerName) {
			if err := c.client.Patch(ctx, role, client.MergeFrom(original)); err != nil {
				c.err = fmt.Errorf("failed to patch manager role without finalizer: %w", err)
				return
			}
		}
		c.logger.Info("Removed finalizer from manager role", "name", managerRoleName)
		return
	case kerrors.IsNotFound(err):
		c.logger.Info("Manager role has already been deleted", "name", managerRoleName)
		return
	default:
		c.err = fmt.Errorf("failed to fetch manager role: %w", err)
		return
	}
}
