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
	"strconv"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	ephemeralRunnerFinalizerName        = "ephemeralrunner.actions.github.com/finalizer"
	ephemeralRunnerActionsFinalizerName = "ephemeralrunner.actions.github.com/runner-registration-finalizer"
)

// EphemeralRunnerReconciler reconciles a EphemeralRunner object
type EphemeralRunnerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	ResourceBuilder
}

// precompute backoff durations for failed ephemeral runners
// the len(failedRunnerBackoff) must be equal to maxFailures + 1
var failedRunnerBackoff = []time.Duration{
	0,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	40 * time.Second,
	80 * time.Second,
}

const maxFailures = 5

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

	if !ephemeralRunner.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(ephemeralRunner, ephemeralRunnerFinalizerName) {
			return ctrl.Result{}, nil
		}

		if controllerutil.ContainsFinalizer(ephemeralRunner, ephemeralRunnerActionsFinalizerName) {
			log.Info("Trying to clean up runner from the service")
			ok, err := r.cleanupRunnerFromService(ctx, ephemeralRunner, log)
			if err != nil {
				log.Error(err, "Failed to clean up runner from service")
				return ctrl.Result{}, err
			}
			if !ok {
				log.Info("Runner is not finished yet, retrying in 30s")
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}

			log.Info("Runner is cleaned up from the service, removing finalizer")
			if err := patch(ctx, r.Client, ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
				controllerutil.RemoveFinalizer(obj, ephemeralRunnerActionsFinalizerName)
			}); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Removed finalizer from ephemeral runner")
		}

		log.Info("Finalizing ephemeral runner")
		err := r.cleanupResources(ctx, ephemeralRunner, log)
		if err != nil {
			log.Error(err, "Failed to clean up ephemeral runner owned resources")
			return ctrl.Result{}, err
		}

		if ephemeralRunner.HasContainerHookConfigured() {
			log.Info("Runner has container hook configured, cleaning up container hook resources")
			err = r.cleanupContainerHooksResources(ctx, ephemeralRunner, log)
			if err != nil {
				log.Error(err, "Failed to clean up container hooks resources")
				return ctrl.Result{}, err
			}
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
		err := r.cleanupResources(ctx, ephemeralRunner, log)
		if err != nil {
			log.Error(err, "Failed to clean up ephemeral runner owned resources")
			return ctrl.Result{}, err
		}

		// Stop reconciling on this object.
		// The EphemeralRunnerSet is responsible for cleaning it up.
		log.Info("EphemeralRunner has already finished. Stopping reconciliation and waiting for EphemeralRunnerSet to clean it up", "phase", ephemeralRunner.Status.Phase)
		return ctrl.Result{}, nil
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
		return ctrl.Result{}, nil
	}

	secret := new(corev1.Secret)
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to fetch secret")
			return ctrl.Result{}, err
		}

		jitConfig, err := r.createRunnerJitConfig(ctx, ephemeralRunner, log)
		switch {
		case err == nil:
			// create secret if not created
			log.Info("Creating new ephemeral runner secret for jitconfig.")
			jitSecret, err := r.createSecret(ctx, ephemeralRunner, jitConfig, log)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create secret: %w", err)
			}
			log.Info("Created new ephemeral runner secret for jitconfig.")
			secret = jitSecret

		case errors.Is(err, retryableError):
			log.Info("Encountered retryable error, requeueing", "error", err.Error())
			return ctrl.Result{Requeue: true}, nil
		case errors.Is(err, fatalError):
			log.Info("JIT config cannot be created for this ephemeral runner, issuing delete", "error", err.Error())
			if err := r.Delete(ctx, ephemeralRunner); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete the ephemeral runner: %w", err)
			}
			log.Info("Request to delete ephemeral runner has been issued")
			return ctrl.Result{}, nil
		default:
			log.Error(err, "Failed to create ephemeral runners secret", "error", err.Error())
			return ctrl.Result{}, err
		}
	}

	if ephemeralRunner.Status.RunnerId == 0 {
		log.Info("Updating ephemeral runner status with runnerId and runnerName")
		runnerID, err := strconv.Atoi(string(secret.Data["runnerId"]))
		if err != nil {
			log.Error(err, "Runner config secret is corrupted: missing runnerId")
			log.Info("Deleting corrupted runner config secret")
			if err := r.Delete(ctx, secret); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete the corrupted runner config secret")
			}
			log.Info("Corrupted runner config secret has been deleted")
			return ctrl.Result{Requeue: true}, nil
		}

		runnerName := string(secret.Data["runnerName"])
		if err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
			obj.Status.RunnerId = runnerID
			obj.Status.RunnerName = runnerName
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update runner status for RunnerId/RunnerName/RunnerJITConfig: %w", err)
		}
		ephemeralRunner.Status.RunnerId = runnerID
		ephemeralRunner.Status.RunnerName = runnerName
		log.Info("Updated ephemeral runner status with runnerId and runnerName")
	}

	if len(ephemeralRunner.Status.Failures) > maxFailures {
		log.Info(fmt.Sprintf("EphemeralRunner has failed more than %d times. Deleting ephemeral runner so it can be re-created", maxFailures))
		if err := r.Delete(ctx, ephemeralRunner); err != nil {
			log.Error(fmt.Errorf("failed to delete ephemeral runner after %d failures: %w", maxFailures, err), "Failed to delete ephemeral runner")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	now := metav1.Now()
	lastFailure := ephemeralRunner.Status.LastFailure()
	backoffDuration := failedRunnerBackoff[len(ephemeralRunner.Status.Failures)]
	nextReconciliation := lastFailure.Add(backoffDuration)
	if !lastFailure.IsZero() && now.Before(&metav1.Time{Time: nextReconciliation}) {
		requeueAfter := nextReconciliation.Sub(now.Time)
		log.Info("Backing off the next reconciliation due to failure",
			"lastFailure", lastFailure,
			"nextReconciliation", nextReconciliation,
			"requeueAfter", requeueAfter,
		)
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: requeueAfter,
		}, nil
	}

	pod := new(corev1.Pod)
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to fetch the pod")
			return ctrl.Result{}, err
		}
		log.Info("Ephemeral runner pod does not exist. Creating new ephemeral runner")

		result, err := r.createPod(ctx, ephemeralRunner, secret, log)
		switch {
		case err == nil:
			return result, nil
		case kerrors.IsAlreadyExists(err):
			log.Info("Runner pod already exists. Waiting for the pod event to be received")
			return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
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
		if ephemeralRunner.HasJob() {
			log.Error(
				errors.New("ephemeral runner has a job assigned, but the pod has failed"),
				"Ephemeral runner either has faulty entrypoint or something external killing the runner",
			)
			log.Info("Deleting the ephemeral runner that has a job assigned but the pod has failed")
			if err := r.Delete(ctx, ephemeralRunner); err != nil {
				log.Error(err, "Failed to delete the ephemeral runner that has a job assigned but the pod has failed")
				return ctrl.Result{}, err
			}

			log.Info("Deleted the ephemeral runner that has a job assigned but the pod has failed")
			log.Info("Trying to remove the runner from the service")
			actionsClient, err := r.GetActionsService(ctx, ephemeralRunner)
			if err != nil {
				log.Error(err, "Failed to get actions client for removing the runner from the service")
				return ctrl.Result{}, nil
			}
			if err := actionsClient.RemoveRunner(ctx, int64(ephemeralRunner.Status.RunnerId)); err != nil {
				log.Error(err, "Failed to remove the runner from the service")
				return ctrl.Result{}, nil
			}
			log.Info("Removed the runner from the service")
			return ctrl.Result{}, nil
		}
		if err := r.deletePodAsFailed(ctx, ephemeralRunner, pod, log); err != nil {
			log.Error(err, "Failed to delete runner pod on failure")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	default: // succeeded
		log.Info("Ephemeral runner has finished successfully, deleting ephemeral runner", "exitCode", cs.State.Terminated.ExitCode)
		if err := r.Delete(ctx, ephemeralRunner); err != nil {
			log.Error(err, "Failed to delete ephemeral runner after successful completion")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
}

func (r *EphemeralRunnerReconciler) cleanupRunnerFromService(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (ok bool, err error) {
	if err := r.deleteRunnerFromService(ctx, ephemeralRunner, log); err != nil {
		actionsError := &actions.ActionsError{}
		if !errors.As(err, &actionsError) {
			return false, err
		}

		if actionsError.StatusCode == http.StatusBadRequest && actionsError.IsException("JobStillRunningException") {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (r *EphemeralRunnerReconciler) cleanupResources(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	log.Info("Cleaning up the runner pod")
	pod := new(corev1.Pod)
	err := r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunner.Namespace, Name: ephemeralRunner.Name}, pod)
	switch {
	case err == nil:
		if pod.DeletionTimestamp.IsZero() {
			log.Info("Deleting the runner pod")
			if err := r.Delete(ctx, pod); err != nil && !kerrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete pod: %w", err)
			}
			log.Info("Deleted the runner pod")
		} else {
			log.Info("Pod contains deletion timestamp")
		}
	case kerrors.IsNotFound(err):
		log.Info("Runner pod is deleted")
	default:
		return err
	}

	log.Info("Cleaning up the runner jitconfig secret")
	secret := new(corev1.Secret)
	err = r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunner.Namespace, Name: ephemeralRunner.Name}, secret)
	switch {
	case err == nil:
		if secret.DeletionTimestamp.IsZero() {
			log.Info("Deleting the jitconfig secret")
			if err := r.Delete(ctx, secret); err != nil && !kerrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete secret: %w", err)
			}
			log.Info("Deleted jitconfig secret")
		} else {
			log.Info("Secret contains deletion timestamp")
		}
	case kerrors.IsNotFound(err):
		log.Info("Runner jitconfig secret is deleted")
	default:
		return err
	}

	return nil
}

