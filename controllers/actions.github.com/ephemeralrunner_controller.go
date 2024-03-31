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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
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
	// EphemeralRunnerContainerName is the name of the runner container.
	// It represents the name of the container running the self-hosted runner image.
	EphemeralRunnerContainerName = "runner"

	ephemeralRunnerFinalizerName        = "ephemeralrunner.actions.github.com/finalizer"
	ephemeralRunnerActionsFinalizerName = "ephemeralrunner.actions.github.com/runner-registration-finalizer"
)

// EphemeralRunnerReconciler reconciles a EphemeralRunner object
type EphemeralRunnerReconciler struct {
	client.Client
	Log             logr.Logger
	Scheme          *runtime.Scheme
	ActionsClient   actions.MultiClient
	resourceBuilder resourceBuilder
}

// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=create;get;list;watch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.6.4/pkg/reconcile
func (r *EphemeralRunnerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ephemeralrunner", req.NamespacedName)

	ephemeralRunner := new(v1alpha1.EphemeralRunner)
	if err := r.Get(ctx, req.NamespacedName, ephemeralRunner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !ephemeralRunner.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(ephemeralRunner, ephemeralRunnerFinalizerName) {
			return ctrl.Result{}, nil
		}

		if controllerutil.ContainsFinalizer(ephemeralRunner, ephemeralRunnerActionsFinalizerName) {
			switch ephemeralRunner.Status.Phase {
			case corev1.PodSucceeded:
				// deleted by the runner set, we can just remove finalizer without API calls
				err := patch(ctx, r.Client, ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
					controllerutil.RemoveFinalizer(obj, ephemeralRunnerActionsFinalizerName)
				})
				if err != nil {
					log.Error(err, "Failed to update ephemeral runner without runner registration finalizer")
					return ctrl.Result{}, err
				}
				log.Info("Successfully removed runner registration finalizer")
				return ctrl.Result{}, nil
			default:
				return r.cleanupRunnerFromService(ctx, ephemeralRunner, log)
			}
		}

		log.Info("Finalizing ephemeral runner")
		done, err := r.cleanupResources(ctx, ephemeralRunner, log)
		if err != nil {
			log.Error(err, "Failed to clean up ephemeral runner owned resources")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for ephemeral runner owned resources to be deleted")
			return ctrl.Result{Requeue: true}, nil
		}

		done, err = r.cleanupContainerHooksResources(ctx, ephemeralRunner, log)
		if err != nil {
			log.Error(err, "Failed to clean up container hooks resources")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for container hooks resources to be deleted")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		log.Info("Removing finalizer")
		err = patch(ctx, r.Client, ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
			controllerutil.RemoveFinalizer(obj, ephemeralRunnerFinalizerName)
		})
		if err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to update ephemeral runner without the finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully removed finalizer after cleanup")
		return ctrl.Result{}, nil
	}

	if ephemeralRunner.IsDone() {
		log.Info("Cleaning up resources after after ephemeral runner termination", "phase", ephemeralRunner.Status.Phase)
		done, err := r.cleanupResources(ctx, ephemeralRunner, log)
		if err != nil {
			log.Error(err, "Failed to clean up ephemeral runner owned resources")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for ephemeral runner owned resources to be deleted")
			return ctrl.Result{Requeue: true}, nil
		}
		// Stop reconciling on this object.
		// The EphemeralRunnerSet is responsible for cleaning it up.
		log.Info("EphemeralRunner has already finished. Stopping reconciliation and waiting for EphemeralRunnerSet to clean it up", "phase", ephemeralRunner.Status.Phase)
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(ephemeralRunner, ephemeralRunnerActionsFinalizerName) {
		log.Info("Adding runner registration finalizer")
		err := patch(ctx, r.Client, ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
			controllerutil.AddFinalizer(obj, ephemeralRunnerActionsFinalizerName)
		})
		if err != nil {
			log.Error(err, "Failed to update with runner registration finalizer set")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added runner registration finalizer")
	}

	if !controllerutil.ContainsFinalizer(ephemeralRunner, ephemeralRunnerFinalizerName) {
		log.Info("Adding finalizer")
		if err := patch(ctx, r.Client, ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
			controllerutil.AddFinalizer(obj, ephemeralRunnerFinalizerName)
		}); err != nil {
			log.Error(err, "Failed to update with finalizer set")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	if ephemeralRunner.Status.RunnerId == 0 {
		log.Info("Creating new ephemeral runner registration and updating status with runner config")
		return r.updateStatusWithRunnerConfig(ctx, ephemeralRunner, log)
	}

	secret := new(corev1.Secret)
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to fetch secret")
			return ctrl.Result{}, err
		}
		// create secret if not created
		log.Info("Creating new ephemeral runner secret for jitconfig.")
		return r.createSecret(ctx, ephemeralRunner, log)
	}

	pod := new(corev1.Pod)
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		switch {
		case !kerrors.IsNotFound(err):
			log.Error(err, "Failed to fetch the pod")
			return ctrl.Result{}, err

		case len(ephemeralRunner.Status.Failures) > 5:
			log.Info("EphemeralRunner has failed more than 5 times. Marking it as failed")
			errMessage := fmt.Sprintf("Pod has failed to start more than 5 times: %s", pod.Status.Message)
			if err := r.markAsFailed(ctx, ephemeralRunner, errMessage, ReasonTooManyPodFailures, log); err != nil {
				log.Error(err, "Failed to set ephemeral runner to phase Failed")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil

		default:
			// Pod was not found. Create if the pod has never been created
			log.Info("Creating new EphemeralRunner pod.")
			result, err := r.createPod(ctx, ephemeralRunner, secret, log)
			switch {
			case err == nil:
				return result, nil
			case kerrors.IsInvalid(err) || kerrors.IsForbidden(err):
				log.Error(err, "Failed to create a pod due to unrecoverable failure")
				errMessage := fmt.Sprintf("Failed to create the pod: %v", err)
				if err := r.markAsFailed(ctx, ephemeralRunner, errMessage, ReasonInvalidPodFailure, log); err != nil {
					log.Error(err, "Failed to set ephemeral runner to phase Failed")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			default:
				log.Error(err, "Failed to create the pod")
				return ctrl.Result{}, err
			}
		}
	}

	cs := runnerContainerStatus(pod)
	switch {
	case cs == nil:
		// starting, no container state yet
		log.Info("Waiting for runner container status to be available")
		return ctrl.Result{}, nil
	case cs.State.Terminated == nil: // still running or evicted
		if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == "Evicted" {
			log.Info("Pod set the termination phase, but container state is not terminated. Deleting pod",
				"PodPhase", pod.Status.Phase,
				"PodReason", pod.Status.Reason,
				"PodMessage", pod.Status.Message,
			)

			if err := r.deletePodAsFailed(ctx, ephemeralRunner, pod, log); err != nil {
				log.Error(err, "failed to delete pod as failed on pod.Status.Phase: Failed")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		log.Info("Ephemeral runner container is still running")
		if err := r.updateRunStatusFromPod(ctx, ephemeralRunner, pod, log); err != nil {
			log.Info("Failed to update ephemeral runner status. Requeue to not miss this event")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	case cs.State.Terminated.ExitCode != 0: // failed
		log.Info("Ephemeral runner container failed", "exitCode", cs.State.Terminated.ExitCode)
		if err := r.deletePodAsFailed(ctx, ephemeralRunner, pod, log); err != nil {
			log.Error(err, "Failed to delete runner pod on failure")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	default:
		// pod succeeded. We double-check with the service if the runner exists.
		// The reason is that image can potentially finish with status 0, but not pick up the job.
		existsInService, err := r.runnerRegisteredWithService(ctx, ephemeralRunner.DeepCopy(), log)
		if err != nil {
			log.Error(err, "Failed to check if runner is registered with the service")
			return ctrl.Result{}, err
		}
		if !existsInService {
			// the runner does not exist in the service, so it must be done
			log.Info("Ephemeral runner has finished since it does not exist in the service anymore")
			if err := r.markAsFinished(ctx, ephemeralRunner, log); err != nil {
				log.Error(err, "Failed to mark ephemeral runner as finished")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// The runner still exists. This can happen if the pod exited with 0 but fails to start
		log.Info("Ephemeral runner pod has finished, but the runner still exists in the service. Deleting the pod to restart it.")
		if err := r.deletePodAsFailed(ctx, ephemeralRunner, pod, log); err != nil {
			log.Error(err, "failed to delete a pod that still exists in the service")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
}

func (r *EphemeralRunnerReconciler) cleanupRunnerFromService(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (ctrl.Result, error) {
	actionsError := &actions.ActionsError{}
	err := r.deleteRunnerFromService(ctx, ephemeralRunner, log)
	if err != nil {
		if errors.As(err, &actionsError) &&
			actionsError.StatusCode == http.StatusBadRequest &&
			strings.Contains(actionsError.ExceptionName, "JobStillRunningException") {
			log.Info("Runner is still running the job. Re-queue in 30 seconds")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		log.Error(err, "Failed clean up runner from the service")
		return ctrl.Result{}, err
	}

	log.Info("Successfully removed runner registration from service")
	err = patch(ctx, r.Client, ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		controllerutil.RemoveFinalizer(obj, ephemeralRunnerActionsFinalizerName)
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Successfully removed runner registration finalizer")
	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerReconciler) cleanupResources(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (deleted bool, err error) {
	log.Info("Cleaning up the runner pod")
	pod := new(corev1.Pod)
	err = r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunner.Namespace, Name: ephemeralRunner.Name}, pod)
	switch {
	case err == nil:
		if pod.ObjectMeta.DeletionTimestamp.IsZero() {
			log.Info("Deleting the runner pod")
			if err := r.Delete(ctx, pod); err != nil && !kerrors.IsNotFound(err) {
				return false, fmt.Errorf("failed to delete pod: %v", err)
			}
		}
		return false, nil
	case !kerrors.IsNotFound(err):
		return false, err
	}
	log.Info("Pod is deleted")

	log.Info("Cleaning up the runner jitconfig secret")
	secret := new(corev1.Secret)
	err = r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunner.Namespace, Name: ephemeralRunner.Name}, secret)
	switch {
	case err == nil:
		if secret.ObjectMeta.DeletionTimestamp.IsZero() {
			log.Info("Deleting the jitconfig secret")
			if err := r.Delete(ctx, secret); err != nil && !kerrors.IsNotFound(err) {
				return false, fmt.Errorf("failed to delete secret: %v", err)
			}
		}
		return false, nil
	case !kerrors.IsNotFound(err):
		return false, err
	}
	log.Info("Secret is deleted")

	return true, nil
}

func (r *EphemeralRunnerReconciler) cleanupContainerHooksResources(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (done bool, err error) {
	log.Info("Cleaning up runner linked pods")
	done, err = r.cleanupRunnerLinkedPods(ctx, ephemeralRunner, log)
	if err != nil {
		return false, fmt.Errorf("failed to clean up runner linked pods: %v", err)
	}

	if !done {
		return false, nil
	}

	log.Info("Cleaning up runner linked secrets")
	done, err = r.cleanupRunnerLinkedSecrets(ctx, ephemeralRunner, log)
	if err != nil {
		return false, err
	}

	return done, nil
}

func (r *EphemeralRunnerReconciler) cleanupRunnerLinkedPods(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (done bool, err error) {
	runnerLinedLabels := client.MatchingLabels(
		map[string]string{
			"runner-pod": ephemeralRunner.Name,
		},
	)
	var runnerLinkedPodList corev1.PodList
	err = r.List(ctx, &runnerLinkedPodList, client.InNamespace(ephemeralRunner.Namespace), runnerLinedLabels)
	if err != nil {
		return false, fmt.Errorf("failed to list runner-linked pods: %v", err)
	}

	if len(runnerLinkedPodList.Items) == 0 {
		log.Info("Runner-linked pods are deleted")
		return true, nil
	}

	log.Info("Deleting container hooks runner-linked pods", "count", len(runnerLinkedPodList.Items))

	var errs []error
	for i := range runnerLinkedPodList.Items {
		linkedPod := &runnerLinkedPodList.Items[i]
		if !linkedPod.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}

		log.Info("Deleting container hooks runner-linked pod", "name", linkedPod.Name)
		if err := r.Delete(ctx, linkedPod); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete runner linked pod %q: %v", linkedPod.Name, err))
		}
	}

	return false, multierr.Combine(errs...)
}

func (r *EphemeralRunnerReconciler) cleanupRunnerLinkedSecrets(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (done bool, err error) {
	runnerLinkedLabels := client.MatchingLabels(
		map[string]string{
			"runner-pod": ephemeralRunner.ObjectMeta.Name,
		},
	)
	var runnerLinkedSecretList corev1.SecretList
	err = r.List(ctx, &runnerLinkedSecretList, client.InNamespace(ephemeralRunner.Namespace), runnerLinkedLabels)
	if err != nil {
		return false, fmt.Errorf("failed to list runner-linked secrets: %w", err)
	}

	if len(runnerLinkedSecretList.Items) == 0 {
		log.Info("Runner-linked secrets are deleted")
		return true, nil
	}

	log.Info("Deleting container hooks runner-linked secrets", "count", len(runnerLinkedSecretList.Items))

	var errs []error
	for i := range runnerLinkedSecretList.Items {
		s := &runnerLinkedSecretList.Items[i]
		if !s.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}

		log.Info("Deleting container hooks runner-linked secret", "name", s.Name)
		if err := r.Delete(ctx, s); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete runner linked secret %q: %v", s.Name, err))
		}
	}

	return false, multierr.Combine(errs...)
}

func (r *EphemeralRunnerReconciler) markAsFailed(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, errMessage string, reason string, log logr.Logger) error {
	log.Info("Updating ephemeral runner status to Failed")
	if err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		obj.Status.Phase = corev1.PodFailed
		obj.Status.Reason = reason
		obj.Status.Message = errMessage
	}); err != nil {
		return fmt.Errorf("failed to update ephemeral runner status Phase/Message: %v", err)
	}

	log.Info("Removing the runner from the service")
	if err := r.deleteRunnerFromService(ctx, ephemeralRunner, log); err != nil {
		return fmt.Errorf("failed to remove the runner from service: %v", err)
	}

	log.Info("EphemeralRunner is marked as Failed and deleted from the service")
	return nil
}

func (r *EphemeralRunnerReconciler) markAsFinished(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	log.Info("Updating ephemeral runner status to Finished")
	if err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		obj.Status.Phase = corev1.PodSucceeded
	}); err != nil {
		return fmt.Errorf("failed to update ephemeral runner with status finished: %v", err)
	}

	log.Info("EphemeralRunner status is marked as Finished")
	return nil
}

// deletePodAsFailed is responsible for deleting the pod and updating the .Status.Failures for tracking failure count.
// It should not be responsible for setting the status to Failed.
func (r *EphemeralRunnerReconciler) deletePodAsFailed(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, pod *corev1.Pod, log logr.Logger) error {
	if pod.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("Deleting the ephemeral runner pod", "podId", pod.UID)
		if err := r.Delete(ctx, pod); err != nil && !kerrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod with status failed: %v", err)
		}
	}

	log.Info("Updating ephemeral runner status to track the failure count")
	if err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		if obj.Status.Failures == nil {
			obj.Status.Failures = make(map[string]bool)
		}
		obj.Status.Failures[string(pod.UID)] = true
		obj.Status.Ready = false
		obj.Status.Reason = pod.Status.Reason
		obj.Status.Message = pod.Status.Message
	}); err != nil {
		return fmt.Errorf("failed to update ephemeral runner status: failed attempts: %v", err)
	}

	log.Info("EphemeralRunner pod is deleted and status is updated with failure count")
	return nil
}

