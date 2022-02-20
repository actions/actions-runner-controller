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
	"reflect"
	"time"

	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v39/github"

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

// RunnerReplicaSetReconciler reconciles a Runner object
type RunnerReplicaSetReconciler struct {
	client.Client
	Log          logr.Logger
	Recorder     record.EventRecorder
	Scheme       *runtime.Scheme
	GitHubClient *github.Client
	Name         string
}

const (
	SyncTimeAnnotationKey = "sync-time"
)

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerreplicasets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerreplicasets/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerreplicasets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerReplicaSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runnerreplicaset", req.NamespacedName)

	var rs v1alpha1.RunnerReplicaSet
	if err := r.Get(ctx, req.NamespacedName, &rs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rs.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Get the Runners managed by the target RunnerReplicaSet
	var allRunners v1alpha1.RunnerList
	if err := r.List(
		ctx,
		&allRunners,
		client.InNamespace(req.Namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	var (
		current   int
		ready     int
		available int

		lastSyncTime *time.Time
	)

	for _, r := range allRunners.Items {
		// This guard is required to avoid the RunnerReplicaSet created by the controller v0.17.0 or before
		// to not treat all the runners in the namespace as its children.
		if metav1.IsControlledBy(&r, &rs) && !metav1.HasAnnotation(r.ObjectMeta, annotationKeyRegistrationOnly) {
			// If the runner is already marked for deletion(=has a non-zero deletion timestamp) by the runner controller (can be caused by an ephemeral runner completion)
			// or by runnerreplicaset controller (in case it was deleted in the previous reconcilation loop),
			// we don't need to bother calling GitHub API to re-mark the runner for deletion.
			// Just hold on, and runners will disappear as long as the runner controller is up and running.
			if !r.DeletionTimestamp.IsZero() {
				continue
			}

			if r.Annotations != nil {
				if a, ok := r.Annotations[SyncTimeAnnotationKey]; ok {
					t, err := time.Parse(time.RFC3339, a)
					if err == nil {
						if lastSyncTime == nil || lastSyncTime.Before(t) {
							lastSyncTime = &t
						}
					}
				}
			}

			current += 1

			if r.Status.Phase == string(corev1.PodRunning) {
				ready += 1
				// available is currently the same as ready, as we don't yet have minReadySeconds for runners
				available += 1
			}
		}
	}

	var desired int

	if rs.Spec.Replicas != nil {
		desired = *rs.Spec.Replicas
	} else {
		desired = 1
	}

	// TODO: remove this registration runner cleanup later (v0.23.0 or v0.24.0)
	//
	// We had to have a registration-only runner to support scale-from-zero before.
	// But since Sep 2021 Actions update on GitHub Cloud and GHES 3.3, it is unneceesary.
	// See the below issues for more contexts:
	// https://github.com/actions-runner-controller/actions-runner-controller/issues/516
	// https://github.com/actions-runner-controller/actions-runner-controller/issues/859
	//
	// In the below block, we have a logic to remove existing registration-only runners as unnecessary.
	// This logic is introduced since actions-runner-controller 0.21.0 and probably last one or two minor releases
	// so that actions-runner-controller instance in everyone's cluster won't leave dangling registration-only runners.
	registrationOnlyRunnerNsName := req.NamespacedName
	registrationOnlyRunnerNsName.Name = registrationOnlyRunnerNameFor(rs.Name)
	registrationOnlyRunner := v1alpha1.Runner{}
	registrationOnlyRunnerExists := false
	if err := r.Get(
		ctx,
		registrationOnlyRunnerNsName,
		&registrationOnlyRunner,
	); err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	} else {
		registrationOnlyRunnerExists = true
	}

	if registrationOnlyRunnerExists {
		if err := r.Client.Delete(ctx, &registrationOnlyRunner); err != nil {
			log.Error(err, "Retrying soon because we failed to delete registration-only runner")

			return ctrl.Result{Requeue: true}, nil
		}
	}

	effectiveTime := rs.Spec.EffectiveTime
	ephemeral := rs.Spec.Template.Spec.Ephemeral == nil || *rs.Spec.Template.Spec.Ephemeral

	if current < desired && ephemeral && lastSyncTime != nil && effectiveTime != nil && lastSyncTime.After(effectiveTime.Time) {
		log.V(1).Info("Detected that some ephemeral runners have disappeared. Usually this is due to that ephemeral runner completions so ARC does not create new runners until EffectiveTime is updated.", "lastSyncTime", metav1.Time{Time: *lastSyncTime}, "effectiveTime", *effectiveTime, "desired", desired, "available", current, "ready", ready)
	} else if current > desired {
		// If you use ephemeral runners with webhook-based autoscaler and the runner controller is working normally,
		// you're unlikely to fall into this branch.
		//
		// That's becaseu all the stakeholders work like this:
		//
		// 1. A runner pod completes with the runner container exiting with code 0
		// 2. ARC runner controller detects the pod completion, marks the runner resource on k8s for deletion (=Runner.DeletionTimestamp becomes non-zero)
		// 3. GitHub triggers a corresponding workflow_job "complete" webhook event
		// 4. ARC github-webhook-server (webhook-based autoscaler) receives the webhook event updates HRA with removing the oldest capacity reservation
		// 5. ARC horizontalrunnerautoscaler updates RunnerDeployment's desired replicas based on capacity reservations
		// 6. ARC runnerdeployment controller updates RunnerReplicaSet's desired replicas
		// 7. (We're here) ARC runnerreplicaset controller (this controller) starts reconciling the RunnerReplicaSet
		//
		// In a normally working ARC installation, the runner that was used to run the workflow job should already have been
		// marked for deletion by the runner controller.
		// This runnerreplicaset controller doesn't count marked runners into the `current` value, hence you're unlikely to
		// fall into this branch when you're using ephemeral runners with webhook-based-autoscaler.

		n := current - desired

		log.V(0).Info(fmt.Sprintf("Deleting %d runners", n), "desired", desired, "current", current, "ready", ready)

		// get runners that are currently offline/not busy/timed-out to register
		var deletionCandidates []v1alpha1.Runner

		for _, runner := range allRunners.Items {
			busy, err := r.GitHubClient.IsRunnerBusy(ctx, runner.Spec.Enterprise, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
			if err != nil {
				notRegistered := false
				offline := false

				var notFoundException *github.RunnerNotFound
				var offlineException *github.RunnerOffline
				if errors.As(err, &notFoundException) {
					log.V(1).Info("Failed to check if runner is busy. Either this runner has never been successfully registered to GitHub or it still needs more time.", "runnerName", runner.Name)
					notRegistered = true
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
								"Failed to check if runner is busy due to GitHub API rate limit. Retrying in %s to avoid excessive GitHub API calls",
								retryDelayOnGitHubAPIRateLimitError,
							),
						)

						return ctrl.Result{RequeueAfter: retryDelayOnGitHubAPIRateLimitError}, err
					}

					return ctrl.Result{}, err
				}

				registrationTimeout := 15 * time.Minute
				currentTime := time.Now()
				registrationDidTimeout := currentTime.Sub(runner.CreationTimestamp.Add(registrationTimeout)) > 0

				if notRegistered && registrationDidTimeout {
					log.Info(
						"Runner failed to register itself to GitHub in timely manner. "+
							"Marking the runner for scale down. "+
							"CAUTION: If you see this a lot, you should investigate the root cause. "+
							"See https://github.com/actions-runner-controller/actions-runner-controller/issues/288",
						"runnerCreationTimestamp", runner.CreationTimestamp,
						"currentTime", currentTime,
						"configuredRegistrationTimeout", registrationTimeout,
					)

					deletionCandidates = append(deletionCandidates, runner)
				}

				// offline runners should always be a great target for scale down
				if offline {
					deletionCandidates = append(deletionCandidates, runner)
				}
			} else if !busy {
				deletionCandidates = append(deletionCandidates, runner)
			}
		}

		if len(deletionCandidates) < n {
			n = len(deletionCandidates)
		}

		log.V(0).Info(fmt.Sprintf("Deleting %d runner(s)", n), "desired", desired, "current", current, "ready", ready)

		for i := 0; i < n; i++ {
			if err := r.Client.Delete(ctx, &deletionCandidates[i]); client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to delete runner resource")

				return ctrl.Result{}, err
			}

			r.Recorder.Event(&rs, corev1.EventTypeNormal, "RunnerDeleted", fmt.Sprintf("Deleted runner '%s'", deletionCandidates[i].Name))
			log.Info("Deleted runner")
		}
	} else if desired > current {
		n := desired - current

		log.V(0).Info(fmt.Sprintf("Creating %d runner(s)", n), "desired", desired, "available", current, "ready", ready)

		for i := 0; i < n; i++ {
			newRunner, err := r.newRunner(rs)
			if err != nil {
				log.Error(err, "Could not create runner")

				return ctrl.Result{}, err
			}

			if err := r.Client.Create(ctx, &newRunner); err != nil {
				log.Error(err, "Failed to create runner resource")

				return ctrl.Result{}, err
			}
		}
	}

	var status v1alpha1.RunnerReplicaSetStatus

	status.Replicas = &current
	status.AvailableReplicas = &available
	status.ReadyReplicas = &ready

	if !reflect.DeepEqual(rs.Status, status) {
		updated := rs.DeepCopy()
		updated.Status = status

		if err := r.Status().Patch(ctx, updated, client.MergeFrom(&rs)); err != nil {
			log.Info("Failed to update runnerreplicaset status. Retrying immediately", "error", err.Error())
			return ctrl.Result{
				Requeue: true,
			}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *RunnerReplicaSetReconciler) newRunner(rs v1alpha1.RunnerReplicaSet) (v1alpha1.Runner, error) {
	objectMeta := rs.Spec.Template.ObjectMeta.DeepCopy()

	objectMeta.GenerateName = rs.ObjectMeta.Name + "-"
	objectMeta.Namespace = rs.ObjectMeta.Namespace
	if objectMeta.Annotations == nil {
		objectMeta.Annotations = map[string]string{}
	}
	objectMeta.Annotations[SyncTimeAnnotationKey] = time.Now().Format(time.RFC3339)

	runner := v1alpha1.Runner{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: *objectMeta,
		Spec:       rs.Spec.Template.Spec,
	}

	if err := ctrl.SetControllerReference(&rs, &runner, r.Scheme); err != nil {
		return runner, err
	}

	return runner, nil
}

func (r *RunnerReplicaSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "runnerreplicaset-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RunnerReplicaSet{}).
		Owns(&v1alpha1.Runner{}).
		Named(name).
		Complete(r)
}

func registrationOnlyRunnerNameFor(rsName string) string {
	return rsName + "-registration-only"
}