func (r *EphemeralRunnerReconciler) cleanupContainerHooksResources(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	log.Info("Cleaning up runner linked pods")
	var errs []error
	if err := r.cleanupRunnerLinkedPods(ctx, ephemeralRunner, log); err != nil {
		errs = append(errs, err)
	}

	log.Info("Cleaning up runner linked secrets")
	if err := r.cleanupRunnerLinkedSecrets(ctx, ephemeralRunner, log); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (r *EphemeralRunnerReconciler) cleanupRunnerLinkedPods(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	runnerLinedLabels := client.MatchingLabels(
		map[string]string{
			"runner-pod": ephemeralRunner.Name,
		},
	)
	var runnerLinkedPodList corev1.PodList
	if err := r.List(ctx, &runnerLinkedPodList, client.InNamespace(ephemeralRunner.Namespace), runnerLinedLabels); err != nil {
		return fmt.Errorf("failed to list runner-linked pods: %w", err)
	}

	if len(runnerLinkedPodList.Items) == 0 {
		log.Info("Runner-linked pods are deleted")
		return nil
	}

	log.Info("Deleting container hooks runner-linked pods", "count", len(runnerLinkedPodList.Items))

	var errs []error
	for i := range runnerLinkedPodList.Items {
		linkedPod := &runnerLinkedPodList.Items[i]
		if !linkedPod.DeletionTimestamp.IsZero() {
			continue
		}

		log.Info("Deleting container hooks runner-linked pod", "name", linkedPod.Name)
		if err := r.Delete(ctx, linkedPod); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete runner linked pod %q: %w", linkedPod.Name, err))
		}
	}

	return errors.Join(errs...)
}

