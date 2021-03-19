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
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"time"

	"github.com/summerwind/actions-runner-controller/github"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/controllers/metrics"
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

	CacheDuration time.Duration
	Name          string
}

const defaultReplicas = 1

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

	metrics.SetHorizontalRunnerAutoscalerSpec(hra.ObjectMeta, hra.Spec)

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

	now := time.Now()

	newDesiredReplicas, computedReplicas, computedReplicasFromCache, err := r.computeReplicasWithCache(log, now, rd, hra)
	if err != nil {
		r.Recorder.Event(&hra, corev1.EventTypeNormal, "RunnerAutoscalingFailure", err.Error())

		log.Error(err, "Could not compute replicas")

		return ctrl.Result{}, err
	}

	currentDesiredReplicas := getIntOrDefault(rd.Spec.Replicas, defaultReplicas)

	// Please add more conditions that we can in-place update the newest runnerreplicaset without disruption
	if currentDesiredReplicas != newDesiredReplicas {
		copy := rd.DeepCopy()
		copy.Spec.Replicas = &newDesiredReplicas

		if err := r.Client.Patch(ctx, copy, client.MergeFrom(&rd)); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching runnerdeployment to have %d replicas: %w", newDesiredReplicas, err)
		}
	}

	var updated *v1alpha1.HorizontalRunnerAutoscaler

	if hra.Status.DesiredReplicas == nil || *hra.Status.DesiredReplicas != newDesiredReplicas {
		updated = hra.DeepCopy()

		if (hra.Status.DesiredReplicas == nil && newDesiredReplicas > 1) ||
			(hra.Status.DesiredReplicas != nil && newDesiredReplicas > *hra.Status.DesiredReplicas) {

			updated.Status.LastSuccessfulScaleOutTime = &metav1.Time{Time: time.Now()}
		}

		updated.Status.DesiredReplicas = &newDesiredReplicas
	}

	if computedReplicasFromCache == nil {
		if updated == nil {
			updated = hra.DeepCopy()
		}

		cacheEntries := getValidCacheEntries(updated, now)

		var cacheDuration time.Duration

		if r.CacheDuration > 0 {
			cacheDuration = r.CacheDuration
		} else {
			cacheDuration = 10 * time.Minute
		}

		updated.Status.CacheEntries = append(cacheEntries, v1alpha1.CacheEntry{
			Key:            v1alpha1.CacheEntryKeyDesiredReplicas,
			Value:          computedReplicas,
			ExpirationTime: metav1.Time{Time: time.Now().Add(cacheDuration)},
		})
	}

	if updated != nil {
		metrics.SetHorizontalRunnerAutoscalerStatus(updated.ObjectMeta, updated.Status)

		if err := r.Status().Patch(ctx, updated, client.MergeFrom(&hra)); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching horizontalrunnerautoscaler status to add cache entry: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

func getValidCacheEntries(hra *v1alpha1.HorizontalRunnerAutoscaler, now time.Time) []v1alpha1.CacheEntry {
	var cacheEntries []v1alpha1.CacheEntry

	for _, ent := range hra.Status.CacheEntries {
		if ent.ExpirationTime.After(now) {
			cacheEntries = append(cacheEntries, ent)
		}
	}

	return cacheEntries
}

func (r *HorizontalRunnerAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "horizontalrunnerautoscaler-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.HorizontalRunnerAutoscaler{}).
		Named(name).
		Complete(r)
}

func (r *HorizontalRunnerAutoscalerReconciler) computeReplicasWithCache(log logr.Logger, now time.Time, rd v1alpha1.RunnerDeployment, hra v1alpha1.HorizontalRunnerAutoscaler) (int, int, *int, error) {
	minReplicas := defaultReplicas
	if hra.Spec.MinReplicas != nil && *hra.Spec.MinReplicas > 0 {
		minReplicas = *hra.Spec.MinReplicas
	}

	var suggestedReplicas int

	suggestedReplicasFromCache := r.fetchSuggestedReplicasFromCache(hra)

	var cached *int

	if suggestedReplicasFromCache != nil {
		cached = suggestedReplicasFromCache

		if cached == nil {
			suggestedReplicas = minReplicas
		} else {
			suggestedReplicas = *cached
		}
	} else {
		v, err := r.suggestDesiredReplicas(rd, hra)
		if err != nil {
			return 0, 0, nil, err
		}

		if v == nil {
			suggestedReplicas = minReplicas
		} else {
			suggestedReplicas = *v
		}
	}

	var reserved int

	for _, reservation := range hra.Spec.CapacityReservations {
		if reservation.ExpirationTime.Time.After(now) {
			reserved += reservation.Replicas
		}
	}

	newDesiredReplicas := suggestedReplicas + reserved

	if newDesiredReplicas < minReplicas {
		newDesiredReplicas = minReplicas
	} else if hra.Spec.MaxReplicas != nil && newDesiredReplicas > *hra.Spec.MaxReplicas {
		newDesiredReplicas = *hra.Spec.MaxReplicas
	}

	//
	// Delay scaling-down for ScaleDownDelaySecondsAfterScaleUp or DefaultScaleDownDelay
	//

	var scaleDownDelay time.Duration

	if hra.Spec.ScaleDownDelaySecondsAfterScaleUp != nil {
		scaleDownDelay = time.Duration(*hra.Spec.ScaleDownDelaySecondsAfterScaleUp) * time.Second
	} else {
		scaleDownDelay = DefaultScaleDownDelay
	}

	var scaleDownDelayUntil *time.Time

	if hra.Status.DesiredReplicas == nil ||
		*hra.Status.DesiredReplicas < newDesiredReplicas ||
		hra.Status.LastSuccessfulScaleOutTime == nil {

	} else if hra.Status.LastSuccessfulScaleOutTime != nil {
		t := hra.Status.LastSuccessfulScaleOutTime.Add(scaleDownDelay)

		// ScaleDownDelay is not passed
		if t.After(now) {
			scaleDownDelayUntil = &t
			newDesiredReplicas = *hra.Status.DesiredReplicas
		}
	} else {
		newDesiredReplicas = *hra.Status.DesiredReplicas
	}

	//
	// Logs various numbers for monitoring and debugging purpose
	//

	kvs := []interface{}{
		"suggested", suggestedReplicas,
		"reserved", reserved,
		"min", minReplicas,
	}

	if cached != nil {
		kvs = append(kvs, "cached", *cached)
	}

	if scaleDownDelayUntil != nil {
		kvs = append(kvs, "last_scale_up_time", *hra.Status.LastSuccessfulScaleOutTime)
		kvs = append(kvs, "scale_down_delay_until", scaleDownDelayUntil)
	}

	if maxReplicas := hra.Spec.MaxReplicas; maxReplicas != nil {
		kvs = append(kvs, "max", *maxReplicas)
	}

	log.V(1).Info(fmt.Sprintf("Calculated desired replicas of %d", newDesiredReplicas),
		kvs...,
	)

	return newDesiredReplicas, suggestedReplicas, suggestedReplicasFromCache, nil
}
