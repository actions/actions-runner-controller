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

package actionssummerwindnet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	arcv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

// RunnerPodReconciler reconciles a Runner object
type RunnerPodReconciler struct {
	client.Client
	Log                         logr.Logger
	Recorder                    record.EventRecorder
	Scheme                      *runtime.Scheme
	GitHubClient                *MultiGitHubClient
	Name                        string
	RegistrationRecheckInterval time.Duration
	RegistrationRecheckJitter   time.Duration

	UnregistrationRetryDelay time.Duration
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runnerpod", req.NamespacedName)

	var runnerPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &runnerPod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	_, isRunnerPod := runnerPod.Labels[LabelKeyRunner]
	_, isRunnerSetPod := runnerPod.Labels[LabelKeyRunnerSetName]
	_, isRunnerDeploymentPod := runnerPod.Labels[LabelKeyRunnerDeploymentName]

	if !isRunnerPod && !isRunnerSetPod && !isRunnerDeploymentPod {
		return ctrl.Result{}, nil
	}

	var envvars []corev1.EnvVar
	for _, container := range runnerPod.Spec.Containers {
		if container.Name == "runner" {
			envvars = container.Env
		}
	}

	if len(envvars) == 0 {
		return ctrl.Result{}, errors.New("Could not determine env vars for runner Pod")
	}

	var enterprise, org, repo string
	var isContainerMode bool

	for _, e := range envvars {
		switch e.Name {
		case EnvVarEnterprise:
			enterprise = e.Value
		case EnvVarOrg:
			org = e.Value
		case EnvVarRepo:
			repo = e.Value
		case "ACTIONS_RUNNER_CONTAINER_HOOKS":
			isContainerMode = true
		}
	}

	ghc, err := r.GitHubClient.InitForRunnerPod(ctx, &runnerPod)
	if err != nil {
		return ctrl.Result{}, err
	}

	if runnerPod.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runnerPod.ObjectMeta.Finalizers, runnerPodFinalizerName)

		var cleanupFinalizersAdded bool
		if isContainerMode {
			finalizers, cleanupFinalizersAdded = addFinalizer(finalizers, runnerLinkedResourcesFinalizerName)
		}

		if added || cleanupFinalizersAdded {
			newRunner := runnerPod.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Patch(ctx, newRunner, client.MergeFrom(&runnerPod)); err != nil {
				log.Error(err, "Failed to update runner")
				return ctrl.Result{}, err
			}

			log.V(2).Info("Added finalizer")

			return ctrl.Result{}, nil
		}
	} else {
		log.V(2).Info("Seen deletion-timestamp is already set")

		// Mark the parent Runner resource for deletion before deleting this runner pod from the cluster.
		// Otherwise the runner controller can recreate the runner pod thinking it has not created any runner pod yet.
		var (
			key    = types.NamespacedName{Namespace: runnerPod.Namespace, Name: runnerPod.Name}
			runner arcv1alpha1.Runner
		)
		if err := r.Get(ctx, key, &runner); err == nil {
			if runner.Name != "" && runner.DeletionTimestamp == nil {
				log.Info("This runner pod seems to have been deleted directly, bypassing the parent Runner resource. Marking the runner for deletion to not let it recreate this pod.")
				if err := r.Delete(ctx, &runner); err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		if finalizers, removed := removeFinalizer(runnerPod.ObjectMeta.Finalizers, runnerLinkedResourcesFinalizerName); removed {
			if err := r.cleanupRunnerLinkedPods(ctx, &runnerPod, log); err != nil {
				log.Info("Runner-linked pods clean up that has failed due to an error. If this persists, please manually remove the runner-linked pods to unblock ARC", "err", err.Error())
				return ctrl.Result{Requeue: true, RequeueAfter: 30 * time.Second}, nil
			}
			if err := r.cleanupRunnerLinkedSecrets(ctx, &runnerPod, log); err != nil {
				log.Info("Runner-linked secrets clean up that has failed due to an error. If this persists, please manually remove the runner-linked secrets to unblock ARC", "err", err.Error())
				return ctrl.Result{Requeue: true, RequeueAfter: 30 * time.Second}, nil
			}
			patchedPod := runnerPod.DeepCopy()
			patchedPod.ObjectMeta.Finalizers = finalizers

			if err := r.Patch(ctx, patchedPod, client.MergeFrom(&runnerPod)); err != nil {
				log.Error(err, "Failed to update runner for finalizer linked resources removal")
				return ctrl.Result{}, err
			}

			// Otherwise the subsequent patch request can revive the removed finalizer and it will trigger a unnecessary reconciliation
			runnerPod = *patchedPod
		}

		finalizers, removed := removeFinalizer(runnerPod.ObjectMeta.Finalizers, runnerPodFinalizerName)

		if removed {
			// In a standard scenario, the upstream controller, like runnerset-controller, ensures this runner to be gracefully stopped before the deletion timestamp is set.
			// But for the case that the user manually deleted it for whatever reason,
			// we have to ensure it to gracefully stop now.
			updatedPod, res, err := tickRunnerGracefulStop(ctx, r.unregistrationRetryDelay(), log, ghc, r.Client, enterprise, org, repo, runnerPod.Name, &runnerPod)
			if res != nil {
				return *res, err
			}

			patchedPod := updatedPod.DeepCopy()
			patchedPod.ObjectMeta.Finalizers = finalizers

			// We commit the removal of the finalizer so that Kubernetes notices it and delete the pod resource from the cluster.
			if err := r.Patch(ctx, patchedPod, client.MergeFrom(&runnerPod)); err != nil {
				log.Error(err, "Failed to update runner for finalizer removal")
				return ctrl.Result{}, err
			}

			log.V(2).Info("Removed finalizer")

			r.GitHubClient.DeinitForRunnerPod(updatedPod)

			return ctrl.Result{}, nil
		}

		deletionTimeout := 1 * time.Minute
		currentTime := time.Now()
		deletionDidTimeout := currentTime.Sub(runnerPod.DeletionTimestamp.Add(deletionTimeout)) > 0

		if deletionDidTimeout {
			log.Info(
				fmt.Sprintf("Failed to delete pod within %s. ", deletionTimeout)+
					"This is typically the case when a Kubernetes node became unreachable "+
					"and the kube controller started evicting nodes. Forcefully deleting the pod to not get stuck.",
				"podDeletionTimestamp", runnerPod.DeletionTimestamp,
				"currentTime", currentTime,
				"configuredDeletionTimeout", deletionTimeout,
			)

			var force int64 = 0
			// forcefully delete runner as we would otherwise get stuck if the node stays unreachable
			if err := r.Delete(ctx, &runnerPod, &client.DeleteOptions{GracePeriodSeconds: &force}); err != nil {
				// probably
				if !kerrors.IsNotFound(err) {
					log.Error(err, "Failed to forcefully delete pod resource ...")
					return ctrl.Result{}, err
				}
				// forceful deletion finally succeeded
				return ctrl.Result{Requeue: true}, nil
			}

			r.Recorder.Event(&runnerPod, corev1.EventTypeNormal, "PodDeleted", fmt.Sprintf("Forcefully deleted pod '%s'", runnerPod.Name))
			log.Info("Forcefully deleted runner pod", "repository", repo)
			// give kube manager a little time to forcefully delete the stuck pod
			return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
		}

		return ctrl.Result{}, nil
	}

	po, res, err := ensureRunnerPodRegistered(ctx, log, ghc, r.Client, enterprise, org, repo, runnerPod.Name, &runnerPod)
	if res != nil {
		return *res, err
	}

	runnerPod = *po

	if _, unregistrationRequested := getAnnotation(&runnerPod, AnnotationKeyUnregistrationRequestTimestamp); unregistrationRequested {
		log.V(2).Info("Progressing unregistration because unregistration-request timestamp is set")

		// At this point we're sure that DeletionTimestamp is not set yet, but the unregistration process is triggered by an upstream controller like runnerset-controller.
		//
		// In a standard scenario, ARC starts the unregistration process before marking the pod for deletion at all,
		// so that it isn't subject to terminationGracePeriod and can safely take hours to finish it's work.
		_, res, err := tickRunnerGracefulStop(ctx, r.unregistrationRetryDelay(), log, ghc, r.Client, enterprise, org, repo, runnerPod.Name, &runnerPod)
		if res != nil {
			return *res, err
		}

		// At this point we are sure that the runner has successfully unregistered, hence is safe to be deleted.
		// But we don't delete the pod here. Instead, let the upstream controller/parent object to delete this pod as
		// a part of a cascade deletion.
		// This is to avoid a parent object, like statefulset, to recreate the deleted pod.
		// If the pod was recreated, it will start a registration process and that may race with the statefulset deleting the pod.
		log.V(2).Info("Unregistration seems complete")

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *RunnerPodReconciler) unregistrationRetryDelay() time.Duration {
	retryDelay := DefaultUnregistrationRetryDelay

	if r.UnregistrationRetryDelay > 0 {
		retryDelay = r.UnregistrationRetryDelay
	}
	return retryDelay
}

func (r *RunnerPodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "runnerpod-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named(name).
		Complete(r)
}

func (r *RunnerPodReconciler) cleanupRunnerLinkedPods(ctx context.Context, pod *corev1.Pod, log logr.Logger) error {
	var runnerLinkedPodList corev1.PodList
	if err := r.List(ctx, &runnerLinkedPodList, client.InNamespace(pod.Namespace), client.MatchingLabels(
		map[string]string{
			"runner-pod": pod.ObjectMeta.Name,
		},
	)); err != nil {
		return fmt.Errorf("failed to list runner-linked pods: %w", err)
	}

	var (
		wg   sync.WaitGroup
		errs []error
	)
	for _, p := range runnerLinkedPodList.Items {
		if !p.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}

		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Delete(ctx, &p); err != nil {
				if kerrors.IsNotFound(err) || kerrors.IsGone(err) {
					return
				}
				errs = append(errs, fmt.Errorf("delete pod %q error: %v", p.ObjectMeta.Name, err))
			}
		}()
	}

	wg.Wait()

	if len(errs) > 0 {
		for _, err := range errs {
			log.Error(err, "failed to remove runner-linked pod")
		}
		return errors.New("failed to remove some runner linked pods")
	}

	return nil
}

