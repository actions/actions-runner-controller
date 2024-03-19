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
	"sort"
	"strconv"
	"strings"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	annotationKeyRunnerSpecHash = "actions.github.com/runner-spec-hash"
	// annotationKeyValuesHash is hash of the entire values json.
	// This is used to determine if the values have changed, so we can
	// re-create listener.
	annotationKeyValuesHash = "actions.github.com/values-hash"

	autoscalingRunnerSetFinalizerName = "autoscalingrunnerset.actions.github.com/finalizer"
	runnerScaleSetIdAnnotationKey     = "runner-scale-set-id"
)

type UpdateStrategy string

// Defines how the controller should handle upgrades while having running jobs.
const (
	// "immediate": (default) The controller will immediately apply the change causing the
	// recreation of the listener and ephemeral runner set. This can lead to an
	// overprovisioning of runners, if there are pending / running jobs. This should not
	// be a problem at a small scale, but it could lead to a significant increase of
	// resources if you have a lot of jobs running concurrently.
	UpdateStrategyImmediate = UpdateStrategy("immediate")
	// "eventual": The controller will remove the listener and ephemeral runner set
	// immediately, but will not recreate them (to apply changes) until all
	// pending / running jobs have completed.
	// This can lead to a longer time to apply the change but it will ensure
	// that you don't have any overprovisioning of runners.
	UpdateStrategyEventual = UpdateStrategy("eventual")
)