func (r *EphemeralRunnerReconciler) cleanupRunnerLinkedSecrets(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	runnerLinkedLabels := client.MatchingLabels(
		map[string]string{
			"runner-pod": ephemeralRunner.Name,
		},
	)
	var runnerLinkedSecretList corev1.SecretList
	if err := r.List(ctx, &runnerLinkedSecretList, client.InNamespace(ephemeralRunner.Namespace), runnerLinkedLabels); err != nil {
		return fmt.Errorf("failed to list runner-linked secrets: %w", err)
	}

	if len(runnerLinkedSecretList.Items) == 0 {
		log.Info("Runner-linked secrets are deleted")
		return nil
	}

	log.Info("Deleting container hooks runner-linked secrets", "count", len(runnerLinkedSecretList.Items))

	var errs []error
	for i := range runnerLinkedSecretList.Items {
		s := &runnerLinkedSecretList.Items[i]
		if !s.DeletionTimestamp.IsZero() {
			continue
		}

		log.Info("Deleting container hooks runner-linked secret", "name", s.Name)
		if err := r.Delete(ctx, s); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete runner linked secret %q: %w", s.Name, err))
		}
	}

	return errors.Join(errs...)
}

func (r *EphemeralRunnerReconciler) markAsFailed(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, errMessage string, reason string, log logr.Logger) error {
	log.Info("Updating ephemeral runner status to Failed")
	if err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		obj.Status.Phase = corev1.PodFailed
		obj.Status.Reason = reason
		obj.Status.Message = errMessage
	}); err != nil {
		return fmt.Errorf("failed to update ephemeral runner status Phase/Message: %w", err)
	}

	log.Info("Removing the runner from the service")
	if err := r.deleteRunnerFromService(ctx, ephemeralRunner, log); err != nil {
		return fmt.Errorf("failed to remove the runner from service: %w", err)
	}

	log.Info("EphemeralRunner is marked as Failed and deleted from the service")
	return nil
}