func (r *RunnerPodReconciler) cleanupRunnerLinkedSecrets(ctx context.Context, pod *corev1.Pod, log logr.Logger) error {
	log.V(2).Info("Listing runner-linked secrets to be deleted", "ns", pod.Namespace)

	var runnerLinkedSecretList corev1.SecretList
	if err := r.List(ctx, &runnerLinkedSecretList, client.InNamespace(pod.Namespace), client.MatchingLabels(
		map[string]string{
			"runner-pod": pod.ObjectMeta.Name,
		},
	)); err != nil {
		return fmt.Errorf("failed to list runner-linked secrets: %w", err)
	}

	var (
		wg   sync.WaitGroup
		errs []error
	)
	for _, s := range runnerLinkedSecretList.Items {
		if !s.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}

		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Delete(ctx, &s); err != nil {
				if kerrors.IsNotFound(err) || kerrors.IsGone(err) {
					return
				}
				errs = append(errs, fmt.Errorf("delete secret %q error: %v", s.ObjectMeta.Name, err))
			}
		}()
	}

	wg.Wait()

	if len(errs) > 0 {
		for _, err := range errs {
			log.Error(err, "failed to remove runner-linked secret")
		}
		return errors.New("failed to remove some runner linked secrets")
	}

	return nil
}