// AutoscalingRunnerSetReconciler reconciles a AutoscalingRunnerSet object
type AutoscalingRunnerSetReconciler struct {
	client.Client
	Log                                           logr.Logger
	Scheme                                        *runtime.Scheme
	ControllerNamespace                           string
	DefaultRunnerScaleSetListenerImage            string
	DefaultRunnerScaleSetListenerImagePullSecrets []string
	UpdateStrategy                                UpdateStrategy
	ActionsClient                                 actions.MultiClient

	resourceBuilder resourceBuilder
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

	autoscalingRunnerSet := new(v1alpha1.AutoscalingRunnerSet)
	if err := r.Get(ctx, req.NamespacedName, autoscalingRunnerSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !autoscalingRunnerSet.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(autoscalingRunnerSet, autoscalingRunnerSetFinalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("Deleting resources")
		done, err := r.cleanupListener(ctx, autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up listener")
			return ctrl.Result{}, err
		}
		if !done {
			// we are going to get notified anyway to proceed with rest of the
			// cleanup. No need to re-queue
			log.Info("Waiting for listener to be deleted")
			return ctrl.Result{}, nil
		}

		done, err = r.cleanupEphemeralRunnerSets(ctx, autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up ephemeral runner sets")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for ephemeral runner sets to be deleted")
			return ctrl.Result{}, nil
		}

		err = r.deleteRunnerScaleSet(ctx, autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to delete runner scale set")
			return ctrl.Result{}, err
		}

		requeue, err := r.removeFinalizersFromDependentResources(ctx, autoscalingRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to remove finalizers on dependent resources")
			return ctrl.Result{}, err
		}

		if requeue {
			log.Info("Waiting for dependent resources to be deleted")
			return ctrl.Result{Requeue: true}, nil
		}

		log.Info("Removing finalizer")
		err = patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
			controllerutil.RemoveFinalizer(obj, autoscalingRunnerSetFinalizerName)
		})
		if err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to update autoscaling runner set without finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully removed finalizer after cleanup")
		return ctrl.Result{}, nil
	}

	if autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion] != build.Version {
		if err := r.Delete(ctx, autoscalingRunnerSet); err != nil {
			log.Error(err, "Failed to delete autoscaling runner set on version mismatch",
				"targetVersion", build.Version,
				"actualVersion", autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
			)
			return ctrl.Result{}, nil
		}

		log.Info("Autoscaling runner set version doesn't match the build version. Deleting the resource.",
			"targetVersion", build.Version,
			"actualVersion", autoscalingRunnerSet.Labels[LabelKeyKubernetesVersion],
		)
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(autoscalingRunnerSet, autoscalingRunnerSetFinalizerName) {
		log.Info("Adding finalizer")
		if err := patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
			controllerutil.AddFinalizer(obj, autoscalingRunnerSetFinalizerName)
		}); err != nil {
			log.Error(err, "Failed to update autoscaling runner set with finalizer added")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	scaleSetIdRaw, ok := autoscalingRunnerSet.Annotations[runnerScaleSetIdAnnotationKey]
	if !ok {
		// Need to create a new runner scale set on Actions service
		log.Info("Runner scale set id annotation does not exist. Creating a new runner scale set.")
		return r.createRunnerScaleSet(ctx, autoscalingRunnerSet, log)
	}

	if id, err := strconv.Atoi(scaleSetIdRaw); err != nil || id <= 0 {
		log.Info("Runner scale set id annotation is not an id, or is <= 0. Creating a new runner scale set.")
		// something modified the scaleSetId. Try to create one
		return r.createRunnerScaleSet(ctx, autoscalingRunnerSet, log)
	}

	// Make sure the runner group of the scale set is up to date
	currentRunnerGroupName, ok := autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerGroupName]
	if !ok || (len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 && !strings.EqualFold(currentRunnerGroupName, autoscalingRunnerSet.Spec.RunnerGroup)) {
		log.Info("AutoScalingRunnerSet runner group changed. Updating the runner scale set.")
		return r.updateRunnerScaleSetRunnerGroup(ctx, autoscalingRunnerSet, log)
	}

	// Make sure the runner scale set name is up to date
	currentRunnerScaleSetName, ok := autoscalingRunnerSet.Annotations[AnnotationKeyGitHubRunnerScaleSetName]
	if !ok || (len(autoscalingRunnerSet.Spec.RunnerScaleSetName) > 0 && !strings.EqualFold(currentRunnerScaleSetName, autoscalingRunnerSet.Spec.RunnerScaleSetName)) {
		log.Info("AutoScalingRunnerSet runner scale set name changed. Updating the runner scale set.")
		return r.updateRunnerScaleSetName(ctx, autoscalingRunnerSet, log)
	}

	secret := new(corev1.Secret)
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: autoscalingRunnerSet.Spec.GitHubConfigSecret}, secret); err != nil {
		log.Error(err, "Failed to find GitHub config secret.",
			"namespace", autoscalingRunnerSet.Namespace,
			"name", autoscalingRunnerSet.Spec.GitHubConfigSecret)
		return ctrl.Result{}, err
	}

	existingRunnerSets, err := r.listEphemeralRunnerSets(ctx, autoscalingRunnerSet)
	if err != nil {
		log.Error(err, "Failed to list existing ephemeral runner sets")
		return ctrl.Result{}, err
	}

	latestRunnerSet := existingRunnerSets.latest()
	if latestRunnerSet == nil {
		log.Info("Latest runner set does not exist. Creating a new runner set.")
		return r.createEphemeralRunnerSet(ctx, autoscalingRunnerSet, log)
	}

	for _, runnerSet := range existingRunnerSets.all() {
		log.Info("Find existing ephemeral runner set", "name", runnerSet.Name, "specHash", runnerSet.Annotations[annotationKeyRunnerSpecHash])
	}

	// Make sure the AutoscalingListener is up and running in the controller namespace
	listener := new(v1alpha1.AutoscalingListener)
	listenerFound := true
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.ControllerNamespace, Name: scaleSetListenerName(autoscalingRunnerSet)}, listener); err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to get AutoscalingListener resource")
			return ctrl.Result{}, err
		}

		listenerFound = false
		log.Info("AutoscalingListener does not exist.")
	}

	// Our listener pod is out of date, so we need to delete it to get a new recreate.
	listenerValuesHashChanged := listener.Annotations[annotationKeyValuesHash] != autoscalingRunnerSet.Annotations[annotationKeyValuesHash]
	listenerSpecHashChanged := listener.Annotations[annotationKeyRunnerSpecHash] != autoscalingRunnerSet.ListenerSpecHash()
	if listenerFound && (listenerValuesHashChanged || listenerSpecHashChanged) {
		log.Info("RunnerScaleSetListener is out of date. Deleting it so that it is recreated", "name", listener.Name)
		if err := r.Delete(ctx, listener); err != nil {
			if kerrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			log.Error(err, "Failed to delete AutoscalingListener resource")
			return ctrl.Result{}, err
		}

		log.Info("Deleted RunnerScaleSetListener since existing one is out of date")
		return ctrl.Result{}, nil
	}

	if latestRunnerSet.Annotations[annotationKeyRunnerSpecHash] != autoscalingRunnerSet.RunnerSetSpecHash() {
		if r.drainingJobs(&latestRunnerSet.Status) {
			log.Info("Latest runner set spec hash does not match the current autoscaling runner set. Waiting for the running and pending runners to finish:", "running", latestRunnerSet.Status.RunningEphemeralRunners, "pending", latestRunnerSet.Status.PendingEphemeralRunners)
			log.Info("Scaling down the number of desired replicas to 0")
			// We are in the process of draining the jobs. The listener has been deleted and the ephemeral runner set replicas
			// need to scale down to 0
			err := patch(ctx, r.Client, latestRunnerSet, func(obj *v1alpha1.EphemeralRunnerSet) {
				obj.Spec.Replicas = 0
			})
			if err != nil {
				log.Error(err, "Failed to patch runner set to set desired count to 0")
			}
			return ctrl.Result{}, err
		}
		log.Info("Latest runner set spec hash does not match the current autoscaling runner set. Creating a new runner set")
		return r.createEphemeralRunnerSet(ctx, autoscalingRunnerSet, log)
	}

	oldRunnerSets := existingRunnerSets.old()
	if len(oldRunnerSets) > 0 {
		log.Info("Cleanup old ephemeral runner sets", "count", len(oldRunnerSets))
		err := r.deleteEphemeralRunnerSets(ctx, oldRunnerSets, log)
		if err != nil {
			log.Error(err, "Failed to clean up old runner sets")
			return ctrl.Result{}, err
		}
	}

	// Make sure the AutoscalingListener is up and running in the controller namespace
	if !listenerFound {
		if r.drainingJobs(&latestRunnerSet.Status) {
			log.Info("Creating a new AutoscalingListener is waiting for the running and pending runners to finish. Waiting for the running and pending runners to finish:", "running", latestRunnerSet.Status.RunningEphemeralRunners, "pending", latestRunnerSet.Status.PendingEphemeralRunners)
			return ctrl.Result{}, nil
		}
		log.Info("Creating a new AutoscalingListener for the runner set", "ephemeralRunnerSetName", latestRunnerSet.Name)
		return r.createAutoScalingListenerForRunnerSet(ctx, autoscalingRunnerSet, latestRunnerSet, log)
	}

	// Update the status of autoscaling runner set.
	if latestRunnerSet.Status.CurrentReplicas != autoscalingRunnerSet.Status.CurrentRunners {
		if err := patchSubResource(ctx, r.Status(), autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
			obj.Status.CurrentRunners = latestRunnerSet.Status.CurrentReplicas
			obj.Status.PendingEphemeralRunners = latestRunnerSet.Status.PendingEphemeralRunners
			obj.Status.RunningEphemeralRunners = latestRunnerSet.Status.RunningEphemeralRunners
			obj.Status.FailedEphemeralRunners = latestRunnerSet.Status.FailedEphemeralRunners
		}); err != nil {
			log.Error(err, "Failed to update autoscaling runner set status with current runner count")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// Prevents overprovisioning of runners.
// We reach this code path when runner scale set has been patched with a new runner spec but there are still running ephemeral runners.
// The safest approach is to wait for the running ephemeral runners to finish before creating a new runner set.
func (r *AutoscalingRunnerSetReconciler) drainingJobs(latestRunnerSetStatus *v1alpha1.EphemeralRunnerSetStatus) bool {
	if r.UpdateStrategy == UpdateStrategyEventual && ((latestRunnerSetStatus.RunningEphemeralRunners + latestRunnerSetStatus.PendingEphemeralRunners) > 0) {
		return true
	}
	return false
}

func (r *AutoscalingRunnerSetReconciler) cleanupListener(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (done bool, err error) {
	logger.Info("Cleaning up the listener")
	var listener v1alpha1.AutoscalingListener
	err = r.Get(ctx, client.ObjectKey{Namespace: r.ControllerNamespace, Name: scaleSetListenerName(autoscalingRunnerSet)}, &listener)
	switch {
	case err == nil:
		if listener.ObjectMeta.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener")
			if err := r.Delete(ctx, &listener); err != nil {
				return false, fmt.Errorf("failed to delete listener: %v", err)
			}
		}
		return false, nil
	case err != nil && !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener: %v", err)
	}

	logger.Info("Listener is deleted")
	return true, nil
}

func (r *AutoscalingRunnerSetReconciler) cleanupEphemeralRunnerSets(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (done bool, err error) {
	logger.Info("Cleaning up ephemeral runner sets")
	runnerSets, err := r.listEphemeralRunnerSets(ctx, autoscalingRunnerSet)
	if err != nil {
		return false, fmt.Errorf("failed to list ephemeral runner sets: %v", err)
	}
	if runnerSets.empty() {
		logger.Info("All ephemeral runner sets are deleted")
		return true, nil
	}

	logger.Info("Deleting all ephemeral runner sets", "count", runnerSets.count())
	if err := r.deleteEphemeralRunnerSets(ctx, runnerSets.all(), logger); err != nil {
		return false, fmt.Errorf("failed to delete ephemeral runner sets: %v", err)
	}
	return false, nil
}

func (r *AutoscalingRunnerSetReconciler) deleteEphemeralRunnerSets(ctx context.Context, oldRunnerSets []v1alpha1.EphemeralRunnerSet, logger logr.Logger) error {
	for i := range oldRunnerSets {
		rs := &oldRunnerSets[i]
		// already deleted but contains finalizer so it still exists
		if !rs.ObjectMeta.DeletionTimestamp.IsZero() {
			logger.Info("Skip ephemeral runner set since it is already marked for deletion", "name", rs.Name)
			continue
		}
		logger.Info("Deleting ephemeral runner set", "name", rs.Name)
		if err := r.Delete(ctx, rs); err != nil {
			return fmt.Errorf("failed to delete EphemeralRunnerSet resource: %v", err)
		}
		logger.Info("Deleted ephemeral runner set", "name", rs.Name)
	}
	return nil
}

func (r *AutoscalingRunnerSetReconciler) removeFinalizersFromDependentResources(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (requeue bool, err error) {
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

	requeue, err = c.result()
	if err != nil {
		logger.Error(err, "Failed to cleanup finalizer from dependent resource")
		return true, err
	}
	return requeue, nil
}

func (r *AutoscalingRunnerSetReconciler) createRunnerScaleSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Creating a new runner scale set")
	actionsClient, err := r.actionsClientFor(ctx, autoscalingRunnerSet)
	if len(autoscalingRunnerSet.Spec.RunnerScaleSetName) == 0 {
		autoscalingRunnerSet.Spec.RunnerScaleSetName = autoscalingRunnerSet.Name
	}
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for creating a new runner scale set")
		return ctrl.Result{}, err
	}

	runnerGroupId := 1
	if len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 {
		runnerGroup, err := actionsClient.GetRunnerGroupByName(ctx, autoscalingRunnerSet.Spec.RunnerGroup)
		if err != nil {
			logger.Error(err, "Failed to get runner group by name", "runnerGroup", autoscalingRunnerSet.Spec.RunnerGroup)
			return ctrl.Result{}, err
		}

		runnerGroupId = int(runnerGroup.ID)
	}

	runnerScaleSet, err := actionsClient.GetRunnerScaleSet(ctx, runnerGroupId, autoscalingRunnerSet.Spec.RunnerScaleSetName)
	if err != nil {
		logger.Error(err, "Failed to get runner scale set from Actions service",
			"runnerGroupId",
			strconv.Itoa(runnerGroupId),
			"runnerScaleSetName",
			autoscalingRunnerSet.Spec.RunnerScaleSetName)
		return ctrl.Result{}, err
	}

	if runnerScaleSet == nil {
		runnerScaleSet, err = actionsClient.CreateRunnerScaleSet(
			ctx,
			&actions.RunnerScaleSet{
				Name:          autoscalingRunnerSet.Spec.RunnerScaleSetName,
				RunnerGroupId: runnerGroupId,
				Labels: []actions.Label{
					{
						Name: autoscalingRunnerSet.Spec.RunnerScaleSetName,
						Type: "System",
					},
				},
				RunnerSetting: actions.RunnerSetting{
					Ephemeral:     true,
					DisableUpdate: true,
				},
			})
		if err != nil {
			logger.Error(err, "Failed to create a new runner scale set on Actions service")
			return ctrl.Result{}, err
		}
	}

	actionsClient.SetUserAgent(actions.UserAgentInfo{
		Version:    build.Version,
		CommitSHA:  build.CommitSHA,
		ScaleSetID: runnerScaleSet.Id,
		HasProxy:   autoscalingRunnerSet.Spec.Proxy != nil,
		Subsystem:  "controller",
	})

	logger.Info("Created/Reused a runner scale set", "id", runnerScaleSet.Id, "runnerGroupName", runnerScaleSet.RunnerGroupName)
	if autoscalingRunnerSet.Annotations == nil {
		autoscalingRunnerSet.Annotations = map[string]string{}
	}
	if autoscalingRunnerSet.Labels == nil {
		autoscalingRunnerSet.Labels = map[string]string{}
	}

	logger.Info("Adding runner scale set ID, name and runner group name as an annotation and url labels")
	if err = patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
		obj.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = runnerScaleSet.Name
		obj.Annotations[runnerScaleSetIdAnnotationKey] = strconv.Itoa(runnerScaleSet.Id)
		obj.Annotations[AnnotationKeyGitHubRunnerGroupName] = runnerScaleSet.RunnerGroupName
		if err := applyGitHubURLLabels(obj.Spec.GitHubConfigUrl, obj.Labels); err != nil { // should never happen
			logger.Error(err, "Failed to apply GitHub URL labels")
		}
	}); err != nil {
		logger.Error(err, "Failed to add runner scale set ID, name and runner group name as an annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated with runner scale set ID, name and runner group name as an annotation",
		"id", runnerScaleSet.Id,
		"name", runnerScaleSet.Name,
		"runnerGroupName", runnerScaleSet.RunnerGroupName)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) updateRunnerScaleSetRunnerGroup(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	runnerScaleSetId, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIdAnnotationKey])
	if err != nil {
		logger.Error(err, "Failed to parse runner scale set ID")
		return ctrl.Result{}, err
	}

	actionsClient, err := r.actionsClientFor(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for updating a existing runner scale set")
		return ctrl.Result{}, err
	}

	runnerGroupId := 1
	if len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 {
		runnerGroup, err := actionsClient.GetRunnerGroupByName(ctx, autoscalingRunnerSet.Spec.RunnerGroup)
		if err != nil {
			logger.Error(err, "Failed to get runner group by name", "runnerGroup", autoscalingRunnerSet.Spec.RunnerGroup)
			return ctrl.Result{}, err
		}

		runnerGroupId = int(runnerGroup.ID)
	}

	updatedRunnerScaleSet, err := actionsClient.UpdateRunnerScaleSet(ctx, runnerScaleSetId, &actions.RunnerScaleSet{RunnerGroupId: runnerGroupId})
	if err != nil {
		logger.Error(err, "Failed to update runner scale set", "runnerScaleSetId", runnerScaleSetId)
		return ctrl.Result{}, err
	}

	logger.Info("Updating runner scale set name and runner group name as annotations")
	if err := patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
		obj.Annotations[AnnotationKeyGitHubRunnerGroupName] = updatedRunnerScaleSet.RunnerGroupName
		obj.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = updatedRunnerScaleSet.Name
	}); err != nil {
		logger.Error(err, "Failed to update runner group name annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated runner scale set with match runner group", "runnerGroup", updatedRunnerScaleSet.RunnerGroupName)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) updateRunnerScaleSetName(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	runnerScaleSetId, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIdAnnotationKey])
	if err != nil {
		logger.Error(err, "Failed to parse runner scale set ID")
		return ctrl.Result{}, err
	}

	if len(autoscalingRunnerSet.Spec.RunnerScaleSetName) == 0 {
		logger.Info("Runner scale set name is not specified, skipping")
		return ctrl.Result{}, nil
	}

	actionsClient, err := r.actionsClientFor(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for updating a existing runner scale set")
		return ctrl.Result{}, err
	}

	updatedRunnerScaleSet, err := actionsClient.UpdateRunnerScaleSet(ctx, runnerScaleSetId, &actions.RunnerScaleSet{Name: autoscalingRunnerSet.Spec.RunnerScaleSetName})
	if err != nil {
		logger.Error(err, "Failed to update runner scale set", "runnerScaleSetId", runnerScaleSetId)
		return ctrl.Result{}, err
	}

	logger.Info("Updating runner scale set name as an annotation")
	if err := patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
		obj.Annotations[AnnotationKeyGitHubRunnerScaleSetName] = updatedRunnerScaleSet.Name
	}); err != nil {
		logger.Error(err, "Failed to update runner scale set name annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated runner scale set with match name", "name", updatedRunnerScaleSet.Name)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) deleteRunnerScaleSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) error {
	scaleSetId, ok := autoscalingRunnerSet.Annotations[runnerScaleSetIdAnnotationKey]
	if !ok {
		// Annotation not being present can occur in 3 scenarios
		// 1. Scale set is never created.
		//    In this case, we don't need to fetch the actions client to delete the scale set that does not exist
		//
		// 2. The scale set has been deleted by the controller.
		//    In that case, the controller will clean up annotation because the scale set does not exist anymore.
		//    Removal of the scale set id is also useful because permission cleanup will eventually lose permission
		//    assigned to it on a GitHub secret, causing actions client from secret to result in permission denied
		//
		// 3. Annotation is removed manually.
		//    In this case, the controller will treat this as if the scale set is being removed from the actions service
		//    Then, manual deletion of the scale set is required.
		return nil
	}
	logger.Info("Deleting the runner scale set from Actions service")
	runnerScaleSetId, err := strconv.Atoi(scaleSetId)
	if err != nil {
		// If the annotation is not set correctly, we are going to get stuck in a loop trying to parse the scale set id.
		// If the configuration is invalid (secret does not exist for example), we never got to the point to create runner set.
		// But then, manual cleanup would get stuck finalizing the resource trying to parse annotation indefinitely
		logger.Info("autoscaling runner set does not have annotation describing scale set id. Skip deletion", "err", err.Error())
		return nil
	}

	actionsClient, err := r.actionsClientFor(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for updating a existing runner scale set")
		return err
	}

	err = actionsClient.DeleteRunnerScaleSet(ctx, runnerScaleSetId)
	if err != nil {
		logger.Error(err, "Failed to delete runner scale set", "runnerScaleSetId", runnerScaleSetId)
		return err
	}

	err = patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
		delete(obj.Annotations, runnerScaleSetIdAnnotationKey)
	})
	if err != nil {
		logger.Error(err, "Failed to patch autoscaling runner set with annotation removed", "annotation", runnerScaleSetIdAnnotationKey)
		return err
	}

	logger.Info("Deleted the runner scale set from Actions service")
	return nil
}

