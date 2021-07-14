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
	"strings"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/hash"
	gogithub "github.com/google/go-github/v37/github"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
)

const (
	containerName = "runner"
	finalizerName = "runner.actions.summerwind.dev"

	LabelKeyPodTemplateHash = "pod-template-hash"

	retryDelayOnGitHubAPIRateLimitError = 30 * time.Second

	// This is an annotation internal to actions-runner-controller and can change in backward-incompatible ways
	annotationKeyRegistrationOnly = "actions-runner-controller/registration-only"

	EnvVarOrg        = "RUNNER_ORG"
	EnvVarRepo       = "RUNNER_REPO"
	EnvVarEnterprise = "RUNNER_ENTERPRISE"
)

// RunnerReconciler reconciles a Runner object
type RunnerReconciler struct {
	client.Client
	Log                         logr.Logger
	Recorder                    record.EventRecorder
	Scheme                      *runtime.Scheme
	GitHubClient                *github.Client
	RunnerImage                 string
	DockerImage                 string
	DockerRegistryMirror        string
	Name                        string
	RegistrationRecheckInterval time.Duration
	RegistrationRecheckJitter   time.Duration
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runner", req.NamespacedName)

	var runner v1alpha1.Runner
	if err := r.Get(ctx, req.NamespacedName, &runner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := runner.Validate()
	if err != nil {
		log.Info("Failed to validate runner spec", "error", err.Error())
		return ctrl.Result{}, nil
	}

	if runner.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runner.ObjectMeta.Finalizers, finalizerName)

		if added {
			newRunner := runner.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Update(ctx, newRunner); err != nil {
				log.Error(err, "Failed to update runner")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	} else {
		finalizers, removed := removeFinalizer(runner.ObjectMeta.Finalizers, finalizerName)

		if removed {
			if len(runner.Status.Registration.Token) > 0 {
				ok, err := r.unregisterRunner(ctx, runner.Spec.Enterprise, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
				if err != nil {
					if errors.Is(err, &gogithub.RateLimitError{}) {
						// We log the underlying error when we failed calling GitHub API to list or unregisters,
						// or the runner is still busy.
						log.Error(
							err,
							fmt.Sprintf(
								"Failed to unregister runner due to GitHub API rate limits. Delaying retry for %s to avoid excessive GitHub API calls",
								retryDelayOnGitHubAPIRateLimitError,
							),
						)

						return ctrl.Result{RequeueAfter: retryDelayOnGitHubAPIRateLimitError}, err
					}

					return ctrl.Result{}, err
				}

				if !ok {
					log.V(1).Info("Runner no longer exists on GitHub")
				}
			} else {
				log.V(1).Info("Runner was never registered on GitHub")
			}

			newRunner := runner.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Patch(ctx, newRunner, client.MergeFrom(&runner)); err != nil {
				log.Error(err, "Failed to update runner for finalizer removal")
				return ctrl.Result{}, err
			}

			log.Info("Removed runner from GitHub", "repository", runner.Spec.Repository, "organization", runner.Spec.Organization)
		}

		return ctrl.Result{}, nil
	}

	registrationOnly := metav1.HasAnnotation(runner.ObjectMeta, annotationKeyRegistrationOnly)
	if registrationOnly && runner.Status.Phase != "" {
		// At this point we are sure that the registration-only runner has successfully configured and
		// is of `offline` status, because we set runner.Status.Phase to that of the runner pod only after
		// successful registration.

		var pod corev1.Pod
		if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("Retrying soon as we failed to get registration-only runner pod: %v", err))

				return ctrl.Result{Requeue: true}, nil
			}
		} else if err := r.Delete(ctx, &pod); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("Retrying soon as we failed to delete registration-only runner pod: %v", err))

				return ctrl.Result{Requeue: true}, nil
			}
		}

		log.Info("Successfully deleted egistration-only runner pod to free node and cluster resource")

		// Return here to not recreate the deleted pod, because recreating it is the waste of cluster and node resource,
		// and also defeats the original purpose of scale-from/to-zero we're trying to implement by using the registration-only runner.
		return ctrl.Result{}, nil
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if updated, err := r.updateRegistrationToken(ctx, runner); err != nil {
			return ctrl.Result{}, err
		} else if updated {
			return ctrl.Result{Requeue: true}, nil
		}

		newPod, err := r.newPod(runner)
		if err != nil {
			log.Error(err, "Could not create pod")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, &newPod); err != nil {
			if kerrors.IsAlreadyExists(err) {
				// Gracefully handle pod-already-exists errors due to informer cache delay.
				// Without this we got a few errors like the below on new runner pod:
				// 2021-03-16T00:23:10.116Z        ERROR   controller-runtime.controller   Reconciler error      {"controller": "runner-controller", "request": "default/example-runnerdeploy-b2g2g-j4mcp", "error": "pods \"example-runnerdeploy-b2g2g-j4mcp\" already exists"}
				log.Info(
					"Failed to create pod due to AlreadyExists error. Probably this pod has been already created in previous reconcilation but is still not in the informer cache. Will retry on pod created. If it doesn't repeat, there's no problem",
				)

				return ctrl.Result{}, nil
			}

			log.Error(err, "Failed to create pod resource")

			return ctrl.Result{}, err
		}

		r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodCreated", fmt.Sprintf("Created pod '%s'", newPod.Name))
		log.Info("Created runner pod", "repository", runner.Spec.Repository)
	} else {
		if !pod.ObjectMeta.DeletionTimestamp.IsZero() {
			deletionTimeout := 1 * time.Minute
			currentTime := time.Now()
			deletionDidTimeout := currentTime.Sub(pod.DeletionTimestamp.Add(deletionTimeout)) > 0

			if deletionDidTimeout {
				log.Info(
					fmt.Sprintf("Failed to delete pod within %s. ", deletionTimeout)+
						"This is typically the case when a Kubernetes node became unreachable "+
						"and the kube controller started evicting nodes. Forcefully deleting the pod to not get stuck.",
					"podDeletionTimestamp", pod.DeletionTimestamp,
					"currentTime", currentTime,
					"configuredDeletionTimeout", deletionTimeout,
				)

				var force int64 = 0
				// forcefully delete runner as we would otherwise get stuck if the node stays unreachable
				if err := r.Delete(ctx, &pod, &client.DeleteOptions{GracePeriodSeconds: &force}); err != nil {
					// probably
					if !kerrors.IsNotFound(err) {
						log.Error(err, "Failed to forcefully delete pod resource ...")
						return ctrl.Result{}, err
					}
					// forceful deletion finally succeeded
					return ctrl.Result{Requeue: true}, nil
				}

				r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodDeleted", fmt.Sprintf("Forcefully deleted pod '%s'", pod.Name))
				log.Info("Forcefully deleted runner pod", "repository", runner.Spec.Repository)
				// give kube manager a little time to forcefully delete the stuck pod
				return ctrl.Result{RequeueAfter: 3 * time.Second}, err
			} else {
				return ctrl.Result{}, err
			}
		}

		// If pod has ended up succeeded we need to restart it
		// Happens e.g. when dind is in runner and run completes
		stopped := pod.Status.Phase == corev1.PodSucceeded

		if !stopped {
			if pod.Status.Phase == corev1.PodRunning {
				for _, status := range pod.Status.ContainerStatuses {
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

		if registrationOnly && stopped {
			restart = false

			log.Info(
				"Observed that registration-only runner for scaling-from-zero has successfully stopped. " +
					"Unlike other pods, this one will be recreated only when runner spec changes.",
			)
		}

		if updated, err := r.updateRegistrationToken(ctx, runner); err != nil {
			return ctrl.Result{}, err
		} else if updated {
			return ctrl.Result{Requeue: true}, nil
		}

		newPod, err := r.newPod(runner)
		if err != nil {
			log.Error(err, "Could not create pod")
			return ctrl.Result{}, err
		}

		if registrationOnly {
			newPod.Spec.Containers[0].Env = append(
				newPod.Spec.Containers[0].Env,
				corev1.EnvVar{
					Name:  "RUNNER_REGISTRATION_ONLY",
					Value: "true",
				},
			)
		}

		var registrationRecheckDelay time.Duration

		// all checks done below only decide whether a restart is needed
		// if a restart was already decided before, there is no need for the checks
		// saving API calls and scary log messages
		if !restart {
			registrationCheckInterval := time.Minute
			if r.RegistrationRecheckInterval > 0 {
				registrationCheckInterval = r.RegistrationRecheckInterval
			}

			// We want to call ListRunners GitHub Actions API only once per runner per minute.
			// This if block, in conjunction with:
			//   return ctrl.Result{RequeueAfter: registrationRecheckDelay}, nil
			// achieves that.
			if lastCheckTime := runner.Status.LastRegistrationCheckTime; lastCheckTime != nil {
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

			runnerBusy, err := r.GitHubClient.IsRunnerBusy(ctx, runner.Spec.Enterprise, runner.Spec.Organization, runner.Spec.Repository, runner.Name)

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

			// See the `newPod` function called above for more information
			// about when this hash changes.
			curHash := pod.Labels[LabelKeyPodTemplateHash]
			newHash := newPod.Labels[LabelKeyPodTemplateHash]

			if !runnerBusy && curHash != newHash {
				restart = true
			}

			registrationTimeout := 10 * time.Minute
			durationAfterRegistrationTimeout := currentTime.Sub(pod.CreationTimestamp.Add(registrationTimeout))
			registrationDidTimeout := durationAfterRegistrationTimeout > 0

			if notFound {
				if registrationDidTimeout {
					log.Info(
						"Runner failed to register itself to GitHub in timely manner. "+
							"Recreating the pod to see if it resolves the issue. "+
							"CAUTION: If you see this a lot, you should investigate the root cause. "+
							"See https://github.com/actions-runner-controller/actions-runner-controller/issues/288",
						"podCreationTimestamp", pod.CreationTimestamp,
						"currentTime", currentTime,
						"configuredRegistrationTimeout", registrationTimeout,
					)

					restart = true
				} else {
					log.V(1).Info(
						"Runner pod exists but we failed to check if runner is busy. Apparently it still needs more time.",
						"runnerName", runner.Name,
					)
				}
			} else if offline {
				if registrationOnly {
					log.Info(
						"Observed that registration-only runner for scaling-from-zero has successfully been registered.",
						"podCreationTimestamp", pod.CreationTimestamp,
						"currentTime", currentTime,
						"configuredRegistrationTimeout", registrationTimeout,
					)
				} else if registrationDidTimeout {
					log.Info(
						"Already existing GitHub runner still appears offline . "+
							"Recreating the pod to see if it resolves the issue. "+
							"CAUTION: If you see this a lot, you should investigate the root cause. ",
						"podCreationTimestamp", pod.CreationTimestamp,
						"currentTime", currentTime,
						"configuredRegistrationTimeout", registrationTimeout,
					)

					restart = true
				} else {
					log.V(1).Info(
						"Runner pod exists but the GitHub runner appears to be still offline. Waiting for runner to get online ...",
						"runnerName", runner.Name,
					)
				}
			}

			if (notFound || (offline && !registrationOnly)) && !registrationDidTimeout {
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

				updated := runner.DeepCopy()
				updated.Status.LastRegistrationCheckTime = &metav1.Time{Time: time.Now()}

				if err := r.Status().Patch(ctx, updated, client.MergeFrom(&runner)); err != nil {
					log.Error(err, "Failed to update runner status for LastRegistrationCheckTime")
					return ctrl.Result{}, err
				}

				return ctrl.Result{RequeueAfter: registrationRecheckDelay}, nil
			}

			if runner.Status.Phase != string(pod.Status.Phase) {
				if pod.Status.Phase == corev1.PodRunning {
					// Seeing this message, you can expect the runner to become `Running` soon.
					log.Info(
						"Runner appears to have registered and running.",
						"podCreationTimestamp", pod.CreationTimestamp,
					)
				}

				updated := runner.DeepCopy()
				updated.Status.Phase = string(pod.Status.Phase)
				updated.Status.Reason = pod.Status.Reason
				updated.Status.Message = pod.Status.Message

				if err := r.Status().Patch(ctx, updated, client.MergeFrom(&runner)); err != nil {
					log.Error(err, "Failed to update runner status for Phase/Reason/Message")
					return ctrl.Result{}, err
				}
			}

			return ctrl.Result{}, nil
		}

		// Delete current pod if recreation is needed
		if err := r.Delete(ctx, &pod); err != nil {
			log.Error(err, "Failed to delete pod resource")
			return ctrl.Result{}, err
		}

		r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodDeleted", fmt.Sprintf("Deleted pod '%s'", newPod.Name))
		log.Info("Deleted runner pod", "repository", runner.Spec.Repository)
	}

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) unregisterRunner(ctx context.Context, enterprise, org, repo, name string) (bool, error) {
	runners, err := r.GitHubClient.ListRunners(ctx, enterprise, org, repo)
	if err != nil {
		return false, err
	}

	id := int64(0)
	for _, runner := range runners {
		if runner.GetName() == name {
			if runner.GetBusy() {
				return false, fmt.Errorf("runner is busy")
			}
			id = runner.GetID()
			break
		}
	}

	if id == int64(0) {
		return false, nil
	}

	if err := r.GitHubClient.RemoveRunner(ctx, enterprise, org, repo, id); err != nil {
		return false, err
	}

	return true, nil
}

func (r *RunnerReconciler) updateRegistrationToken(ctx context.Context, runner v1alpha1.Runner) (bool, error) {
	if runner.IsRegisterable() {
		return false, nil
	}

	log := r.Log.WithValues("runner", runner.Name)

	rt, err := r.GitHubClient.GetRegistrationToken(ctx, runner.Spec.Enterprise, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
	if err != nil {
		r.Recorder.Event(&runner, corev1.EventTypeWarning, "FailedUpdateRegistrationToken", "Updating registration token failed")
		log.Error(err, "Failed to get new registration token")
		return false, err
	}

	updated := runner.DeepCopy()
	updated.Status.Registration = v1alpha1.RunnerStatusRegistration{
		Organization: runner.Spec.Organization,
		Repository:   runner.Spec.Repository,
		Labels:       runner.Spec.Labels,
		Token:        rt.GetToken(),
		ExpiresAt:    metav1.NewTime(rt.GetExpiresAt().Time),
	}

	if err := r.Status().Patch(ctx, updated, client.MergeFrom(&runner)); err != nil {
		log.Error(err, "Failed to update runner status for Registration")
		return false, err
	}

	r.Recorder.Event(&runner, corev1.EventTypeNormal, "RegistrationTokenUpdated", "Successfully update registration token")
	log.Info("Updated registration token", "repository", runner.Spec.Repository)

	return true, nil
}

func (r *RunnerReconciler) newPod(runner v1alpha1.Runner) (corev1.Pod, error) {
	var template corev1.Pod

	labels := map[string]string{}

	for k, v := range runner.ObjectMeta.Labels {
		labels[k] = v
	}

	// This implies that...
	//
	// (1) We recreate the runner pod whenever the runner has changes in:
	// - metadata.labels (excluding "runner-template-hash" added by the parent RunnerReplicaSet
	// - metadata.annotations
	// - metadata.spec (including image, env, organization, repository, group, and so on)
	// - GithubBaseURL setting of the controller (can be configured via GITHUB_ENTERPRISE_URL)
	//
	// (2) We don't recreate the runner pod when there are changes in:
	// - runner.status.registration.token
	//   - This token expires and changes hourly, but you don't need to recreate the pod due to that.
	//     It's the opposite.
	//     An unexpired token is required only when the runner agent is registering itself on launch.
	//
	//     In other words, the registered runner doesn't get invalidated on registration token expiration.
	//     A registered runner's session and the a registration token seem to have two different and independent
	//     lifecycles.
	//
	//     See https://github.com/actions-runner-controller/actions-runner-controller/issues/143 for more context.
	labels[LabelKeyPodTemplateHash] = hash.FNVHashStringObjects(
		filterLabels(runner.ObjectMeta.Labels, LabelKeyRunnerTemplateHash),
		runner.ObjectMeta.Annotations,
		runner.Spec,
		r.GitHubClient.GithubBaseURL,
	)

	objectMeta := metav1.ObjectMeta{
		Name:        runner.ObjectMeta.Name,
		Namespace:   runner.ObjectMeta.Namespace,
		Labels:      labels,
		Annotations: runner.ObjectMeta.Annotations,
	}

	template.ObjectMeta = objectMeta

	if len(runner.Spec.Containers) == 0 {
		template.Spec.Containers = append(template.Spec.Containers, corev1.Container{
			Name:            "runner",
			ImagePullPolicy: runner.Spec.ImagePullPolicy,
			EnvFrom:         runner.Spec.EnvFrom,
			Env:             runner.Spec.Env,
			Resources:       runner.Spec.Resources,
		})

		if runner.Spec.DockerdWithinRunnerContainer == nil || !*runner.Spec.DockerdWithinRunnerContainer {
			template.Spec.Containers = append(template.Spec.Containers, corev1.Container{
				Name:         "docker",
				VolumeMounts: runner.Spec.DockerVolumeMounts,
				Resources:    runner.Spec.DockerdContainerResources,
			})
		}
	} else {
		template.Spec.Containers = runner.Spec.Containers
	}

	template.Spec.SecurityContext = runner.Spec.SecurityContext
	template.Spec.EnableServiceLinks = runner.Spec.EnableServiceLinks

	registrationOnly := metav1.HasAnnotation(runner.ObjectMeta, annotationKeyRegistrationOnly)

	pod, err := newRunnerPod(template, runner.Spec.RunnerConfig, r.RunnerImage, r.DockerImage, r.DockerRegistryMirror, r.GitHubClient.GithubBaseURL, registrationOnly)
	if err != nil {
		return pod, err
	}

	// Customize the pod spec according to the runner spec
	runnerSpec := runner.Spec

	if len(runnerSpec.VolumeMounts) != 0 {
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, runnerSpec.VolumeMounts...)
	}

	if len(runnerSpec.Volumes) != 0 {
		pod.Spec.Volumes = append(pod.Spec.Volumes, runnerSpec.Volumes...)
	}
	if len(runnerSpec.InitContainers) != 0 {
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, runnerSpec.InitContainers...)
	}

	if runnerSpec.NodeSelector != nil {
		pod.Spec.NodeSelector = runnerSpec.NodeSelector
	}
	if runnerSpec.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = runnerSpec.ServiceAccountName
	}
	if runnerSpec.AutomountServiceAccountToken != nil {
		pod.Spec.AutomountServiceAccountToken = runnerSpec.AutomountServiceAccountToken
	}

	if len(runnerSpec.SidecarContainers) != 0 {
		pod.Spec.Containers = append(pod.Spec.Containers, runnerSpec.SidecarContainers...)
	}

	if len(runnerSpec.ImagePullSecrets) != 0 {
		pod.Spec.ImagePullSecrets = runnerSpec.ImagePullSecrets
	}

	if runnerSpec.Affinity != nil {
		pod.Spec.Affinity = runnerSpec.Affinity
	}

	if len(runnerSpec.Tolerations) != 0 {
		pod.Spec.Tolerations = runnerSpec.Tolerations
	}

	if len(runnerSpec.EphemeralContainers) != 0 {
		pod.Spec.EphemeralContainers = runnerSpec.EphemeralContainers
	}

	if runnerSpec.TerminationGracePeriodSeconds != nil {
		pod.Spec.TerminationGracePeriodSeconds = runnerSpec.TerminationGracePeriodSeconds
	}

	if len(runnerSpec.HostAliases) != 0 {
		pod.Spec.HostAliases = runnerSpec.HostAliases
	}

	if runnerSpec.RuntimeClassName != nil {
		pod.Spec.RuntimeClassName = runnerSpec.RuntimeClassName
	}

	pod.ObjectMeta.Name = runner.ObjectMeta.Name

	// Inject the registration token and the runner name
	updated := mutatePod(&pod, runner.Status.Registration.Token)

	if err := ctrl.SetControllerReference(&runner, updated, r.Scheme); err != nil {
		return pod, err
	}

	return *updated, nil
}

func mutatePod(pod *corev1.Pod, token string) *corev1.Pod {
	updated := pod.DeepCopy()

	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == "runner" {
			updated.Spec.Containers[i].Env = append(updated.Spec.Containers[i].Env,
				corev1.EnvVar{
					Name:  "RUNNER_NAME",
					Value: pod.ObjectMeta.Name,
				},
				corev1.EnvVar{
					Name:  "RUNNER_TOKEN",
					Value: token,
				},
			)
		}
	}

	return updated
}

