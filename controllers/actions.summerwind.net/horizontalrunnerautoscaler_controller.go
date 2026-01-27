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
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.summerwind.net/metrics"
	arcgithub "github.com/actions/actions-runner-controller/github"
)

const (
	DefaultScaleDownDelay = 10 * time.Minute
)

// HorizontalRunnerAutoscalerReconciler reconciles a HorizontalRunnerAutoscaler object
type HorizontalRunnerAutoscalerReconciler struct {
	client.Client
	GitHubClient          *MultiGitHubClient
	Log                   logr.Logger
	Recorder              record.EventRecorder
	Scheme                *runtime.Scheme
	DefaultScaleDownDelay time.Duration
	Name                  string
}

const defaultReplicas = 1

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerdeployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *HorizontalRunnerAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("horizontalrunnerautoscaler", req.NamespacedName)

	var hra v1alpha1.HorizontalRunnerAutoscaler
	if err := r.Get(ctx, req.NamespacedName, &hra); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !hra.DeletionTimestamp.IsZero() {
		r.GitHubClient.DeinitForHRA(&hra)

		return ctrl.Result{}, nil
	}

	metrics.SetHorizontalRunnerAutoscalerSpec(hra.ObjectMeta, hra.Spec)

	kind := hra.Spec.ScaleTargetRef.Kind

	switch kind {
	case "", "RunnerDeployment":
		var rd v1alpha1.RunnerDeployment
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: req.Namespace,
			Name:      hra.Spec.ScaleTargetRef.Name,
		}, &rd); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		if !rd.DeletionTimestamp.IsZero() {
			return ctrl.Result{}, nil
		}

		st := r.scaleTargetFromRD(ctx, rd)

		return r.reconcile(ctx, req, log, hra, st, func(newDesiredReplicas int) error {
			currentDesiredReplicas := getIntOrDefault(rd.Spec.Replicas, defaultReplicas)

			ephemeral := rd.Spec.Template.Spec.Ephemeral == nil || *rd.Spec.Template.Spec.Ephemeral

			var effectiveTime *time.Time

			for _, r := range hra.Spec.CapacityReservations {
				t := r.EffectiveTime
				if effectiveTime == nil || effectiveTime.Before(t.Time) {
					effectiveTime = &t.Time
				}
			}

			// Please add more conditions that we can in-place update the newest runnerreplicaset without disruption
			if currentDesiredReplicas != newDesiredReplicas {
				copy := rd.DeepCopy()
				copy.Spec.Replicas = &newDesiredReplicas

				if ephemeral && effectiveTime != nil {
					copy.Spec.EffectiveTime = &metav1.Time{Time: *effectiveTime}
				}

				if err := r.Patch(ctx, copy, client.MergeFrom(&rd)); err != nil {
					return fmt.Errorf("patching runnerdeployment to have %d replicas: %w", newDesiredReplicas, err)
				}
			} else if ephemeral && effectiveTime != nil {
				copy := rd.DeepCopy()
				copy.Spec.EffectiveTime = &metav1.Time{Time: *effectiveTime}

				if err := r.Patch(ctx, copy, client.MergeFrom(&rd)); err != nil {
					return fmt.Errorf("patching runnerdeployment to have %d replicas: %w", newDesiredReplicas, err)
				}
			}
			return nil
		})
	case "RunnerSet":
		var rs v1alpha1.RunnerSet
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: req.Namespace,
			Name:      hra.Spec.ScaleTargetRef.Name,
		}, &rs); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		if !rs.DeletionTimestamp.IsZero() {
			return ctrl.Result{}, nil
		}

		var replicas *int

		if rs.Spec.Replicas != nil {
			v := int(*rs.Spec.Replicas)
			replicas = &v
		}

		st := scaleTarget{
			st:         rs.Name,
			kind:       "runnerset",
			enterprise: rs.Spec.Enterprise,
			org:        rs.Spec.Organization,
			repo:       rs.Spec.Repository,
			replicas:   replicas,
			labels:     rs.Spec.Labels,
			getRunnerMap: func() (map[string]struct{}, error) {
				// return the list of runners in namespace. Horizontal Runner Autoscaler should only be responsible for scaling resources in its own ns.
				var runnerPodList corev1.PodList

				var opts []client.ListOption

				opts = append(opts, client.InNamespace(rs.Namespace))

				selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
				if err != nil {
					return nil, err
				}

				opts = append(opts, client.MatchingLabelsSelector{Selector: selector})

				r.Log.V(2).Info("Finding runnerset's runner pods with selector", "ns", rs.Namespace)

				if err := r.List(
					ctx,
					&runnerPodList,
					opts...,
				); err != nil {
					if !kerrors.IsNotFound(err) {
						return nil, err
					}
				}
				runnerMap := make(map[string]struct{})
				for _, items := range runnerPodList.Items {
					runnerMap[items.Name] = struct{}{}
				}

				return runnerMap, nil
			},
		}

		return r.reconcile(ctx, req, log, hra, st, func(newDesiredReplicas int) error {
			var replicas *int
			if rs.Spec.Replicas != nil {
				v := int(*rs.Spec.Replicas)
				replicas = &v
			}
			currentDesiredReplicas := getIntOrDefault(replicas, defaultReplicas)

			ephemeral := rs.Spec.Ephemeral == nil || *rs.Spec.Ephemeral

			var effectiveTime *time.Time

			for _, r := range hra.Spec.CapacityReservations {
				t := r.EffectiveTime
				if effectiveTime == nil || effectiveTime.Before(t.Time) {
					effectiveTime = &t.Time
				}
			}

			if currentDesiredReplicas != newDesiredReplicas {
				copy := rs.DeepCopy()
				v := int32(newDesiredReplicas)
				copy.Spec.Replicas = &v

				if ephemeral && effectiveTime != nil {
					copy.Spec.EffectiveTime = &metav1.Time{Time: *effectiveTime}
				}

				if err := r.Patch(ctx, copy, client.MergeFrom(&rs)); err != nil {
					return fmt.Errorf("patching runnerset to have %d replicas: %w", newDesiredReplicas, err)
				}
			} else if ephemeral && effectiveTime != nil {
				copy := rs.DeepCopy()
				copy.Spec.EffectiveTime = &metav1.Time{Time: *effectiveTime}

				if err := r.Patch(ctx, copy, client.MergeFrom(&rs)); err != nil {
					return fmt.Errorf("patching runnerset to have %d replicas: %w", newDesiredReplicas, err)
				}
			}

			return nil
		})
	}

	log.Info(fmt.Sprintf("Unsupported scale target %s %s: kind %s is not supported. valid kinds are %s and %s", kind, hra.Spec.ScaleTargetRef.Name, kind, "RunnerDeployment", "RunnerSet"))

	return ctrl.Result{}, nil
}

