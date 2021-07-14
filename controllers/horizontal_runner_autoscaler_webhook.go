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
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v37/github"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
)

const (
	scaleTargetKey = "scaleTarget"
)

// HorizontalRunnerAutoscalerGitHubWebhook autoscales a HorizontalRunnerAutoscaler and the RunnerDeployment on each
// GitHub Webhook received
type HorizontalRunnerAutoscalerGitHubWebhook struct {
	client.Client
	Log      logr.Logger
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme

	// SecretKeyBytes is the byte representation of the Webhook secret token
	// the administrator is generated and specified in GitHub Web UI.
	SecretKeyBytes []byte

	// Namespace is the namespace to watch for HorizontalRunnerAutoscaler's to be
	// scaled on Webhook.
	// Set to empty for letting it watch for all namespaces.
	Namespace string
	Name      string
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) Reconcile(_ context.Context, request reconcile.Request) (reconcile.Result, error) {
	return ctrl.Result{}, nil
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=horizontalrunnerautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) Handle(w http.ResponseWriter, r *http.Request) {
	var (
		ok bool

		err error
	)

	defer func() {
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)

			if err != nil {
				msg := err.Error()
				if written, err := w.Write([]byte(msg)); err != nil {
					autoscaler.Log.Error(err, "failed writing http error response", "msg", msg, "written", written)
				}
			}
		}
	}()

	defer func() {
		if r.Body != nil {
			r.Body.Close()
		}
	}()

	// respond ok to GET / e.g. for health check
	if r.Method == http.MethodGet {
		fmt.Fprintln(w, "webhook server is running")
		return
	}

	var payload []byte

	if len(autoscaler.SecretKeyBytes) > 0 {
		payload, err = gogithub.ValidatePayload(r, autoscaler.SecretKeyBytes)
		if err != nil {
			autoscaler.Log.Error(err, "error validating request body")

			return
		}
	} else {
		payload, err = ioutil.ReadAll(r.Body)
		if err != nil {
			autoscaler.Log.Error(err, "error reading request body")

			return
		}
	}

	webhookType := gogithub.WebHookType(r)
	event, err := gogithub.ParseWebHook(webhookType, payload)
	if err != nil {
		var s string
		if payload != nil {
			s = string(payload)
		}

		autoscaler.Log.Error(err, "could not parse webhook", "webhookType", webhookType, "payload", s)

		return
	}

	var target *ScaleTarget

	log := autoscaler.Log.WithValues(
		"event", webhookType,
		"hookID", r.Header.Get("X-GitHub-Hook-ID"),
		"delivery", r.Header.Get("X-GitHub-Delivery"),
	)

	switch e := event.(type) {
	case *gogithub.PushEvent:
		target, err = autoscaler.getScaleUpTarget(
			context.TODO(),
			log,
			e.Repo.GetName(),
			e.Repo.Owner.GetLogin(),
			e.Repo.Owner.GetType(),
			autoscaler.MatchPushEvent(e),
		)
	case *gogithub.PullRequestEvent:
		target, err = autoscaler.getScaleUpTarget(
			context.TODO(),
			log,
			e.Repo.GetName(),
			e.Repo.Owner.GetLogin(),
			e.Repo.Owner.GetType(),
			autoscaler.MatchPullRequestEvent(e),
		)

		if pullRequest := e.PullRequest; pullRequest != nil {
			log = log.WithValues(
				"pullRequest.base.ref", e.PullRequest.Base.GetRef(),
				"action", e.GetAction(),
			)
		}
	case *gogithub.CheckRunEvent:
		target, err = autoscaler.getScaleUpTarget(
			context.TODO(),
			log,
			e.Repo.GetName(),
			e.Repo.Owner.GetLogin(),
			e.Repo.Owner.GetType(),
			autoscaler.MatchCheckRunEvent(e),
		)

		if checkRun := e.GetCheckRun(); checkRun != nil {
			log = log.WithValues(
				"checkRun.status", checkRun.GetStatus(),
				"action", e.GetAction(),
			)
		}
	case *gogithub.PingEvent:
		ok = true

		w.WriteHeader(http.StatusOK)

		msg := "pong"

		if written, err := w.Write([]byte(msg)); err != nil {
			log.Error(err, "failed writing http response", "msg", msg, "written", written)
		}

		log.Info("received ping event")

		return
	default:
		log.Info("unknown event type", "eventType", webhookType)

		return
	}

	if err != nil {
		log.Error(err, "handling check_run event")

		return
	}

	if target == nil {
		log.Info(
			"Scale target not found. If this is unexpected, ensure that there is exactly one repository-wide or organizational runner deployment that matches this webhook event",
		)

		msg := "no horizontalrunnerautoscaler to scale for this github event"

		ok = true

		w.WriteHeader(http.StatusOK)

		if written, err := w.Write([]byte(msg)); err != nil {
			log.Error(err, "failed writing http response", "msg", msg, "written", written)
		}

		return
	}

	if err := autoscaler.tryScaleUp(context.TODO(), target); err != nil {
		log.Error(err, "could not scale up")

		return
	}

	ok = true

	w.WriteHeader(http.StatusOK)

	msg := fmt.Sprintf("scaled %s by 1", target.Name)

	autoscaler.Log.Info(msg)

	if written, err := w.Write([]byte(msg)); err != nil {
		log.Error(err, "failed writing http response", "msg", msg, "written", written)
	}
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) findHRAsByKey(ctx context.Context, value string) ([]v1alpha1.HorizontalRunnerAutoscaler, error) {
	ns := autoscaler.Namespace

	var defaultListOpts []client.ListOption

	if ns != "" {
		defaultListOpts = append(defaultListOpts, client.InNamespace(ns))
	}

	var hras []v1alpha1.HorizontalRunnerAutoscaler

	if value != "" {
		opts := append([]client.ListOption{}, defaultListOpts...)
		opts = append(opts, client.MatchingFields{scaleTargetKey: value})

		if autoscaler.Namespace != "" {
			opts = append(opts, client.InNamespace(autoscaler.Namespace))
		}

		var hraList v1alpha1.HorizontalRunnerAutoscalerList

		if err := autoscaler.List(ctx, &hraList, opts...); err != nil {
			return nil, err
		}

		for _, d := range hraList.Items {
			hras = append(hras, d)
		}
	}

	return hras, nil
}