func newRunnerPod(template corev1.Pod, runnerSpec v1alpha1.RunnerConfig, defaultRunnerImage, defaultDockerImage, defaultDockerRegistryMirror string, githubBaseURL string, registrationOnly bool) (corev1.Pod, error) {
	var (
		privileged                bool = true
		dockerdInRunner           bool = runnerSpec.DockerdWithinRunnerContainer != nil && *runnerSpec.DockerdWithinRunnerContainer
		dockerEnabled             bool = runnerSpec.DockerEnabled == nil || *runnerSpec.DockerEnabled
		ephemeral                 bool = runnerSpec.Ephemeral == nil || *runnerSpec.Ephemeral
		dockerdInRunnerPrivileged bool = dockerdInRunner
	)

	runnerImage := runnerSpec.Image
	if runnerImage == "" {
		runnerImage = defaultRunnerImage
	}

	workDir := runnerSpec.WorkDir
	if workDir == "" {
		workDir = "/runner/_work"
	}

	var dockerRegistryMirror string
	if runnerSpec.DockerRegistryMirror == nil {
		dockerRegistryMirror = defaultDockerRegistryMirror
	} else {
		dockerRegistryMirror = *runnerSpec.DockerRegistryMirror
	}

	env := []corev1.EnvVar{
		{
			Name:  EnvVarOrg,
			Value: runnerSpec.Organization,
		},
		{
			Name:  EnvVarRepo,
			Value: runnerSpec.Repository,
		},
		{
			Name:  EnvVarEnterprise,
			Value: runnerSpec.Enterprise,
		},
		{
			Name:  "RUNNER_LABELS",
			Value: strings.Join(runnerSpec.Labels, ","),
		},
		{
			Name:  "RUNNER_GROUP",
			Value: runnerSpec.Group,
		},
		{
			Name:  "DOCKERD_IN_RUNNER",
			Value: fmt.Sprintf("%v", dockerdInRunner),
		},
		{
			Name:  "GITHUB_URL",
			Value: githubBaseURL,
		},
		{
			Name:  "RUNNER_WORKDIR",
			Value: workDir,
		},
		{
			Name:  "RUNNER_EPHEMERAL",
			Value: fmt.Sprintf("%v", ephemeral),
		},
	}

	if registrationOnly {
		env = append(env, corev1.EnvVar{
			Name:  "RUNNER_REGISTRATION_ONLY",
			Value: "true",
		},
		)
	}

	var seLinuxOptions *corev1.SELinuxOptions
	if template.Spec.SecurityContext != nil {
		seLinuxOptions = template.Spec.SecurityContext.SELinuxOptions
		if seLinuxOptions != nil {
			privileged = false
			dockerdInRunnerPrivileged = false
		}
	}

	var runnerContainerIndex, dockerdContainerIndex int
	var runnerContainer, dockerdContainer *corev1.Container

	for i := range template.Spec.Containers {
		c := template.Spec.Containers[i]
		if c.Name == containerName {
			runnerContainerIndex = i
			runnerContainer = &c
		} else if c.Name == "docker" {
			dockerdContainerIndex = i
			dockerdContainer = &c
		}
	}

	if runnerContainer == nil {
		runnerContainerIndex = -1
		runnerContainer = &corev1.Container{
			Name: containerName,
			SecurityContext: &corev1.SecurityContext{
				// Runner need to run privileged if it contains DinD
				Privileged: &dockerdInRunnerPrivileged,
			},
		}
	}

	if dockerdContainer == nil {
		dockerdContainerIndex = -1
		dockerdContainer = &corev1.Container{
			Name: "docker",
		}
	}

	runnerContainer.Image = runnerImage
	if runnerContainer.ImagePullPolicy == "" {
		runnerContainer.ImagePullPolicy = corev1.PullAlways
	}

	runnerContainer.Env = append(runnerContainer.Env, env...)

	if runnerContainer.SecurityContext == nil {
		runnerContainer.SecurityContext = &corev1.SecurityContext{}
	}
	// Runner need to run privileged if it contains DinD
	runnerContainer.SecurityContext.Privileged = &dockerdInRunnerPrivileged

	pod := template.DeepCopy()

	if pod.Spec.RestartPolicy == "" {
		pod.Spec.RestartPolicy = "OnFailure"
	}

	if mtu := runnerSpec.DockerMTU; mtu != nil && dockerdInRunner {
		runnerContainer.Env = append(runnerContainer.Env, []corev1.EnvVar{
			{
				Name:  "MTU",
				Value: fmt.Sprintf("%d", *runnerSpec.DockerMTU),
			},
		}...)
	}

	if dockerRegistryMirror != "" && dockerdInRunner {
		runnerContainer.Env = append(runnerContainer.Env, []corev1.EnvVar{
			{
				Name:  "DOCKER_REGISTRY_MIRROR",
				Value: dockerRegistryMirror,
			},
		}...)
	}

	//
	// /runner must be generated on runtime from /runnertmp embedded in the container image.
	//
	// When you're NOT using dindWithinRunner=true,
	// it must also be shared with the dind container as it seems like required to run docker steps.
	//

	runnerVolumeName := "runner"
	runnerVolumeMountPath := "/runner"
	runnerVolumeEmptyDir := &corev1.EmptyDirVolumeSource{}

	if runnerSpec.VolumeSizeLimit != nil {
		runnerVolumeEmptyDir.SizeLimit = runnerSpec.VolumeSizeLimit
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes,
		corev1.Volume{
			Name: runnerVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: runnerVolumeEmptyDir,
			},
		},
	)

	runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts,
		corev1.VolumeMount{
			Name:      runnerVolumeName,
			MountPath: runnerVolumeMountPath,
		},
	)

	if !dockerdInRunner && dockerEnabled {
		pod.Spec.Volumes = append(pod.Spec.Volumes,
			corev1.Volume{
				Name: "work",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			corev1.Volume{
				Name: "certs-client",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
		runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts,
			corev1.VolumeMount{
				Name:      "work",
				MountPath: workDir,
			},
			corev1.VolumeMount{
				Name:      "certs-client",
				MountPath: "/certs/client",
				ReadOnly:  true,
			},
		)
		runnerContainer.Env = append(runnerContainer.Env, []corev1.EnvVar{
			{
				Name:  "DOCKER_HOST",
				Value: "tcp://localhost:2376",
			},
			{
				Name:  "DOCKER_TLS_VERIFY",
				Value: "1",
			},
			{
				Name:  "DOCKER_CERT_PATH",
				Value: "/certs/client",
			},
		}...)

		// Determine the volume mounts assigned to the docker sidecar. In case extra mounts are included in the RunnerSpec, append them to the standard
		// set of mounts. See https://github.com/actions-runner-controller/actions-runner-controller/issues/435 for context.
		dockerVolumeMounts := []corev1.VolumeMount{
			{
				Name:      "work",
				MountPath: workDir,
			},
			{
				Name:      runnerVolumeName,
				MountPath: runnerVolumeMountPath,
			},
			{
				Name:      "certs-client",
				MountPath: "/certs/client",
			},
		}

		if dockerdContainer.Image == "" {
			dockerdContainer.Image = defaultDockerImage
		}

		dockerdContainer.Env = append(dockerdContainer.Env, corev1.EnvVar{
			Name:  "DOCKER_TLS_CERTDIR",
			Value: "/certs",
		})

		if dockerdContainer.SecurityContext == nil {
			dockerdContainer.SecurityContext = &corev1.SecurityContext{
				Privileged:     &privileged,
				SELinuxOptions: seLinuxOptions,
			}
		}

		dockerdContainer.VolumeMounts = append(dockerdContainer.VolumeMounts, dockerVolumeMounts...)

		if mtu := runnerSpec.DockerMTU; mtu != nil {
			dockerdContainer.Env = append(dockerdContainer.Env, []corev1.EnvVar{
				// See https://docs.docker.com/engine/security/rootless/
				{
					Name:  "DOCKERD_ROOTLESS_ROOTLESSKIT_MTU",
					Value: fmt.Sprintf("%d", *runnerSpec.DockerMTU),
				},
			}...)

			dockerdContainer.Args = append(dockerdContainer.Args,
				"--mtu",
				fmt.Sprintf("%d", *runnerSpec.DockerMTU),
			)
		}

		if dockerRegistryMirror != "" {
			dockerdContainer.Args = append(dockerdContainer.Args,
				fmt.Sprintf("--registry-mirror=%s", dockerRegistryMirror),
			)
		}
	}

	if runnerContainerIndex == -1 {
		pod.Spec.Containers = append([]corev1.Container{*runnerContainer}, pod.Spec.Containers...)

		if dockerdContainerIndex != -1 {
			dockerdContainerIndex++
		}
	} else {
		pod.Spec.Containers[runnerContainerIndex] = *runnerContainer
	}

	if !dockerdInRunner && dockerEnabled {
		if dockerdContainerIndex == -1 {
			pod.Spec.Containers = append(pod.Spec.Containers, *dockerdContainer)
		} else {
			pod.Spec.Containers[dockerdContainerIndex] = *dockerdContainer
		}
	}

	return *pod, nil
}

func (r *RunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "runner-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Runner{}).
		Owns(&corev1.Pod{}).
		Named(name).
		Complete(r)
}

func addFinalizer(finalizers []string, finalizerName string) ([]string, bool) {
	exists := false
	for _, name := range finalizers {
		if name == finalizerName {
			exists = true
		}
	}

	if exists {
		return finalizers, false
	}

	return append(finalizers, finalizerName), true
}

func removeFinalizer(finalizers []string, finalizerName string) ([]string, bool) {
	removed := false
	result := []string{}

	for _, name := range finalizers {
		if name == finalizerName {
			removed = true
			continue
		}
		result = append(result, name)
	}

	return result, removed
}
