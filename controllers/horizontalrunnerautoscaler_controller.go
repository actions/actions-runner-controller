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
	"time"

	"github.com/summerwind/actions-runner-controller/github"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

const (
	DefaultScaleDownDelay = 10 * time.Minute
)

// HorizontalRunnerAutoscalerReconciler reconciles a HorizontalRunnerAutoscaler object
type HorizontalRunnerAutoscalerReconciler struct {
	client.Client
	GitHubClient *github.Client
	Log          logr.Logger
	Recorder     record.EventRecorder
	Scheme       *runtime.Scheme
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerdeployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *HorizontalRunnerAutoscalerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("horizontalrunnerautoscaler", req.NamespacedName)

	var hra v1alpha1.HorizontalRunnerAutoscaler
	if err := r.Get(ctx, req.NamespacedName, &hra); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !hra.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var rd v1alpha1.RunnerDeployment
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: req.Namespace,
		Name:      hra.Spec.ScaleTargetRef.Name,
	}, &rd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rd.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	replicas, err := r.computeReplicas(rd, hra)
	if err != nil {
		r.Recorder.Event(&hra, corev1.EventTypeNormal, "RunnerAutoscalingFailure", err.Error())

		log.Error(err, "Could not compute replicas")

		return ctrl.Result{}, err
	}

	const defaultReplicas = 1

	currentDesiredReplicas := getIntOrDefault(rd.Spec.Replicas, defaultReplicas)
	newDesiredReplicas := getIntOrDefault(replicas, defaultReplicas)

	// Please add more conditions that we can in-place update the newest runnerreplicaset without disruption
	if currentDesiredReplicas != newDesiredReplicas {
		copy := rd.DeepCopy()
		copy.Spec.Replicas = &newDesiredReplicas

		if err := r.Client.Update(ctx, copy); err != nil {
			log.Error(err, "Failed to update runnerderployment resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	if hra.Status.DesiredReplicas == nil || *hra.Status.DesiredReplicas != *replicas {
		updated := hra.DeepCopy()

		if (hra.Status.DesiredReplicas == nil && *replicas > 1) ||
			(hra.Status.DesiredReplicas != nil && *replicas > *hra.Status.DesiredReplicas) {

			updated.Status.LastSuccessfulScaleOutTime = &metav1.Time{Time: time.Now()}
		}

		updated.Status.DesiredReplicas = replicas

		if err := r.Status().Update(ctx, updated); err != nil {
			log.Error(err, "Failed to update horizontalrunnerautoscaler status")

			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *HorizontalRunnerAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("horizontalrunnerautoscaler-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.HorizontalRunnerAutoscaler{}).
		Complete(r)
}

func (r *HorizontalRunnerAutoscalerReconciler) computeReplicas(rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler) (*int, error) {
	var computedReplicas *int

	replicas, err := r.determineDesiredReplicas(rd, hra)
	if err != nil {
		return nil, err
	}

	var scaleDownDelay time.Duration

	if hra.Spec.ScaleDownDelaySecondsAfterScaleUp != nil {
		scaleDownDelay = time.Duration(*hra.Spec.ScaleDownDelaySecondsAfterScaleUp) * time.Second
	} else {
		scaleDownDelay = DefaultScaleDownDelay
	}

	now := time.Now()

	if hra.Status.DesiredReplicas == nil ||
		*hra.Status.DesiredReplicas < *replicas ||
		hra.Status.LastSuccessfulScaleOutTime == nil ||
		hra.Status.LastSuccessfulScaleOutTime.Add(scaleDownDelay).Before(now) {

		computedReplicas = replicas
	} else {
		computedReplicas = hra.Status.DesiredReplicas
	}

	return computedReplicas, nil
}
