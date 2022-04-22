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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runnerpod", req.NamespacedName)

	var runnerPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &runnerPod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	_, isRunnerPod := runnerPod.Labels[LabelKeyRunnerSetName]
	if !isRunnerPod {
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

	for _, e := range envvars {
		switch e.Name {
		case EnvVarEnterprise:
			enterprise = e.Value
		case EnvVarOrg:
			org = e.Value
		case EnvVarRepo:
			repo = e.Value
		}
	}

	ghc, err := r.GitHubClient.Init(ctx, runnerPod)
	if err != nil {
		return ctrl.Result{}, err
	}

	if runnerPod.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runnerPod.ObjectMeta.Finalizers, runnerPodFinalizerName)

		if added {
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

			// We commit the removal of the finalizer so that Kuberenetes notices it and delete the pod resource from the cluster.
			if err := r.Patch(ctx, patchedPod, client.MergeFrom(&runnerPod)); err != nil {
				log.Error(err, "Failed to update runner for finalizer removal")
				return ctrl.Result{}, err
			}

			log.V(2).Info("Removed finalizer")

			if err := r.GitHubClient.Deinit(ctx, updatedPod); err != nil {
				return ctrl.Result{}, err
			}

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