func (r *HorizontalRunnerAutoscalerReconciler) scaleTargetFromRD(ctx context.Context, rd v1alpha1.RunnerDeployment) scaleTarget {
	st := scaleTarget{
		st:         rd.Name,
		kind:       "runnerdeployment",
		enterprise: rd.Spec.Template.Spec.Enterprise,
		org:        rd.Spec.Template.Spec.Organization,
		repo:       rd.Spec.Template.Spec.Repository,
		replicas:   rd.Spec.Replicas,
		labels:     rd.Spec.Template.Spec.Labels,
		getRunnerMap: func() (map[string]struct{}, error) {
			// return the list of runners in namespace. Horizontal Runner Autoscaler should only be responsible for scaling resources in its own ns.
			var runnerList v1alpha1.RunnerList

			var opts []client.ListOption

			opts = append(opts, client.InNamespace(rd.Namespace))

			selector, err := metav1.LabelSelectorAsSelector(getSelector(&rd))
			if err != nil {
				return nil, err
			}

			opts = append(opts, client.MatchingLabelsSelector{Selector: selector})

			r.Log.V(2).Info("Finding runners with selector", "ns", rd.Namespace)

			if err := r.List(
				ctx,
				&runnerList,
				opts...,
			); err != nil {
				if !kerrors.IsNotFound(err) {
					return nil, err
				}
			}
			runnerMap := make(map[string]struct{})
			for _, items := range runnerList.Items {
				runnerMap[items.Name] = struct{}{}
			}

			return runnerMap, nil
		},
	}

	return st
}