// updateStatusWithRunnerConfig fetches runtime configuration needed by the runner
// This method should always set .status.runnerId and .status.runnerJITConfig
func (r *EphemeralRunnerReconciler) updateStatusWithRunnerConfig(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (ctrl.Result, error) {
	// Runner is not registered with the service. We need to register it first
	log.Info("Creating ephemeral runner JIT config")
	actionsClient, err := r.actionsClientFor(ctx, ephemeralRunner)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get actions client for generating JIT config: %v", err)
	}

	jitSettings := &actions.RunnerScaleSetJitRunnerSetting{
		Name: ephemeralRunner.Name,
	}
	jitConfig, err := actionsClient.GenerateJitRunnerConfig(ctx, jitSettings, ephemeralRunner.Spec.RunnerScaleSetId)
	if err != nil {
		actionsError := &actions.ActionsError{}
		if !errors.As(err, &actionsError) {
			return ctrl.Result{}, fmt.Errorf("failed to generate JIT config with generic error: %v", err)
		}

		if actionsError.StatusCode != http.StatusConflict ||
			!strings.Contains(actionsError.ExceptionName, "AgentExistsException") {
			return ctrl.Result{}, fmt.Errorf("failed to generate JIT config with Actions service error: %v", err)
		}

		// If the runner with the name we want already exists it means:
		// - We might have a name collision.
		// - Our previous reconciliation loop failed to update the
		//   status with the runnerId and runnerJITConfig after the `GenerateJitRunnerConfig`
		//   created the runner registration on the service.
		// We will try to get the runner and see if it's belong to this AutoScalingRunnerSet,
		// if so, we can simply delete the runner registration and create a new one.
		log.Info("Getting runner jit config failed with conflict error, trying to get the runner by name", "runnerName", ephemeralRunner.Name)
		existingRunner, err := actionsClient.GetRunnerByName(ctx, ephemeralRunner.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get runner by name: %v", err)
		}

		if existingRunner == nil {
			log.Info("Runner with the same name does not exist, re-queuing the reconciliation")
			return ctrl.Result{Requeue: true}, nil
		}

		log.Info("Found the runner with the same name", "runnerId", existingRunner.Id, "runnerScaleSetId", existingRunner.RunnerScaleSetId)
		if existingRunner.RunnerScaleSetId == ephemeralRunner.Spec.RunnerScaleSetId {
			log.Info("Removing the runner with the same name")
			err := actionsClient.RemoveRunner(ctx, int64(existingRunner.Id))
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove runner from the service: %v", err)
			}

			log.Info("Removed the runner with the same name, re-queuing the reconciliation")
			return ctrl.Result{Requeue: true}, nil
		}

		// TODO: Do we want to mark the ephemeral runner as failed, and let EphemeralRunnerSet to clean it up, so we can recover from this situation?
		// The situation is that the EphemeralRunner's name is already used by something else to register a runner, and we can't take the control back.
		return ctrl.Result{}, fmt.Errorf("runner with the same name but doesn't belong to this RunnerScaleSet: %v", err)
	}
	log.Info("Created ephemeral runner JIT config", "runnerId", jitConfig.Runner.Id)

	log.Info("Updating ephemeral runner status with runnerId and runnerJITConfig")
	err = patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		obj.Status.RunnerId = jitConfig.Runner.Id
		obj.Status.RunnerName = jitConfig.Runner.Name
		obj.Status.RunnerJITConfig = jitConfig.EncodedJITConfig
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update runner status for RunnerId/RunnerName/RunnerJITConfig: %v", err)
	}

	log.Info("Updated ephemeral runner status with runnerId and runnerJITConfig")
	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerReconciler) createPod(ctx context.Context, runner *v1alpha1.EphemeralRunner, secret *corev1.Secret, log logr.Logger) (ctrl.Result, error) {
	var envs []corev1.EnvVar
	if runner.Spec.ProxySecretRef != "" {
		http := corev1.EnvVar{
			Name: "http_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: runner.Spec.ProxySecretRef,
					},
					Key: "http_proxy",
				},
			},
		}
		if runner.Spec.Proxy.HTTP != nil {
			envs = append(envs, http)
		}

		https := corev1.EnvVar{
			Name: "https_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: runner.Spec.ProxySecretRef,
					},
					Key: "https_proxy",
				},
			},
		}
		if runner.Spec.Proxy.HTTPS != nil {
			envs = append(envs, https)
		}

		noProxy := corev1.EnvVar{
			Name: "no_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: runner.Spec.ProxySecretRef,
					},
					Key: "no_proxy",
				},
			},
		}
		if len(runner.Spec.Proxy.NoProxy) > 0 {
			envs = append(envs, noProxy)
		}
	}

	log.Info("Creating new pod for ephemeral runner")
	newPod := r.resourceBuilder.newEphemeralRunnerPod(ctx, runner, secret, envs...)

	if err := ctrl.SetControllerReference(runner, newPod, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference to a new pod")
		return ctrl.Result{}, err
	}

	log.Info("Created new pod spec for ephemeral runner")
	if err := r.Create(ctx, newPod); err != nil {
		log.Error(err, "Failed to create pod resource for ephemeral runner.")
		return ctrl.Result{}, err
	}

	log.Info("Created ephemeral runner pod",
		"runnerScaleSetId", runner.Spec.RunnerScaleSetId,
		"runnerName", runner.Status.RunnerName,
		"runnerId", runner.Status.RunnerId,
		"configUrl", runner.Spec.GitHubConfigUrl,
		"podName", newPod.Name)

	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerReconciler) createSecret(ctx context.Context, runner *v1alpha1.EphemeralRunner, log logr.Logger) (ctrl.Result, error) {
	log.Info("Creating new secret for ephemeral runner")
	jitSecret := r.resourceBuilder.newEphemeralRunnerJitSecret(runner)

	if err := ctrl.SetControllerReference(runner, jitSecret, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set controller reference: %v", err)
	}

	log.Info("Created new secret spec for ephemeral runner")
	if err := r.Create(ctx, jitSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create jit secret: %v", err)
	}

	log.Info("Created ephemeral runner secret", "secretName", jitSecret.Name)
	return ctrl.Result{Requeue: true}, nil
}