// deletePodAsFailed is responsible for deleting the pod and updating the .Status.Failures for tracking failure count.
// It should not be responsible for setting the status to Failed.
func (r *EphemeralRunnerReconciler) deletePodAsFailed(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, pod *corev1.Pod, log logr.Logger) error {
	if pod.DeletionTimestamp.IsZero() {
		log.Info("Deleting the ephemeral runner pod", "podId", pod.UID)
		if err := r.Delete(ctx, pod); err != nil && !kerrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod with status failed: %w", err)
		}
	}

	log.Info("Updating ephemeral runner status to track the failure count")
	if err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		if obj.Status.Failures == nil {
			obj.Status.Failures = make(map[string]metav1.Time)
		}
		obj.Status.Failures[string(pod.UID)] = metav1.Now()
		obj.Status.Ready = false
		obj.Status.Reason = pod.Status.Reason
		obj.Status.Message = pod.Status.Message
	}); err != nil {
		return fmt.Errorf("failed to update ephemeral runner status: failed attempts: %w", err)
	}

	log.Info("EphemeralRunner pod is deleted and status is updated with failure count")
	return nil
}

func (r *EphemeralRunnerReconciler) createRunnerJitConfig(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) (*actions.RunnerScaleSetJitRunnerConfig, error) {
	// Runner is not registered with the service. We need to register it first
	log.Info("Creating ephemeral runner JIT config")
	actionsClient, err := r.GetActionsService(ctx, ephemeralRunner)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions client for generating JIT config: %w", err)
	}

	jitSettings := &actions.RunnerScaleSetJitRunnerSetting{
		Name: ephemeralRunner.Name,
	}

	for i := range ephemeralRunner.Spec.Spec.Containers {
		if ephemeralRunner.Spec.Spec.Containers[i].Name == v1alpha1.EphemeralRunnerContainerName &&
			ephemeralRunner.Spec.Spec.Containers[i].WorkingDir != "" {
			jitSettings.WorkFolder = ephemeralRunner.Spec.Spec.Containers[i].WorkingDir
		}
	}

	jitConfig, err := actionsClient.GenerateJitRunnerConfig(ctx, jitSettings, ephemeralRunner.Spec.RunnerScaleSetId)
	if err == nil { // if NO error
		log.Info("Created ephemeral runner JIT config", "runnerId", jitConfig.Runner.Id)
		return jitConfig, nil
	}

	actionsError := &actions.ActionsError{}
	if !errors.As(err, &actionsError) {
		return nil, fmt.Errorf("failed to generate JIT config with generic error: %w", err)
	}

	if actionsError.StatusCode != http.StatusConflict ||
		!actionsError.IsException("AgentExistsException") {
		return nil, fmt.Errorf("failed to generate JIT config with Actions service error: %w", err)
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
		return nil, fmt.Errorf("failed to get runner by name: %w", err)
	}

	if existingRunner == nil {
		log.Info("Runner with the same name does not exist anymore, re-queuing the reconciliation")
		return nil, fmt.Errorf("%w: runner existed, retry configuration", retryableError)
	}

	log.Info("Found the runner with the same name", "runnerId", existingRunner.Id, "runnerScaleSetId", existingRunner.RunnerScaleSetId)
	if existingRunner.RunnerScaleSetId == ephemeralRunner.Spec.RunnerScaleSetId {
		log.Info("Removing the runner with the same name")
		err := actionsClient.RemoveRunner(ctx, int64(existingRunner.Id))
		if err != nil {
			return nil, fmt.Errorf("failed to remove runner from the service: %w", err)
		}

		log.Info("Removed the runner with the same name, re-queuing the reconciliation")
		return nil, fmt.Errorf("%w: runner existed belonging to the scale set, retry configuration", retryableError)
	}

	return nil, fmt.Errorf("%w: runner with the same name but doesn't belong to this RunnerScaleSet: %w", fatalError, err)
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
	newPod := r.newEphemeralRunnerPod(ctx, runner, secret, envs...)

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

