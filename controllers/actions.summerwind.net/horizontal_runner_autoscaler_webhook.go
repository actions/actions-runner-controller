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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v52/github"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/github"
	"github.com/actions/actions-runner-controller/simulator"
)

const (
	scaleTargetKey = "scaleTarget"

	keyPrefixEnterprise = "enterprises/"
	keyRunnerGroup      = "/group/"

	DefaultQueueLimit = 100
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

	// GitHub Client to discover runner groups assigned to a repository
	GitHubClient *github.Client

	// Namespace is the namespace to watch for HorizontalRunnerAutoscaler's to be
	// scaled on Webhook.
	// Set to empty for letting it watch for all namespaces.
	Namespace string
	Name      string

	// QueueLimit is the maximum length of the bounded queue of scale targets and their associated operations
	// A scale target is enqueued on each retrieval of each eligible webhook event, so that it is processed asynchronously.
	QueueLimit int

	worker     *worker
	workerInit sync.Once
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
					autoscaler.Log.V(1).Error(err, "failed writing http error response", "msg", msg, "written", written)
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
	if strings.ToUpper(r.Method) == http.MethodGet {
		ok = true
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
		payload, err = io.ReadAll(r.Body)
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

	var enterpriseEvent struct {
		Enterprise struct {
			Slug string `json:"slug,omitempty"`
		} `json:"enterprise,omitempty"`
	}
	if err := json.Unmarshal(payload, &enterpriseEvent); err != nil {
		var s string
		if payload != nil {
			s = string(payload)
		}
		autoscaler.Log.Error(err, "could not parse webhook payload for extracting enterprise slug", "webhookType", webhookType, "payload", s)
	}
	enterpriseSlug := enterpriseEvent.Enterprise.Slug

	switch e := event.(type) {
	case *gogithub.WorkflowJobEvent:
		if workflowJob := e.GetWorkflowJob(); workflowJob != nil {
			log = log.WithValues(
				"workflowJob.status", workflowJob.GetStatus(),
				"workflowJob.labels", workflowJob.Labels,
				"repository.name", e.Repo.GetName(),
				"repository.owner.login", e.Repo.Owner.GetLogin(),
				"repository.owner.type", e.Repo.Owner.GetType(),
				"enterprise.slug", enterpriseSlug,
				"action", e.GetAction(),
				"workflowJob.runID", e.WorkflowJob.GetRunID(),
				"workflowJob.ID", e.WorkflowJob.GetID(),
			)
		}

		labels := e.WorkflowJob.Labels

		switch action := e.GetAction(); action {
		case "queued", "completed":
			target, err = autoscaler.getJobScaleUpTargetForRepoOrOrg(
				context.TODO(),
				log,
				e.Repo.GetName(),
				e.Repo.Owner.GetLogin(),
				e.Repo.Owner.GetType(),
				enterpriseSlug,
				labels,
			)
			if target == nil {
				break
			}

			if e.GetAction() == "queued" {
				target.Amount = 1
				break
			} else if e.GetAction() == "completed" && e.GetWorkflowJob().GetConclusion() != "skipped" {
				// We want to filter out "completed" events sent by check runs.
				// See https://github.com/actions/actions-runner-controller/issues/2118
				// and https://github.com/actions/actions-runner-controller/pull/2119
				// But canceled events have runner_id == 0 and GetRunnerID() returns 0 when RunnerID == nil,
				// so we need to be more specific in filtering out the check runs.
				// See example check run completion at https://gist.github.com/nathanklick/268fea6496a4d7b14cecb2999747ef84
				// Check runs appear to have no labels set, so use that in conjuction with the nil RunnerID to filter the event:
				if len(e.GetWorkflowJob().Labels) == 0 && e.GetWorkflowJob().RunnerID == nil {
					log.V(1).Info("Ignoring workflow_job event because it does not relate to a self-hosted runner")
				} else {
					// A negative amount is processed in the tryScale func as a scale-down request,
					// that erases the oldest CapacityReservation with the same amount.
					// If the first CapacityReservation was with Replicas=1, this negative scale target erases that,
					// so that the resulting desired replicas decreases by 1.
					target.Amount = -1
					break
				}
			}
			// If the conclusion is "skipped", we will ignore it and fallthrough to the default case.
			fallthrough
		default:
			ok = true

			w.WriteHeader(http.StatusOK)

			log.V(2).Info("Received and ignored a workflow_job event as it triggers neither scale-up nor scale-down", "action", action)

			return
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
		log.V(1).Info(
			"Scale target not found. If this is unexpected, ensure that there is exactly one repository-wide or organizational runner deployment that matches this webhook event. If --watch-namespace is set ensure this is configured correctly.",
		)

		msg := "no horizontalrunnerautoscaler to scale for this github event"

		ok = true

		w.WriteHeader(http.StatusOK)

		if written, err := w.Write([]byte(msg)); err != nil {
			log.Error(err, "failed writing http response", "msg", msg, "written", written)
		}

		return
	}

	autoscaler.workerInit.Do(func() {
		batchScaler := newBatchScaler(context.Background(), autoscaler.Client, autoscaler.Log)

		queueLimit := autoscaler.QueueLimit
		if queueLimit == 0 {
			queueLimit = DefaultQueueLimit
		}
		autoscaler.worker = newWorker(context.Background(), queueLimit, batchScaler.Add)
	})

	target.log = &log
	if ok := autoscaler.worker.Add(target); !ok {
		log.Error(err, "Could not scale up due to queue full")
		return
	}

	ok = true

	w.WriteHeader(http.StatusOK)

	msg := fmt.Sprintf("scaled %s by %d", target.Name, target.Amount)

	log.Info(msg)

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

		hras = append(hras, hraList.Items...)
	}

	return hras, nil
}

type ScaleTarget struct {
	v1alpha1.HorizontalRunnerAutoscaler
	v1alpha1.ScaleUpTrigger

	log *logr.Logger
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) getJobScaleUpTargetForRepoOrOrg(
	ctx context.Context, log logr.Logger, repo, owner, ownerType, enterprise string, labels []string,
) (*ScaleTarget, error) {

	scaleTarget := func(value string) (*ScaleTarget, error) {
		return autoscaler.getJobScaleTarget(ctx, value, labels)
	}
	return autoscaler.getScaleUpTargetWithFunction(ctx, log, repo, owner, ownerType, enterprise, scaleTarget)
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) getScaleUpTargetWithFunction(
	ctx context.Context, log logr.Logger, repo, owner, ownerType, enterprise string, scaleTarget func(value string) (*ScaleTarget, error)) (*ScaleTarget, error) {

	repositoryRunnerKey := owner + "/" + repo

	// Search for repository HRAs
	if target, err := scaleTarget(repositoryRunnerKey); err != nil {
		log.Error(err, "finding repository-wide runner", "repository", repositoryRunnerKey)
		return nil, err
	} else if target != nil {
		log.Info("job scale up target is repository-wide runners", "repository", repo)
		return target, nil
	}

	if ownerType == "User" {
		log.V(1).Info("user repositories not supported", "owner", owner)
		return nil, nil
	}

	// Find the potential runner groups first to avoid spending API queries needless. Once/if GitHub improves an
	// API to find related/linked runner groups from a specific repository this logic could be removed
	managedRunnerGroups, err := autoscaler.getManagedRunnerGroupsFromHRAs(ctx, enterprise, owner)
	if err != nil {
		log.Error(err, "finding potential organization/enterprise runner groups from HRAs", "organization", owner)
		return nil, err
	}
	if managedRunnerGroups.IsEmpty() {
		log.V(1).Info("no repository/organizational/enterprise runner found",
			"repository", repositoryRunnerKey,
			"organization", owner,
			"enterprises", enterprise,
		)
	} else {
		log.V(1).Info("Found some runner groups are managed by ARC", "groups", managedRunnerGroups)
	}

	var visibleGroups *simulator.VisibleRunnerGroups
	if autoscaler.GitHubClient != nil {
		simu := &simulator.Simulator{
			Client: autoscaler.GitHubClient,
			Log:    log,
		}
		// Get available organization runner groups and enterprise runner groups for a repository
		// These are the sum of runner groups with repository access = All repositories and runner groups
		// where owner/repo has access to as well. The list will include default runner group also if it has access to
		visibleGroups, err = simu.GetRunnerGroupsVisibleToRepository(ctx, owner, repositoryRunnerKey, managedRunnerGroups)
		log.V(1).Info("Searching in runner groups", "groups", visibleGroups)
		if err != nil {
			log.Error(err, "Unable to find runner groups from repository", "organization", owner, "repository", repo)
			return nil, fmt.Errorf("error while finding visible runner groups: %v", err)
		}
	} else {
		// For backwards compatibility if GitHub authentication is not configured, we assume all runner groups have
		// visibility=all to honor the previous implementation, therefore any available enterprise/organization runner
		// is a potential target for scaling. This will also avoid doing extra API calls caused by
		// GitHubClient.GetRunnerGroupsVisibleToRepository in case users are not using custom visibility on their runner
		// groups or they are using only default runner groups
		visibleGroups = managedRunnerGroups
	}

	scaleTargetKey := func(rg simulator.RunnerGroup) string {
		switch rg.Kind {
		case simulator.Default:
			switch rg.Scope {
			case simulator.Organization:
				return owner
			case simulator.Enterprise:
				return enterpriseKey(enterprise)
			}
		case simulator.Custom:
			switch rg.Scope {
			case simulator.Organization:
				return organizationalRunnerGroupKey(owner, rg.Name)
			case simulator.Enterprise:
				return enterpriseRunnerGroupKey(enterprise, rg.Name)
			}
		}
		return ""
	}

	log.V(1).Info("groups", "groups", visibleGroups)

	var t *ScaleTarget

	traverseErr := visibleGroups.Traverse(func(rg simulator.RunnerGroup) (bool, error) {
		key := scaleTargetKey(rg)

		target, err := scaleTarget(key)

		if err != nil {
			log.Error(err, "finding runner group", "enterprise", enterprise, "organization", owner, "repository", repo, "key", key)
			return false, err
		} else if target == nil {
			return false, nil
		}

		t = target
		log.V(1).Info("job scale up target found", "enterprise", enterprise, "organization", owner, "repository", repo, "key", key)

		return true, nil
	})

	if traverseErr != nil {
		return nil, err
	}

	if t == nil {
		log.V(1).Info("no repository/organizational/enterprise runner found",
			"repository", repositoryRunnerKey,
			"organization", owner,
			"enterprise", enterprise,
		)
	}

	return t, nil
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) getManagedRunnerGroupsFromHRAs(ctx context.Context, enterprise, org string) (*simulator.VisibleRunnerGroups, error) {
	groups := simulator.NewVisibleRunnerGroups()
	ns := autoscaler.Namespace

	var defaultListOpts []client.ListOption
	if ns != "" {
		defaultListOpts = append(defaultListOpts, client.InNamespace(ns))
	}

	opts := append([]client.ListOption{}, defaultListOpts...)
	if autoscaler.Namespace != "" {
		opts = append(opts, client.InNamespace(autoscaler.Namespace))
	}

	var hraList v1alpha1.HorizontalRunnerAutoscalerList
	if err := autoscaler.List(ctx, &hraList, opts...); err != nil {
		return groups, err
	}

	for _, hra := range hraList.Items {
		var o, e, g string

		kind := hra.Spec.ScaleTargetRef.Kind
		switch kind {
		case "RunnerSet":
			var rs v1alpha1.RunnerSet
			if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rs); err != nil {
				return groups, err
			}
			o, e, g = rs.Spec.Organization, rs.Spec.Enterprise, rs.Spec.Group
		case "RunnerDeployment", "":
			var rd v1alpha1.RunnerDeployment
			if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rd); err != nil {
				return groups, err
			}
			o, e, g = rd.Spec.Template.Spec.Organization, rd.Spec.Template.Spec.Enterprise, rd.Spec.Template.Spec.Group
		default:
			return nil, fmt.Errorf("unsupported scale target kind: %v", kind)
		}

		if g != "" && e == "" && o == "" {
			autoscaler.Log.V(1).Info(
				"invalid runner group config in scale target: spec.group must be set along with either spec.enterprise or spec.organization",
				"scaleTargetKind", kind,
				"group", g,
				"enterprise", e,
				"organization", o,
			)

			continue
		}

		if e != enterprise && o != org {
			autoscaler.Log.V(1).Info(
				"Skipped scale target irrelevant to event",
				"eventOrganization", org,
				"eventEnterprise", enterprise,
				"scaleTargetKind", kind,
				"scaleTargetGroup", g,
				"scaleTargetEnterprise", e,
				"scaleTargetOrganization", o,
			)

			continue
		}

		rg := simulator.NewRunnerGroupFromProperties(e, o, g)

		if err := groups.Add(rg); err != nil {
			return groups, fmt.Errorf("failed adding visible group from HRA %s/%s: %w", hra.Namespace, hra.Name, err)
		}
	}
	return groups, nil
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) getJobScaleTarget(ctx context.Context, name string, labels []string) (*ScaleTarget, error) {
	hras, err := autoscaler.findHRAsByKey(ctx, name)
	if err != nil {
		return nil, err
	}

	autoscaler.Log.V(1).Info(fmt.Sprintf("Found %d HRAs by key", len(hras)), "key", name)

HRA:
	for _, hra := range hras {
		if !hra.ObjectMeta.DeletionTimestamp.IsZero() {
			continue
		}

		if len(hra.Spec.ScaleUpTriggers) > 1 {
			autoscaler.Log.V(1).Info("Skipping this HRA as it has too many ScaleUpTriggers to be used in workflow_job based scaling", "hra", hra.Name)
			continue
		}

		if len(hra.Spec.ScaleUpTriggers) == 0 {
			autoscaler.Log.V(1).Info("Skipping this HRA as it has no ScaleUpTriggers configured", "hra", hra.Name)
			continue
		}

		scaleUpTrigger := hra.Spec.ScaleUpTriggers[0]

		if scaleUpTrigger.GitHubEvent == nil {
			autoscaler.Log.V(1).Info("Skipping this HRA as it has no `githubEvent` scale trigger configured", "hra", hra.Name)

			continue
		}

		if scaleUpTrigger.GitHubEvent.WorkflowJob == nil {
			autoscaler.Log.V(1).Info("Skipping this HRA as it has no `githubEvent.workflowJob` scale trigger configured", "hra", hra.Name)

			continue
		}

		duration := scaleUpTrigger.Duration
		if duration.Duration <= 0 {
			// Try to release the reserved capacity after at least 10 minutes by default,
			// we won't end up in the reserved capacity remained forever in case GitHub somehow stopped sending us "completed" workflow_job events.
			// GitHub usually send us those but nothing is 100% guaranteed, e.g. in case of something went wrong on GitHub :)
			// Probably we'd better make this configurable via custom resources in the future?
			duration.Duration = 10 * time.Minute
		}

		switch hra.Spec.ScaleTargetRef.Kind {
		case "RunnerSet":
			var rs v1alpha1.RunnerSet

			if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rs); err != nil {
				return nil, err
			}

			// Ensure that the RunnerSet-managed runners have all the labels requested by the workflow_job.
			for _, l := range labels {
				var matched bool

				// ignore "self-hosted" label as all instance here are self-hosted
				if l == "self-hosted" {
					continue
				}

				// TODO labels related to OS and architecture needs to be explicitly declared or the current implementation will not be able to find them.

				for _, l2 := range rs.Spec.Labels {
					if strings.EqualFold(l, l2) {
						matched = true
						break
					}
				}

				if !matched {
					continue HRA
				}
			}

			return &ScaleTarget{HorizontalRunnerAutoscaler: hra, ScaleUpTrigger: v1alpha1.ScaleUpTrigger{Duration: duration}}, nil
		case "RunnerDeployment", "":
			var rd v1alpha1.RunnerDeployment

			if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rd); err != nil {
				return nil, err
			}

			// Ensure that the RunnerDeployment-managed runners have all the labels requested by the workflow_job.
			for _, l := range labels {
				var matched bool

				// ignore "self-hosted" label as all instance here are self-hosted
				if l == "self-hosted" {
					continue
				}

				// TODO labels related to OS and architecture needs to be explicitly declared or the current implementation will not be able to find them.

				for _, l2 := range rd.Spec.Template.Spec.Labels {
					if strings.EqualFold(l, l2) {
						matched = true
						break
					}
				}

				if !matched {
					continue HRA
				}
			}

			return &ScaleTarget{HorizontalRunnerAutoscaler: hra, ScaleUpTrigger: v1alpha1.ScaleUpTrigger{Duration: duration}}, nil
		default:
			return nil, fmt.Errorf("unsupported scaleTargetRef.kind: %v", hra.Spec.ScaleTargetRef.Kind)
		}
	}

	return nil, nil
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

	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.HorizontalRunnerAutoscaler{}, scaleTargetKey, autoscaler.indexer); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.HorizontalRunnerAutoscaler{}).
		Named(name).
		Complete(autoscaler)
}