// updateRunStatusFromPod is responsible for updating non-exiting statuses.
// It should never update phase to Failed or Succeeded
//
// The event should not be re-queued since the termination status should be set
// before proceeding with reconciliation logic
func (r *EphemeralRunnerReconciler) updateRunStatusFromPod(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, pod *corev1.Pod, log logr.Logger) error {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return nil
	}
	if ephemeralRunner.Status.Phase == pod.Status.Phase {
		return nil
	}

	log.Info("Updating ephemeral runner status with pod phase", "statusPhase", pod.Status.Phase, "statusReason", pod.Status.Reason, "statusMessage", pod.Status.Message)
	err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		obj.Status.Phase = pod.Status.Phase
		obj.Status.Ready = obj.Status.Ready || (pod.Status.Phase == corev1.PodRunning)
		obj.Status.Reason = pod.Status.Reason
		obj.Status.Message = pod.Status.Message
	})
	if err != nil {
		return fmt.Errorf("failed to update runner status for Phase/Reason/Message: %v", err)
	}

	log.Info("Updated ephemeral runner status with pod phase")
	return nil
}

func (r *EphemeralRunnerReconciler) actionsClientFor(ctx context.Context, runner *v1alpha1.EphemeralRunner) (actions.ActionsService, error) {
	secret := new(corev1.Secret)
	if err := r.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: runner.Spec.GitHubConfigSecret}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	opts, err := r.actionsClientOptionsFor(ctx, runner)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions client options: %w", err)
	}

	return r.ActionsClient.GetClientFromSecret(
		ctx,
		runner.Spec.GitHubConfigUrl,
		runner.Namespace,
		secret.Data,
		opts...,
	)
}