func (r *EphemeralRunnerReconciler) createSecret(ctx context.Context, runner *v1alpha1.EphemeralRunner, jitConfig *actions.RunnerScaleSetJitRunnerConfig, log logr.Logger) (*corev1.Secret, error) {
	log.Info("Creating new secret for ephemeral runner")
	jitSecret := r.newEphemeralRunnerJitSecret(runner, jitConfig)

	if err := ctrl.SetControllerReference(runner, jitSecret, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	log.Info("Created new secret spec for ephemeral runner")
	if err := r.Create(ctx, jitSecret); err != nil {
		return nil, fmt.Errorf("failed to create jit secret: %w", err)
	}

	log.Info("Created ephemeral runner secret", "secretName", jitSecret.Name)
	return jitSecret, nil
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

	var ready bool
	var lastTransitionTime time.Time
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.LastTransitionTime.After(lastTransitionTime) {
			ready = condition.Status == corev1.ConditionTrue
			lastTransitionTime = condition.LastTransitionTime.Time
		}
	}

	phaseChanged := ephemeralRunner.Status.Phase != pod.Status.Phase
	readyChanged := ready != ephemeralRunner.Status.Ready

	if !phaseChanged && !readyChanged {
		return nil
	}

	log.Info(
		"Updating ephemeral runner status",
		"statusPhase", pod.Status.Phase,
		"statusReason", pod.Status.Reason,
		"statusMessage", pod.Status.Message,
		"ready", ready,
	)
	err := patchSubResource(ctx, r.Status(), ephemeralRunner, func(obj *v1alpha1.EphemeralRunner) {
		obj.Status.Phase = pod.Status.Phase
		obj.Status.Ready = ready
		obj.Status.Reason = pod.Status.Reason
		obj.Status.Message = pod.Status.Message
	})
	if err != nil {
		return fmt.Errorf("failed to update runner status for Phase/Reason/Message/Ready: %w", err)
	}

	log.Info("Updated ephemeral runner status")
	return nil
}

func (r *EphemeralRunnerReconciler) deleteRunnerFromService(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, log logr.Logger) error {
	client, err := r.GetActionsService(ctx, ephemeralRunner)
	if err != nil {
		return fmt.Errorf("failed to get actions client for runner: %w", err)
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
func (r *EphemeralRunnerReconciler) SetupWithManager(mgr ctrl.Manager, opts ...Option) error {
	return builderWithOptions(
		ctrl.NewControllerManagedBy(mgr).
			For(&v1alpha1.EphemeralRunner{}).
			Owns(&corev1.Pod{}).
			WithEventFilter(predicate.ResourceVersionChangedPredicate{}),
		opts,
	).Complete(r)
}

func runnerContainerStatus(pod *corev1.Pod) *corev1.ContainerStatus {
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == v1alpha1.EphemeralRunnerContainerName {
			return cs
		}
	}
	return nil
}