func matchTriggerConditionAgainstEvent(types []string, eventAction *string) bool {
	if len(types) == 0 {
		return true
	}

	if eventAction == nil {
		return false
	}

	for _, tpe := range types {
		if tpe == *eventAction {
			return true
		}
	}

	return false
}

type ScaleTarget struct {
	v1alpha1.HorizontalRunnerAutoscaler
	v1alpha1.ScaleUpTrigger
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) searchScaleTargets(hras []v1alpha1.HorizontalRunnerAutoscaler, f func(v1alpha1.ScaleUpTrigger) bool) []ScaleTarget {
	var matched []ScaleTarget

	for _, hra := range hras {
		if !hra.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}

		for _, scaleUpTrigger := range hra.Spec.ScaleUpTriggers {
			if !f(scaleUpTrigger) {
				continue
			}

			matched = append(matched, ScaleTarget{
				HorizontalRunnerAutoscaler: hra,
				ScaleUpTrigger:             scaleUpTrigger,
			})
		}
	}

	return matched
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) getScaleTarget(ctx context.Context, name string, f func(v1alpha1.ScaleUpTrigger) bool) (*ScaleTarget, error) {
	hras, err := autoscaler.findHRAsByKey(ctx, name)
	if err != nil {
		return nil, err
	}

	autoscaler.Log.V(1).Info(fmt.Sprintf("Found %d HRAs by key", len(hras)), "key", name)

	targets := autoscaler.searchScaleTargets(hras, f)

	n := len(targets)

	if n == 0 {
		return nil, nil
	}

	if n > 1 {
		var scaleTargetIDs []string

		for _, t := range targets {
			scaleTargetIDs = append(scaleTargetIDs, t.HorizontalRunnerAutoscaler.Name)
		}

		autoscaler.Log.Info(
			"Found too many scale targets: "+
				"It must be exactly one to avoid ambiguity. "+
				"Either set Namespace for the webhook-based autoscaler to let it only find HRAs in the namespace, "+
				"or update Repository or Organization fields in your RunnerDeployment resources to fix the ambiguity.",
			"scaleTargets", strings.Join(scaleTargetIDs, ","))

		return nil, nil
	}

	return &targets[0], nil
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) getScaleUpTarget(ctx context.Context, log logr.Logger, repo, owner, ownerType string, f func(v1alpha1.ScaleUpTrigger) bool) (*ScaleTarget, error) {
	repositoryRunnerKey := owner + "/" + repo

	if target, err := autoscaler.getScaleTarget(ctx, repositoryRunnerKey, f); err != nil {
		log.Info("finding repository-wide runner", "repository", repositoryRunnerKey)
		return nil, err
	} else if target != nil {
		log.Info("scale up target is repository-wide runners", "repository", repo)
		return target, nil
	}

	if ownerType == "User" {
		log.V(1).Info("no repository runner found", "organization", owner)

		return nil, nil
	}

	if target, err := autoscaler.getScaleTarget(ctx, owner, f); err != nil {
		log.Info("finding organizational runner", "organization", owner)
		return nil, err
	} else if target != nil {
		log.Info("scale up target is organizational runners", "organization", owner)
		return target, nil
	} else {
		log.V(1).Info("no repository runner or organizational runner found",
			"repository", repositoryRunnerKey,
			"organization", owner,
		)
	}

	return nil, nil
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) tryScaleUp(ctx context.Context, target *ScaleTarget) error {
	if target == nil {
		return nil
	}

	copy := target.HorizontalRunnerAutoscaler.DeepCopy()

	amount := 1

	if target.ScaleUpTrigger.Amount > 0 {
		amount = target.ScaleUpTrigger.Amount
	}

	capacityReservations := getValidCapacityReservations(copy)

	copy.Spec.CapacityReservations = append(capacityReservations, v1alpha1.CapacityReservation{
		ExpirationTime: metav1.Time{Time: time.Now().Add(target.ScaleUpTrigger.Duration.Duration)},
		Replicas:       amount,
	})

	if err := autoscaler.Client.Patch(ctx, copy, client.MergeFrom(&target.HorizontalRunnerAutoscaler)); err != nil {
		return fmt.Errorf("patching horizontalrunnerautoscaler to add capacity reservation: %w", err)
	}

	return nil
}