type scaleTarget struct {
	st, kind              string
	enterprise, repo, org string
	replicas              *int
	labels                []string

	getRunnerMap func() (map[string]struct{}, error)
}

func (r *HorizontalRunnerAutoscalerReconciler) reconcile(ctx context.Context, req ctrl.Request, log logr.Logger, hra v1alpha1.HorizontalRunnerAutoscaler, st scaleTarget, updatedDesiredReplicas func(int) error) (ctrl.Result, error) {
	now := time.Now()

	minReplicas, active, upcoming, err := r.getMinReplicas(log, now, hra)
	if err != nil {
		log.Error(err, "Could not compute min replicas")

		return ctrl.Result{}, err
	}

	ghc, err := r.GitHubClient.InitForHRA(context.Background(), &hra)
	if err != nil {
		return ctrl.Result{}, err
	}

	newDesiredReplicas, err := r.computeReplicasWithCache(ghc, log, now, st, hra, minReplicas)
	if err != nil {
		r.Recorder.Event(&hra, corev1.EventTypeNormal, "RunnerAutoscalingFailure", err.Error())

		log.Error(err, "Could not compute replicas")

		return ctrl.Result{}, err
	}

	if err := updatedDesiredReplicas(newDesiredReplicas); err != nil {
		return ctrl.Result{}, err
	}

	updated := hra.DeepCopy()

	if hra.Status.DesiredReplicas == nil || *hra.Status.DesiredReplicas != newDesiredReplicas {
		if (hra.Status.DesiredReplicas == nil && newDesiredReplicas > 1) ||
			(hra.Status.DesiredReplicas != nil && newDesiredReplicas > *hra.Status.DesiredReplicas) {

			updated.Status.LastSuccessfulScaleOutTime = &metav1.Time{Time: time.Now()}
		}

		updated.Status.DesiredReplicas = &newDesiredReplicas
	}

	var overridesSummary string

	if (active != nil && upcoming == nil) || (active != nil && upcoming != nil && active.Period.EndTime.Before(upcoming.Period.StartTime)) {
		after := defaultReplicas
		if hra.Spec.MinReplicas != nil && *hra.Spec.MinReplicas >= 0 {
			after = *hra.Spec.MinReplicas
		}

		overridesSummary = fmt.Sprintf("min=%d time=%s", after, active.Period.EndTime)
	}

	if active == nil && upcoming != nil || (active != nil && upcoming != nil && active.Period.EndTime.After(upcoming.Period.StartTime)) {
		if upcoming.ScheduledOverride.MinReplicas != nil {
			overridesSummary = fmt.Sprintf("min=%d time=%s", *upcoming.ScheduledOverride.MinReplicas, upcoming.Period.StartTime)
		}
	}

	if overridesSummary != "" {
		updated.Status.ScheduledOverridesSummary = &overridesSummary
	} else {
		updated.Status.ScheduledOverridesSummary = nil
	}

	if !reflect.DeepEqual(hra.Status, updated.Status) {
		metrics.SetHorizontalRunnerAutoscalerStatus(updated.ObjectMeta, updated.Status)

		if err := r.Status().Patch(ctx, updated, client.MergeFrom(&hra)); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching horizontalrunnerautoscaler status: %w", err)
		}
	}

	return ctrl.Result{}, nil
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

type Override struct {
	ScheduledOverride v1alpha1.ScheduledOverride
	Period            Period
}