func (r *AutoscalingRunnerSetReconciler) createEphemeralRunnerSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, log logr.Logger) (ctrl.Result, error) {
	desiredRunnerSet, err := r.resourceBuilder.newEphemeralRunnerSet(autoscalingRunnerSet)
	if err != nil {
		log.Error(err, "Could not create EphemeralRunnerSet")
		return ctrl.Result{}, err
	}

	if err := ctrl.SetControllerReference(autoscalingRunnerSet, desiredRunnerSet, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference to a new EphemeralRunnerSet")
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

	autoscalingListener, err := r.resourceBuilder.newAutoScalingListener(autoscalingRunnerSet, ephemeralRunnerSet, r.ControllerNamespace, r.DefaultRunnerScaleSetListenerImage, imagePullSecrets)
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

func (r *AutoscalingRunnerSetReconciler) listEphemeralRunnerSets(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) (*EphemeralRunnerSets, error) {
	list := new(v1alpha1.EphemeralRunnerSetList)
	if err := r.List(ctx, list, client.InNamespace(autoscalingRunnerSet.Namespace), client.MatchingFields{resourceOwnerKey: autoscalingRunnerSet.Name}); err != nil {
		return nil, fmt.Errorf("failed to list ephemeral runner sets: %v", err)
	}

	return &EphemeralRunnerSets{list: list}, nil
}

func (r *AutoscalingRunnerSetReconciler) actionsClientFor(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) (actions.ActionsService, error) {
	var configSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: autoscalingRunnerSet.Spec.GitHubConfigSecret}, &configSecret); err != nil {
		return nil, fmt.Errorf("failed to find GitHub config secret: %w", err)
	}

	opts, err := r.actionsClientOptionsFor(ctx, autoscalingRunnerSet)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions client options: %w", err)
	}

	return r.ActionsClient.GetClientFromSecret(
		ctx,
		autoscalingRunnerSet.Spec.GitHubConfigUrl,
		autoscalingRunnerSet.Namespace,
		configSecret.Data,
		opts...,
	)
}