func getValidCapacityReservations(autoscaler *v1alpha1.HorizontalRunnerAutoscaler) []v1alpha1.CapacityReservation {
	var capacityReservations []v1alpha1.CapacityReservation

	now := time.Now()

	for _, reservation := range autoscaler.Spec.CapacityReservations {
		if reservation.ExpirationTime.Time.After(now) {
			capacityReservations = append(capacityReservations, reservation)
		}
	}

	return capacityReservations
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) SetupWithManager(mgr ctrl.Manager) error {
	name := "webhookbasedautoscaler"
	if autoscaler.Name != "" {
		name = autoscaler.Name
	}

	autoscaler.Recorder = mgr.GetEventRecorderFor(name)

	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.HorizontalRunnerAutoscaler{}, scaleTargetKey, func(rawObj client.Object) []string {
		hra := rawObj.(*v1alpha1.HorizontalRunnerAutoscaler)

		if hra.Spec.ScaleTargetRef.Name == "" {
			return nil
		}

		var rd v1alpha1.RunnerDeployment

		if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rd); err != nil {
			return nil
		}

		return []string{rd.Spec.Template.Spec.Repository, rd.Spec.Template.Spec.Organization}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.HorizontalRunnerAutoscaler{}).
		Named(name).
		Complete(autoscaler)
}