func (autoscaler *HorizontalRunnerAutoscalerGitHubWebhook) indexer(rawObj client.Object) []string {
	hra := rawObj.(*v1alpha1.HorizontalRunnerAutoscaler)

	if hra.Spec.ScaleTargetRef.Name == "" {
		autoscaler.Log.V(1).Info(fmt.Sprintf("scale target ref name not set for hra %s", hra.Name))
		return nil
	}

	switch hra.Spec.ScaleTargetRef.Kind {
	case "", "RunnerDeployment":
		var rd v1alpha1.RunnerDeployment
		if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rd); err != nil {
			autoscaler.Log.V(1).Info(fmt.Sprintf("RunnerDeployment not found with scale target ref name %s for hra %s", hra.Spec.ScaleTargetRef.Name, hra.Name))
			return nil
		}

		keys := []string{}
		if rd.Spec.Template.Spec.Repository != "" {
			keys = append(keys, rd.Spec.Template.Spec.Repository) // Repository runners
		}
		if rd.Spec.Template.Spec.Organization != "" {
			if group := rd.Spec.Template.Spec.Group; group != "" {
				keys = append(keys, organizationalRunnerGroupKey(rd.Spec.Template.Spec.Organization, rd.Spec.Template.Spec.Group)) // Organization runner groups
			} else {
				keys = append(keys, rd.Spec.Template.Spec.Organization) // Organization runners
			}
		}
		if enterprise := rd.Spec.Template.Spec.Enterprise; enterprise != "" {
			if group := rd.Spec.Template.Spec.Group; group != "" {
				keys = append(keys, enterpriseRunnerGroupKey(enterprise, rd.Spec.Template.Spec.Group)) // Enterprise runner groups
			} else {
				keys = append(keys, enterpriseKey(enterprise)) // Enterprise runners
			}
		}
		autoscaler.Log.V(2).Info(fmt.Sprintf("HRA keys indexed for HRA %s: %v", hra.Name, keys))
		return keys
	case "RunnerSet":
		var rs v1alpha1.RunnerSet
		if err := autoscaler.Client.Get(context.Background(), types.NamespacedName{Namespace: hra.Namespace, Name: hra.Spec.ScaleTargetRef.Name}, &rs); err != nil {
			autoscaler.Log.V(1).Info(fmt.Sprintf("RunnerSet not found with scale target ref name %s for hra %s", hra.Spec.ScaleTargetRef.Name, hra.Name))
			return nil
		}

		keys := []string{}
		if rs.Spec.Repository != "" {
			keys = append(keys, rs.Spec.Repository) // Repository runners
		}
		if rs.Spec.Organization != "" {
			keys = append(keys, rs.Spec.Organization) // Organization runners
			if group := rs.Spec.Group; group != "" {
				keys = append(keys, organizationalRunnerGroupKey(rs.Spec.Organization, rs.Spec.Group)) // Organization runner groups
			}
		}
		if enterprise := rs.Spec.Enterprise; enterprise != "" {
			keys = append(keys, enterpriseKey(enterprise)) // Enterprise runners
			if group := rs.Spec.Group; group != "" {
				keys = append(keys, enterpriseRunnerGroupKey(enterprise, rs.Spec.Group)) // Enterprise runner groups
			}
		}
		autoscaler.Log.V(2).Info(fmt.Sprintf("HRA keys indexed for HRA %s: %v", hra.Name, keys))
		return keys
	}

	return nil
}

func enterpriseKey(name string) string {
	return keyPrefixEnterprise + name
}

func organizationalRunnerGroupKey(owner, group string) string {
	return owner + keyRunnerGroup + group
}

func enterpriseRunnerGroupKey(enterprise, group string) string {
	return keyPrefixEnterprise + enterprise + keyRunnerGroup + group
}