func (r *AutoscalingRunnerSetReconciler) actionsClientOptionsFor(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) ([]actions.ClientOption, error) {
	var options []actions.ClientOption

	if autoscalingRunnerSet.Spec.Proxy != nil {
		proxyFunc, err := autoscalingRunnerSet.Spec.Proxy.ProxyFunc(func(s string) (*corev1.Secret, error) {
			var secret corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: s}, &secret)
			if err != nil {
				return nil, fmt.Errorf("failed to get proxy secret %s: %w", s, err)
			}

			return &secret, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get proxy func: %w", err)
		}

		options = append(options, actions.WithProxy(proxyFunc))
	}

	tlsConfig := autoscalingRunnerSet.Spec.GitHubServerTLS
	if tlsConfig != nil {
		pool, err := tlsConfig.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := r.Get(
				ctx,
				types.NamespacedName{
					Namespace: autoscalingRunnerSet.Namespace,
					Name:      name,
				},
				&configmap,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get configmap %s: %w", name, err)
			}

			return []byte(configmap.Data[key]), nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tls config: %w", err)
		}

		options = append(options, actions.WithRootCAs(pool))
	}

	return options, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutoscalingRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	groupVersionIndexer := func(rawObj client.Object) []string {
		groupVersion := v1alpha1.GroupVersion.String()
		owner := metav1.GetControllerOf(rawObj)
		if owner == nil {
			return nil
		}

		// ...make sure it is owned by this controller
		if owner.APIVersion != groupVersion || owner.Kind != "AutoscalingRunnerSet" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.EphemeralRunnerSet{}, resourceOwnerKey, groupVersionIndexer); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
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
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Complete(r)
}

