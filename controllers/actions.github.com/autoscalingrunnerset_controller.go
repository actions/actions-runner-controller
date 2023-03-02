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
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	// TODO: Replace with shared image.
	autoscalingRunnerSetOwnerKey      = ".metadata.controller"
	LabelKeyRunnerSpecHash            = "runner-spec-hash"
	autoscalingRunnerSetFinalizerName = "autoscalingrunnerset.actions.github.com/finalizer"
	runnerScaleSetIdKey               = "runner-scale-set-id"
	runnerScaleSetRunnerGroupNameKey  = "runner-scale-set-runner-group-name"
)

// AutoscalingRunnerSetReconciler reconciles a AutoscalingRunnerSet object
type AutoscalingRunnerSetReconciler struct {
	client.Client
	Log                                           logr.Logger
	Scheme                                        *runtime.Scheme
	ControllerNamespace                           string
	DefaultRunnerScaleSetListenerImage            string
	DefaultRunnerScaleSetListenerImagePullSecrets []string
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

		log.Info("Removing finalizer")
		err = patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
			controllerutil.RemoveFinalizer(obj, autoscalingRunnerSetFinalizerName)
		})
		if err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to update autoscaling runner set without finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully removed finalizer after cleanup")
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

	scaleSetIdRaw, ok := autoscalingRunnerSet.Annotations[runnerScaleSetIdKey]
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
	currentRunnerGroupName, ok := autoscalingRunnerSet.Annotations[runnerScaleSetRunnerGroupNameKey]
	if !ok || (len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 && !strings.EqualFold(currentRunnerGroupName, autoscalingRunnerSet.Spec.RunnerGroup)) {
		log.Info("AutoScalingRunnerSet runner group changed. Updating the runner scale set.")
		return r.updateRunnerScaleSetRunnerGroup(ctx, autoscalingRunnerSet, log)
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

	desiredSpecHash := autoscalingRunnerSet.RunnerSetSpecHash()
	for _, runnerSet := range existingRunnerSets.all() {
		log.Info("Find existing ephemeral runner set", "name", runnerSet.Name, "specHash", runnerSet.Labels[LabelKeyRunnerSpecHash])
	}

	if desiredSpecHash != latestRunnerSet.Labels[LabelKeyRunnerSpecHash] {
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
	listener := new(v1alpha1.AutoscalingListener)
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.ControllerNamespace, Name: scaleSetListenerName(autoscalingRunnerSet)}, listener); err != nil {
		if kerrors.IsNotFound(err) {
			// We don't have a listener
			log.Info("Creating a new AutoscalingListener for the runner set", "ephemeralRunnerSetName", latestRunnerSet.Name)
			return r.createAutoScalingListenerForRunnerSet(ctx, autoscalingRunnerSet, latestRunnerSet, log)
		}
		log.Error(err, "Failed to get AutoscalingListener resource")
		return ctrl.Result{}, err
	}

	// Our listener pod is out of date, so we need to delete it to get a new recreate.
	if listener.Labels[LabelKeyRunnerSpecHash] != autoscalingRunnerSet.ListenerSpecHash() {
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

	// Update the status of autoscaling runner set.
	if latestRunnerSet.Status.CurrentReplicas != autoscalingRunnerSet.Status.CurrentRunners {
		if err := patchSubResource(ctx, r.Status(), autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
			obj.Status.CurrentRunners = latestRunnerSet.Status.CurrentReplicas
		}); err != nil {
			log.Error(err, "Failed to update autoscaling runner set status with current runner count")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
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

func (r *AutoscalingRunnerSetReconciler) createRunnerScaleSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Creating a new runner scale set")
	actionsClient, err := r.actionsClientFor(ctx, autoscalingRunnerSet)
	if err != nil {
		logger.Error(err, "Failed to initialize Actions service client for creating a new runner scale set")
		return ctrl.Result{}, err
	}
	runnerScaleSet, err := actionsClient.GetRunnerScaleSet(ctx, autoscalingRunnerSet.Name)
	if err != nil {
		logger.Error(err, "Failed to get runner scale set from Actions service")
		return ctrl.Result{}, err
	}

	runnerGroupId := 1
	if runnerScaleSet == nil {
		if len(autoscalingRunnerSet.Spec.RunnerGroup) > 0 {
			runnerGroup, err := actionsClient.GetRunnerGroupByName(ctx, autoscalingRunnerSet.Spec.RunnerGroup)
			if err != nil {
				logger.Error(err, "Failed to get runner group by name", "runnerGroup", autoscalingRunnerSet.Spec.RunnerGroup)
				return ctrl.Result{}, err
			}

			runnerGroupId = int(runnerGroup.ID)
		}

		runnerScaleSet, err = actionsClient.CreateRunnerScaleSet(
			ctx,
			&actions.RunnerScaleSet{
				Name:          autoscalingRunnerSet.Name,
				RunnerGroupId: runnerGroupId,
				Labels: []actions.Label{
					{
						Name: autoscalingRunnerSet.Name,
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

	logger.Info("Created/Reused a runner scale set", "id", runnerScaleSet.Id, "runnerGroupName", runnerScaleSet.RunnerGroupName)
	if autoscalingRunnerSet.Annotations == nil {
		autoscalingRunnerSet.Annotations = map[string]string{}
	}

	logger.Info("Adding runner scale set ID and runner group name as an annotation")
	if err = patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
		obj.Annotations[runnerScaleSetIdKey] = strconv.Itoa(runnerScaleSet.Id)
		obj.Annotations[runnerScaleSetRunnerGroupNameKey] = runnerScaleSet.RunnerGroupName
	}); err != nil {
		logger.Error(err, "Failed to add runner scale set ID and runner group name as an annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated with runner scale set ID and runner group name as an annotation")
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) updateRunnerScaleSetRunnerGroup(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) (ctrl.Result, error) {
	runnerScaleSetId, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIdKey])
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

	updatedRunnerScaleSet, err := actionsClient.UpdateRunnerScaleSet(ctx, runnerScaleSetId, &actions.RunnerScaleSet{Name: autoscalingRunnerSet.Name, RunnerGroupId: runnerGroupId})
	if err != nil {
		logger.Error(err, "Failed to update runner scale set", "runnerScaleSetId", runnerScaleSetId)
		return ctrl.Result{}, err
	}

	logger.Info("Updating runner scale set runner group name as an annotation")
	if err := patch(ctx, r.Client, autoscalingRunnerSet, func(obj *v1alpha1.AutoscalingRunnerSet) {
		obj.Annotations[runnerScaleSetRunnerGroupNameKey] = updatedRunnerScaleSet.RunnerGroupName
	}); err != nil {
		logger.Error(err, "Failed to update runner group name annotation")
		return ctrl.Result{}, err
	}

	logger.Info("Updated runner scale set with match runner group", "runnerGroup", updatedRunnerScaleSet.RunnerGroupName)
	return ctrl.Result{}, nil
}

func (r *AutoscalingRunnerSetReconciler) deleteRunnerScaleSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, logger logr.Logger) error {
	logger.Info("Deleting the runner scale set from Actions service")
	runnerScaleSetId, err := strconv.Atoi(autoscalingRunnerSet.Annotations[runnerScaleSetIdKey])
	if err != nil {
		// If the annotation is not set correctly, or if it does not exist, we are going to get stuck in a loop trying to parse the scale set id.
		// If the configuration is invalid (secret does not exist for example), we never get to the point to create runner set. But then, manual cleanup
		// would get stuck finalizing the resource trying to parse annotation indefinitely
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
	if err := r.List(ctx, list, client.InNamespace(autoscalingRunnerSet.Namespace), client.MatchingFields{autoscalingRunnerSetOwnerKey: autoscalingRunnerSet.Name}); err != nil {
		return nil, fmt.Errorf("failed to list ephemeral runner sets: %v", err)
	}

	return &EphemeralRunnerSets{list: list}, nil
}

func (r *AutoscalingRunnerSetReconciler) actionsClientFor(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) (actions.ActionsService, error) {
	var configSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: autoscalingRunnerSet.Spec.GitHubConfigSecret}, &configSecret); err != nil {
		return nil, fmt.Errorf("failed to find GitHub config secret: %w", err)
	}

	var opts []actions.ClientOption
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

		opts = append(opts, actions.WithProxy(proxyFunc))
	}

	return r.ActionsClient.GetClientFromSecret(
		ctx,
		autoscalingRunnerSet.Spec.GitHubConfigUrl,
		autoscalingRunnerSet.Namespace,
		configSecret.Data,
		opts...,
	)
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

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.EphemeralRunnerSet{}, autoscalingRunnerSetOwnerKey, groupVersionIndexer); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AutoscalingRunnerSet{}).
		Owns(&v1alpha1.EphemeralRunnerSet{}).
		Watches(&source.Kind{Type: &v1alpha1.AutoscalingListener{}}, handler.EnqueueRequestsFromMapFunc(
			func(o client.Object) []reconcile.Request {
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
