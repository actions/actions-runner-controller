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
	if targetHash := autoscalingRunnerSet.Hash(); autoscalingRunnerSet.Annotations[AnnotationKeyIntegrityHash] != targetHash {
		// TODO: apply the version label
		original := autoscalingRunnerSet.DeepCopy()
		if autoscalingRunnerSet.Annotations == nil {
			autoscalingRunnerSet.Annotations = map[string]string{}
		}
		autoscalingRunnerSet.Annotations[AnnotationKeyIntegrityHash] = targetHash
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

		if ephemeralRunnerSet.Annotations[AnnotationKeyIntegrityHash] != desired.Annotations[AnnotationKeyIntegrityHash] {
			original := ephemeralRunnerSet.DeepCopy()
			ephemeralRunnerSet.Spec.EphemeralRunnerMetadata = desired.Spec.EphemeralRunnerMetadata
			ephemeralRunnerSet.Spec.EphemeralRunnerSpec = desired.Spec.EphemeralRunnerSpec
			ephemeralRunnerSet.Labels = r.filterAndMergeLabels(ephemeralRunnerSet.Labels, desired.Labels)
			ephemeralRunnerSet.Annotations = r.filterAndMergeAnnotations(ephemeralRunnerSet.Annotations, desired.Annotations)

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
			ephemeralRunnerSet.Annotations = r.filterAndMergeAnnotations(ephemeralRunnerSet.Annotations, desired.Annotations)
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
		&ephemeralRunnerSet,
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
func (r *AutoscalingRunnerSetReconciler) updateStatus(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, phase v1alpha1.AutoscalingRunnerSetPhase, log logr.Logger) error {
	countDiff := ephemeralRunnerSet != nil && ephemeralRunnerSet.Status.CurrentReplicas != autoscalingRunnerSet.Status.CurrentRunners
	phaseDiff := phase != autoscalingRunnerSet.Status.Phase
	if !countDiff && !phaseDiff {
		return nil
	}

	original := autoscalingRunnerSet.DeepCopy()
	if phaseDiff {
		autoscalingRunnerSet.Status.Phase = phase
	}

	if countDiff && ephemeralRunnerSet != nil {
		autoscalingRunnerSet.Status.CurrentRunners = ephemeralRunnerSet.Status.CurrentReplicas
		autoscalingRunnerSet.Status.PendingEphemeralRunners = ephemeralRunnerSet.Status.PendingEphemeralRunners
		autoscalingRunnerSet.Status.RunningEphemeralRunners = ephemeralRunnerSet.Status.RunningEphemeralRunners
		autoscalingRunnerSet.Status.FailedEphemeralRunners = ephemeralRunnerSet.Status.FailedEphemeralRunners
	}

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
	var ers v1alpha1.EphemeralRunnerSet
	err = r.Get(
		ctx,
		client.ObjectKey{
			Namespace: autoscalingRunnerSet.Namespace,
			Name:      autoscalingRunnerSet.Name,
		},
		&ers,
	)
	switch {
	case err == nil:
		if ers.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the ephemeral runner set")
			if err := r.Delete(ctx, &ers); err != nil {
				return false, fmt.Errorf("failed to delete ephemeral runner set: %w", err)
			}
		}
		return false, nil
	case !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get ephemeral runner set: %w", err)
	}

	logger.Info("Ephemeral runner set is deleted")
	return true, nil
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

	runnerScaleSet, err := actionsClient.GetRunnerScaleSet(ctx, runnerGroupID, autoscalingRunnerSet.Spec.RunnerScaleSetName)
	if err != nil {
		logger.Error(err, "Failed to get runner scale set from Actions service",
			"runnerGroupId",
			strconv.Itoa(runnerGroupID),
			"runnerScaleSetName",
			autoscalingRunnerSet.Spec.RunnerScaleSetName)
		return ctrl.Result{}, err
	}

	if runnerScaleSet == nil {
		labels := []scaleset.Label{
			{
				Name: autoscalingRunnerSet.Spec.RunnerScaleSetName,
				Type: "System",
			},
		}

		if labelCount := len(autoscalingRunnerSet.Spec.RunnerScaleSetLabels); labelCount > 0 {
			unique := make(map[string]bool, labelCount+1)
			unique[autoscalingRunnerSet.Spec.RunnerScaleSetName] = true

			for _, label := range autoscalingRunnerSet.Spec.RunnerScaleSetLabels {
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
				Name:          autoscalingRunnerSet.Spec.RunnerScaleSetName,
				RunnerGroupID: runnerGroupID,
				Labels:        labels,
				RunnerSetting: scaleset.RunnerSetting{
					DisableUpdate: true,
				},
			},
		)
		if err != nil {
			logger.Error(err, "Failed to create a new runner scale set on Actions service")
			return ctrl.Result{}, err
		}
	}

	info := actionsClient.SystemInfo()
	info.ScaleSetID = runnerScaleSet.ID
	actionsClient.SetSystemInfo(info)

	logger.Info("Created/Reused a runner scale set", "id", runnerScaleSet.ID, "runnerGroupName", runnerScaleSet.RunnerGroupName)
	if autoscalingRunnerSet.Annotations == nil {
		autoscalingRunnerSet.Annotations = map[string]string{}
	}
	if autoscalingRunnerSet.Labels == nil {
		autoscalingRunnerSet.Labels = map[string]string{}
	}

	autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = runnerScaleSet.Name
	autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey] = strconv.Itoa(runnerScaleSet.ID)
	autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName] = runnerScaleSet.RunnerGroupName
	if err := applyGitHubURLLabels(autoscalingRunnerSet.Spec.GitHubConfigUrl, autoscalingRunnerSet.Labels); err != nil { // should never happen
		logger.Error(err, "Failed to apply GitHub URL labels")
		return ctrl.Result{}, err
	}

	logger.Info("Adding runner scale set ID, name and runner group name as an annotation and url labels")
	if err = r.Patch(ctx, autoscalingRunnerSet, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to add runner scale set ID, name and runner group name as an annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated with runner scale set ID, name and runner group name as an annotation",
		"id", runnerScaleSet.ID,
		"name", runnerScaleSet.Name,
		"runnerGroupName", runnerScaleSet.RunnerGroupName)
	return ctrl.Result{}, nil
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

	original := autoscalingRunnerSet.DeepCopy()
	delete(autoscalingRunnerSet.Annotations, runnerScaleSetIDAnnotationKey)

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

// TODO: change that
func shouldCreateScaleSet(autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) bool {
	scaleSetIDRaw, ok := autoscalingRunnerSet.Annotations[runnerScaleSetIDAnnotationKey]
	if !ok {
		return true
	}
	id, err := strconv.Atoi(scaleSetIDRaw)
	return err != nil || id <= 0
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