type autoscalingRunnerSetFinalizerDependencyCleaner struct {
	// configuration fields
	client               client.Client
	autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	logger               logr.Logger

	// fields to operate on
	requeue bool
	err     error
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) result() (requeue bool, err error) {
	return c.requeue, c.err
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeKubernetesModeRoleBindingFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
		err = patch(ctx, c.client, roleBinding, func(obj *rbacv1.RoleBinding) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch kubernetes mode role binding without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from container mode kubernetes role binding", "name", roleBindingName)
		return
	case err != nil && !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch kubernetes mode role binding: %w", err)
		return
	default:
		c.logger.Info("Container mode kubernetes role binding has already been deleted", "name", roleBindingName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeKubernetesModeRoleFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
		err = patch(ctx, c.client, role, func(obj *rbacv1.Role) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch kubernetes mode role without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from container mode kubernetes role")
		return
	case err != nil && !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch kubernetes mode role: %w", err)
		return
	default:
		c.logger.Info("Container mode kubernetes role has already been deleted", "name", roleName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeKubernetesModeServiceAccountFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
		err = patch(ctx, c.client, serviceAccount, func(obj *corev1.ServiceAccount) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch kubernetes mode service account without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from container mode kubernetes service account")
		return
	case err != nil && !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch kubernetes mode service account: %w", err)
		return
	default:
		c.logger.Info("Container mode kubernetes service account has already been deleted", "name", serviceAccountName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeNoPermissionServiceAccountFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
	err := c.client.Get(ctx, types.NamespacedName{Name: serviceAccountName, Namespace: c.autoscalingRunnerSet.Namespace}, serviceAccount)
	switch {
	case err == nil:
		if !controllerutil.ContainsFinalizer(serviceAccount, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("No permission service account finalizer has already been removed", "name", serviceAccountName)
			return
		}
		err = patch(ctx, c.client, serviceAccount, func(obj *corev1.ServiceAccount) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch service account without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from no permission service account", "name", serviceAccountName)
		return
	case err != nil && !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch service account: %w", err)
		return
	default:
		c.logger.Info("No permission service account has already been deleted", "name", serviceAccountName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeGitHubSecretFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
		err = patch(ctx, c.client, githubSecret, func(obj *corev1.Secret) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch GitHub secret without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from GitHub secret", "name", githubSecretName)
		return
	case err != nil && !kerrors.IsNotFound(err) && !kerrors.IsForbidden(err):
		c.err = fmt.Errorf("failed to fetch GitHub secret: %w", err)
		return
	default:
		c.logger.Info("GitHub secret has already been deleted", "name", githubSecretName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeManagerRoleBindingFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
		if !controllerutil.ContainsFinalizer(roleBinding, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("Manager role binding finalizer has already been removed", "name", managerRoleBindingName)
			return
		}
		err = patch(ctx, c.client, roleBinding, func(obj *rbacv1.RoleBinding) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch manager role binding without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from manager role binding", "name", managerRoleBindingName)
		return
	case err != nil && !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch manager role binding: %w", err)
		return
	default:
		c.logger.Info("Manager role binding has already been deleted", "name", managerRoleBindingName)
		return
	}
}

func (c *autoscalingRunnerSetFinalizerDependencyCleaner) removeManagerRoleFinalizer(ctx context.Context) {
	if c.requeue || c.err != nil {
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
		if !controllerutil.ContainsFinalizer(role, AutoscalingRunnerSetCleanupFinalizerName) {
			c.logger.Info("Manager role finalizer has already been removed", "name", managerRoleName)
			return
		}
		err = patch(ctx, c.client, role, func(obj *rbacv1.Role) {
			controllerutil.RemoveFinalizer(obj, AutoscalingRunnerSetCleanupFinalizerName)
		})
		if err != nil {
			c.err = fmt.Errorf("failed to patch manager role without finalizer: %w", err)
			return
		}
		c.requeue = true
		c.logger.Info("Removed finalizer from manager role", "name", managerRoleName)
		return
	case err != nil && !kerrors.IsNotFound(err):
		c.err = fmt.Errorf("failed to fetch manager role: %w", err)
		return
	default:
		c.logger.Info("Manager role has already been deleted", "name", managerRoleName)
		return
	}
}

// NOTE: if this is logic should be used for other resources,
// consider using generics
type EphemeralRunnerSets struct {
	list   *v1alpha1.EphemeralRunnerSetList
	sorted bool
}

func (rs *EphemeralRunnerSets) latest() *v1alpha1.EphemeralRunnerSet {
	if rs.empty() {
		return nil
	}
	if !rs.sorted {
		rs.sort()
	}
	return rs.list.Items[0].DeepCopy()
}

func (rs *EphemeralRunnerSets) old() []v1alpha1.EphemeralRunnerSet {
	if rs.empty() {
		return nil
	}
	if !rs.sorted {
		rs.sort()
	}
	copy := rs.list.DeepCopy()
	return copy.Items[1:]
}

func (rs *EphemeralRunnerSets) all() []v1alpha1.EphemeralRunnerSet {
	if rs.empty() {
		return nil
	}
	copy := rs.list.DeepCopy()
	return copy.Items
}

func (rs *EphemeralRunnerSets) empty() bool {
	return rs.list == nil || len(rs.list.Items) == 0
}

func (rs *EphemeralRunnerSets) sort() {
	sort.Slice(rs.list.Items, func(i, j int) bool {
		return rs.list.Items[i].GetCreationTimestamp().After(rs.list.Items[j].GetCreationTimestamp().Time)
	})
}

func (rs *EphemeralRunnerSets) count() int {
	return len(rs.list.Items)
}