func (r *EphemeralRunnerReconciler) actionsClientOptionsFor(ctx context.Context, runner *v1alpha1.EphemeralRunner) ([]actions.ClientOption, error) {
	var opts []actions.ClientOption
	if runner.Spec.Proxy != nil {
		proxyFunc, err := runner.Spec.Proxy.ProxyFunc(func(s string) (*corev1.Secret, error) {
			var secret corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: s}, &secret)
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

	tlsConfig := runner.Spec.GitHubServerTLS
	if tlsConfig != nil {
		pool, err := tlsConfig.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := r.Get(
				ctx,
				types.NamespacedName{
					Namespace: runner.Namespace,
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

		opts = append(opts, actions.WithRootCAs(pool))
	}

	return opts, nil
}

// runnerRegisteredWithService checks if the runner is still registered with the service
// Returns found=false and err=nil if ephemeral runner does not exist in GitHub service and should be deleted
func (r EphemeralRunnerReconciler) runnerRegisteredWithService(ctx context.Context, runner *v1alpha1.EphemeralRunner, log logr.Logger) (found bool, err error) {
	actionsClient, err := r.actionsClientFor(ctx, runner)
	if err != nil {
		return false, fmt.Errorf("failed to get Actions client for ScaleSet: %w", err)
	}

	log.Info("Checking if runner exists in GitHub service", "runnerId", runner.Status.RunnerId)
	_, err = actionsClient.GetRunner(ctx, int64(runner.Status.RunnerId))
	if err != nil {
		actionsError := &actions.ActionsError{}
		if !errors.As(err, &actionsError) {
			return false, err
		}

		if actionsError.StatusCode != http.StatusNotFound ||
			!strings.Contains(actionsError.ExceptionName, "AgentNotFoundException") {
			return false, fmt.Errorf("failed to check if runner exists in GitHub service: %v", err)
		}

		log.Info("Runner does not exist in GitHub service", "runnerId", runner.Status.RunnerId)
		return false, nil
	}

	log.Info("Runner exists in GitHub service", "runnerId", runner.Status.RunnerId)
	return true, nil
}

func (r *EphemeralRunnerReconciler) deleteRunnerFromService(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	client, err := r.actionsClientFor(ctx, ephemeralRunner)
	if err != nil {
		return fmt.Errorf("failed to get actions client for runner: %v", err)
	}

	log.Info("Removing runner from the service", "runnerId", ephemeralRunner.Status.RunnerId)
	err = client.RemoveRunner(ctx, int64(ephemeralRunner.Status.RunnerId))
	if err != nil {
		return fmt.Errorf("failed to remove runner from the service: %w", err)
	}

	log.Info("Removed runner from the service", "runnerId", ephemeralRunner.Status.RunnerId)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralRunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO(nikola-jokic): Add indexing and filtering fields on corev1.Pod{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.EphemeralRunner{}).
		Owns(&corev1.Pod{}).
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Named("ephemeral-runner-controller").
		Complete(r)
}

func runnerContainerStatus(pod *corev1.Pod) *corev1.ContainerStatus {
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == EphemeralRunnerContainerName {
			return cs
		}
	}
	return nil
}