func (r *HorizontalRunnerAutoscalerReconciler) matchScheduledOverrides(log logr.Logger, now time.Time, hra v1alpha1.HorizontalRunnerAutoscaler) (*int, *Override, *Override, error) {
	var minReplicas *int
	var active, upcoming *Override

	for _, o := range hra.Spec.ScheduledOverrides {
		log.V(1).Info(
			"Checking scheduled override",
			"now", now,
			"startTime", o.StartTime,
			"endTime", o.EndTime,
			"frequency", o.RecurrenceRule.Frequency,
			"untilTime", o.RecurrenceRule.UntilTime,
		)

		a, u, err := MatchSchedule(
			now, o.StartTime.Time, o.EndTime.Time,
			RecurrenceRule{
				Frequency: o.RecurrenceRule.Frequency,
				UntilTime: o.RecurrenceRule.UntilTime.Time,
			},
		)
		if err != nil {
			return minReplicas, nil, nil, err
		}

		// Use the first when there are two or more active scheduled overrides,
		// as the spec defines that the earlier scheduled override is prioritized higher than later ones.
		if a != nil && active == nil {
			active = &Override{Period: *a, ScheduledOverride: o}

			if o.MinReplicas != nil {
				minReplicas = o.MinReplicas

				log.V(1).Info(
					"Found active scheduled override",
					"activeStartTime", a.StartTime,
					"activeEndTime", a.EndTime,
					"activeMinReplicas", minReplicas,
				)
			}
		}

		if u != nil && (upcoming == nil || u.StartTime.Before(upcoming.Period.StartTime)) {
			upcoming = &Override{Period: *u, ScheduledOverride: o}

			log.V(1).Info(
				"Found upcoming scheduled override",
				"upcomingStartTime", u.StartTime,
				"upcomingEndTime", u.EndTime,
				"upcomingMinReplicas", o.MinReplicas,
			)
		}
	}

	return minReplicas, active, upcoming, nil
}

func (r *HorizontalRunnerAutoscalerReconciler) getMinReplicas(log logr.Logger, now time.Time, hra v1alpha1.HorizontalRunnerAutoscaler) (int, *Override, *Override, error) {
	minReplicas := defaultReplicas
	if hra.Spec.MinReplicas != nil && *hra.Spec.MinReplicas >= 0 {
		minReplicas = *hra.Spec.MinReplicas
	}

	m, active, upcoming, err := r.matchScheduledOverrides(log, now, hra)
	if err != nil {
		return 0, nil, nil, err
	} else if m != nil {
		minReplicas = *m
	}

	return minReplicas, active, upcoming, nil
}

func (r *HorizontalRunnerAutoscalerReconciler) computeReplicasWithCache(ghc *arcgithub.Client, log logr.Logger, now time.Time, st scaleTarget, hra v1alpha1.HorizontalRunnerAutoscaler, minReplicas int) (int, error) {
	var suggestedReplicas int

	v, err := r.suggestDesiredReplicas(ghc, st, hra)
	if err != nil {
		return 0, err
	}

	if v == nil {
		suggestedReplicas = minReplicas
	} else {
		suggestedReplicas = *v
	}

	var reserved int

	for _, reservation := range hra.Spec.CapacityReservations {
		if reservation.ExpirationTime.After(now) {
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
		scaleDownDelay = r.DefaultScaleDownDelay
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

	if maxReplicas := hra.Spec.MaxReplicas; maxReplicas != nil {
		kvs = append(kvs, "max", *maxReplicas)
	}

	if scaleDownDelayUntil != nil {
		kvs = append(kvs, "last_scale_up_time", *hra.Status.LastSuccessfulScaleOutTime)
		kvs = append(kvs, "scale_down_delay_until", scaleDownDelayUntil)
	}

	log.V(1).Info(fmt.Sprintf("Calculated desired replicas of %d", newDesiredReplicas),
		kvs...,
	)

	return newDesiredReplicas, nil
}
