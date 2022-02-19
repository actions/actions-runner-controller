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
	gogithub "github.com/google/go-github/v39/github"
	"k8s.io/apimachinery/pkg/util/wait"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	"github.com/actions-runner-controller/actions-runner-controller/github"
)

// RunnerPodReconciler reconciles a Runner object
type RunnerPodReconciler struct {
	client.Client
	Log                         logr.Logger
	Recorder                    record.EventRecorder
	Scheme                      *runtime.Scheme
	GitHubClient                *github.Client
	Name                        string
	RegistrationRecheckInterval time.Duration
	RegistrationRecheckJitter   time.Duration
}

const (
	// This names requires at least one slash to work.
	// See https://github.com/google/knative-gcp/issues/378
	runnerPodFinalizerName = "actions.summerwind.dev/runner-pod"

	AnnotationKeyLastRegistrationCheckTime = "actions-runner-controller/last-registration-check-time"
)

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

	var enterprise, org, repo string

	envvars := runnerPod.Spec.Containers[0].Env
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

	if runnerPod.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runnerPod.ObjectMeta.Finalizers, runnerPodFinalizerName)

		if added {
			newRunner := runnerPod.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Patch(ctx, newRunner, client.MergeFrom(&runnerPod)); err != nil {
				log.Error(err, "Failed to update runner")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	} else {
		finalizers, removed := removeFinalizer(runnerPod.ObjectMeta.Finalizers, runnerPodFinalizerName)

		if removed {
			updatedPod, res, err := tickRunnerGracefulStop(ctx, log, r.GitHubClient, r.Client, enterprise, org, repo, runnerPod.Name, &runnerPod)
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

			log.Info("Removed runner from GitHub", "repository", repo, "organization", org)
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

	// If pod has ended up succeeded we need to restart it
	// Happens e.g. when dind is in runner and run completes
	stopped := runnerPod.Status.Phase == corev1.PodSucceeded

	if !stopped {
		if runnerPod.Status.Phase == corev1.PodRunning {
			for _, status := range runnerPod.Status.ContainerStatuses {
				if status.Name != containerName {
					continue
				}

				if status.State.Terminated != nil && status.State.Terminated.ExitCode == 0 {
					stopped = true
				}
			}
		}
	}

	restart := stopped

	var registrationRecheckDelay time.Duration

	// all checks done below only decide whether a restart is needed
	// if a restart was already decided before, there is no need for the checks
	// saving API calls and scary log messages
	if !restart {
		registrationCheckInterval := time.Minute
		if r.RegistrationRecheckInterval > 0 {
			registrationCheckInterval = r.RegistrationRecheckInterval
		}

		lastCheckTimeStr := runnerPod.Annotations[AnnotationKeyLastRegistrationCheckTime]

		var lastCheckTime *time.Time

		if lastCheckTimeStr != "" {
			t, err := time.Parse(time.RFC3339, lastCheckTimeStr)
			if err != nil {
				log.Error(err, "failed to parase last check time %q", lastCheckTimeStr)
				return ctrl.Result{}, nil
			}

			lastCheckTime = &t
		}

		// We want to call ListRunners GitHub Actions API only once per runner per minute.
		// This if block, in conjunction with:
		//   return ctrl.Result{RequeueAfter: registrationRecheckDelay}, nil
		// achieves that.
		if lastCheckTime != nil {
			nextCheckTime := lastCheckTime.Add(registrationCheckInterval)
			now := time.Now()

			// Requeue scheduled by RequeueAfter can happen a bit earlier (like dozens of milliseconds)
			// so to avoid excessive, in-effective retry, we heuristically ignore the remaining delay in case it is
			// shorter than 1s
			requeueAfter := nextCheckTime.Sub(now) - time.Second
			if requeueAfter > 0 {
				log.Info(
					fmt.Sprintf("Skipped registration check because it's deferred until %s. Retrying in %s at latest", nextCheckTime, requeueAfter),
					"lastRegistrationCheckTime", lastCheckTime,
					"registrationCheckInterval", registrationCheckInterval,
				)

				// Without RequeueAfter, the controller may not retry on scheduled. Instead, it must wait until the
				// next sync period passes, which can be too much later than nextCheckTime.
				//
				// We need to requeue on this reconcilation even though we have already scheduled the initial
				// requeue previously with `return ctrl.Result{RequeueAfter: registrationRecheckDelay}, nil`.
				// Apparently, the workqueue used by controller-runtime seems to deduplicate and resets the delay on
				// other requeues- so the initial scheduled requeue may have been reset due to requeue on
				// spec/status change.
				return ctrl.Result{RequeueAfter: requeueAfter}, nil
			}
		}

		notFound := false
		offline := false

		_, err := r.GitHubClient.IsRunnerBusy(ctx, enterprise, org, repo, runnerPod.Name)

		currentTime := time.Now()

		if err != nil {
			var notFoundException *github.RunnerNotFound
			var offlineException *github.RunnerOffline
			if errors.As(err, &notFoundException) {
				notFound = true
			} else if errors.As(err, &offlineException) {
				offline = true
			} else {
				var e *gogithub.RateLimitError
				if errors.As(err, &e) {
					// We log the underlying error when we failed calling GitHub API to list or unregisters,
					// or the runner is still busy.
					log.Error(
						err,
						fmt.Sprintf(
							"Failed to check if runner is busy due to Github API rate limit. Retrying in %s to avoid excessive GitHub API calls",
							retryDelayOnGitHubAPIRateLimitError,
						),
					)

					return ctrl.Result{RequeueAfter: retryDelayOnGitHubAPIRateLimitError}, err
				}

				return ctrl.Result{}, err
			}
		}

		registrationTimeout := 10 * time.Minute
		durationAfterRegistrationTimeout := currentTime.Sub(runnerPod.CreationTimestamp.Add(registrationTimeout))
		registrationDidTimeout := durationAfterRegistrationTimeout > 0

		if notFound {
			if registrationDidTimeout {
				log.Info(
					"Runner failed to register itself to GitHub in timely manner. "+
						"Recreating the pod to see if it resolves the issue. "+
						"CAUTION: If you see this a lot, you should investigate the root cause. "+
						"See https://github.com/actions-runner-controller/actions-runner-controller/issues/288",
					"podCreationTimestamp", runnerPod.CreationTimestamp,
					"currentTime", currentTime,
					"configuredRegistrationTimeout", registrationTimeout,
				)

				restart = true
			} else {
				log.V(1).Info(
					"Runner pod exists but we failed to check if runner is busy. Apparently it still needs more time.",
					"runnerName", runnerPod.Name,
				)
			}
		} else if offline {
			if registrationDidTimeout {
				log.Info(
					"Already existing GitHub runner still appears offline . "+
						"Recreating the pod to see if it resolves the issue. "+
						"CAUTION: If you see this a lot, you should investigate the root cause. ",
					"podCreationTimestamp", runnerPod.CreationTimestamp,
					"currentTime", currentTime,
					"configuredRegistrationTimeout", registrationTimeout,
				)

				restart = true
			} else {
				log.V(1).Info(
					"Runner pod exists but the GitHub runner appears to be still offline. Waiting for runner to get online ...",
					"runnerName", runnerPod.Name,
				)
			}
		}

		if (notFound || offline) && !registrationDidTimeout {
			registrationRecheckJitter := 10 * time.Second
			if r.RegistrationRecheckJitter > 0 {
				registrationRecheckJitter = r.RegistrationRecheckJitter
			}

			registrationRecheckDelay = registrationCheckInterval + wait.Jitter(registrationRecheckJitter, 0.1)
		}
	}

	// Don't do anything if there's no need to restart the runner
	if !restart {
		// This guard enables us to update runner.Status.Phase to `Running` only after
		// the runner is registered to GitHub.
		if registrationRecheckDelay > 0 {
			log.V(1).Info(fmt.Sprintf("Rechecking the runner registration in %s", registrationRecheckDelay))

			updated := runnerPod.DeepCopy()
			t := time.Now().Format(time.RFC3339)
			updated.Annotations[AnnotationKeyLastRegistrationCheckTime] = t

			if err := r.Patch(ctx, updated, client.MergeFrom(&runnerPod)); err != nil {
				log.Error(err, "Failed to update runner pod annotation for LastRegistrationCheckTime")
				return ctrl.Result{}, err
			}

			return ctrl.Result{RequeueAfter: registrationRecheckDelay}, nil
		}

		// Seeing this message, you can expect the runner to become `Running` soon.
		log.Info(
			"Runner appears to have registered and running.",
			"podCreationTimestamp", runnerPod.CreationTimestamp,
		)

		return ctrl.Result{}, nil
	}

	updated, res, err := tickRunnerGracefulStop(ctx, log, r.GitHubClient, r.Client, enterprise, org, repo, runnerPod.Name, &runnerPod)
	if res != nil {
		return *res, err
	}

	// Delete current pod if recreation is needed
	if err := r.Delete(ctx, updated); err != nil {
		log.Error(err, "Failed to delete pod resource")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(&runnerPod, corev1.EventTypeNormal, "PodDeleted", fmt.Sprintf("Deleted pod '%s'", runnerPod.Name))
	log.Info("Deleted runner pod", "name", runnerPod.Name)

	return ctrl.Result{}, nil
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
