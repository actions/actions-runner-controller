/*
Copyright 2021 The actions-runner-controller authors.

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
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.summerwind.net/metrics"
	"github.com/go-logr/logr"
)

// RunnerSetReconciler reconciles a Runner object
type RunnerSetReconciler struct {
	Name string

	client.Client
	Log      logr.Logger
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme

	CommonRunnerLabels []string
	GitHubClient       *MultiGitHubClient

	RunnerPodDefaults RunnerPodDefaults
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;create;update

// Note that coordination.k8s.io/leases permission must be added to any of the controllers to avoid the following error:
//   E0613 07:02:08.004278       1 leaderelection.go:325] error retrieving resource lock actions-runner-system/actions-runner-controller: leases.coordination.k8s.io "actions-runner-controller" is forbidden: User "system:serviceaccount:actions-runner-system:actions-runner-controller" cannot get resource "leases" in API group "coordination.k8s.io" in the namespace "actions-runner-system"

func (r *RunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runnerset", req.NamespacedName)

	runnerSet := &v1alpha1.RunnerSet{}
	if err := r.Get(ctx, req.NamespacedName, runnerSet); err != nil {
		err = client.IgnoreNotFound(err)

		if err != nil {
			log.Error(err, "Could not get RunnerSet")
		}

		return ctrl.Result{}, err
	}

	if !runnerSet.ObjectMeta.DeletionTimestamp.IsZero() {
		r.GitHubClient.DeinitForRunnerSet(runnerSet)

		return ctrl.Result{}, nil
	}

	metrics.SetRunnerSet(*runnerSet)

	var statefulsetList appsv1.StatefulSetList
	if err := r.List(ctx, &statefulsetList, client.InNamespace(req.Namespace), client.MatchingFields{runnerSetOwnerKey: req.Name}); err != nil {
		return ctrl.Result{}, err
	}

	statefulsets := statefulsetList.Items

	if len(statefulsets) > 1000 {
		log.Info("Postponed reconcilation to prevent potential infinite loop. If you're really scaling more than 1000 statefulsets, do change this hard-coded threshold!")
		return ctrl.Result{}, nil
	}

	desiredStatefulSet, err := r.newStatefulSet(ctx, runnerSet)
	if err != nil {
		r.Recorder.Event(runnerSet, corev1.EventTypeNormal, "RunnerAutoscalingFailure", err.Error())

		log.Error(err, "Could not create statefulset")

		return ctrl.Result{}, err
	}

	addedReplicas := int32(1)
	create := desiredStatefulSet.DeepCopy()
	create.Spec.Replicas = &addedReplicas

	const defaultReplicas = 1

	var replicasOfDesiredStatefulSet *int
	if desiredStatefulSet.Spec.Replicas != nil {
		v := int(*desiredStatefulSet.Spec.Replicas)
		replicasOfDesiredStatefulSet = &v
	}

	newDesiredReplicas := getIntOrDefault(replicasOfDesiredStatefulSet, defaultReplicas)

	effectiveTime := runnerSet.Spec.EffectiveTime
	ephemeral := runnerSet.Spec.Ephemeral == nil || *runnerSet.Spec.Ephemeral

	var owners []client.Object

	for _, ss := range statefulsets {
		ss := ss
		owners = append(owners, &ss)
	}

	if res, err := syncVolumes(ctx, r.Client, log, req.Namespace, runnerSet, statefulsets); err != nil {
		return ctrl.Result{}, err
	} else if res != nil {
		return *res, nil
	}

	res, err := syncRunnerPodsOwners(ctx, r.Client, log, effectiveTime, newDesiredReplicas, func() client.Object { return create.DeepCopy() }, ephemeral, owners)
	if err != nil || res == nil {
		return ctrl.Result{}, err
	}

	var statusReplicas, statusReadyReplicas, totalCurrentReplicas, updatedReplicas int

	for _, ss := range res.currentObjects {
		statusReplicas += int(ss.statefulSet.Status.Replicas)
		statusReadyReplicas += int(ss.statefulSet.Status.ReadyReplicas)
		totalCurrentReplicas += int(ss.statefulSet.Status.CurrentReplicas)
		updatedReplicas += int(ss.statefulSet.Status.UpdatedReplicas)
	}

	status := runnerSet.Status.DeepCopy()

	status.CurrentReplicas = &totalCurrentReplicas
	status.ReadyReplicas = &statusReadyReplicas
	status.DesiredReplicas = &newDesiredReplicas
	status.Replicas = &statusReplicas
	status.UpdatedReplicas = &updatedReplicas
	selector, err := metav1.LabelSelectorAsSelector(runnerSet.Spec.Selector)
	if err != nil {
		log.Error(err, "Failed to retrieve pod labels")

		return ctrl.Result{}, err
	}
	status.Selector = selector.String()

	if !reflect.DeepEqual(runnerSet.Status, status) {
		updated := runnerSet.DeepCopy()
		updated.Status = *status

		if err := r.Status().Patch(ctx, updated, client.MergeFrom(runnerSet)); err != nil {
			log.Info("Failed to patch runnerset status. Retrying immediately", "error", err.Error())
			return ctrl.Result{
				Requeue: true,
			}, nil
		}
	}

	return ctrl.Result{}, nil
}

func getRunnerSetSelector(runnerSet *v1alpha1.RunnerSet) *metav1.LabelSelector {
	selector := runnerSet.Spec.Selector
	if selector == nil {
		selector = &metav1.LabelSelector{MatchLabels: map[string]string{LabelKeyRunnerSetName: runnerSet.Name}}
	}

	return selector
}

var LabelKeyPodMutation = "actions-runner-controller/inject-registration-token"
var LabelValuePodMutation = "true"

func (r *RunnerSetReconciler) newStatefulSet(ctx context.Context, runnerSet *v1alpha1.RunnerSet) (*appsv1.StatefulSet, error) {
	runnerSetWithOverrides := *runnerSet.Spec.DeepCopy()

	runnerSetWithOverrides.Labels = append(runnerSetWithOverrides.Labels, r.CommonRunnerLabels...)

	template := corev1.Pod{
		ObjectMeta: runnerSetWithOverrides.StatefulSetSpec.Template.ObjectMeta,
		Spec:       runnerSetWithOverrides.StatefulSetSpec.Template.Spec,
	}

	if runnerSet.Spec.RunnerConfig.ContainerMode == "kubernetes" {
		found := false
		for i := range template.Spec.Containers {
			if template.Spec.Containers[i].Name == containerName {
				found = true
			}
		}
		if !found {
			template.Spec.Containers = append(template.Spec.Containers, corev1.Container{
				Name: "runner",
			})
		}

		workDir := runnerSet.Spec.RunnerConfig.WorkDir
		if workDir == "" {
			workDir = "/runner/_work"
		}
		if err := applyWorkVolumeClaimTemplateToPod(&template, runnerSet.Spec.WorkVolumeClaimTemplate, workDir); err != nil {
			return nil, err
		}

		template.Spec.ServiceAccountName = runnerSet.Spec.ServiceAccountName
	}

	template.ObjectMeta.Labels = CloneAndAddLabel(template.ObjectMeta.Labels, LabelKeyRunnerSetName, runnerSet.Name)

	ghc, err := r.GitHubClient.InitForRunnerSet(ctx, runnerSet)
	if err != nil {
		return nil, err
	}

	githubBaseURL := ghc.GithubBaseURL

	pod, err := newRunnerPodWithContainerMode(runnerSet.Spec.RunnerConfig.ContainerMode, template, runnerSet.Spec.RunnerConfig, githubBaseURL, r.RunnerPodDefaults)
	if err != nil {
		return nil, err
	}

	runnerSetWithOverrides.StatefulSetSpec.Template.ObjectMeta = pod.ObjectMeta
	runnerSetWithOverrides.StatefulSetSpec.Template.Spec = pod.Spec
	// NOTE: Seems like the only supported restart policy for statefulset is "Always"?
	// I got errosr like the below when tried to use "OnFailure":
	//   StatefulSet.apps \"example-runnersetpg9rx\" is invalid: [spec.template.metadata.labels: Invalid value: map[string]string{\"runner-template-hash\"
	//   :\"85d7578bd6\", \"runnerset-name\":\"example-runnerset\"}: `selector` does not match template `labels`, spec.
	//   template.spec.restartPolicy: Unsupported value: \"OnFailure\": supported values: \"Always\"]
	runnerSetWithOverrides.StatefulSetSpec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways

	templateHash := ComputeHash(pod.Spec)

	// Add template hash label to selector.
	runnerSetWithOverrides.Template.ObjectMeta.Labels = CloneAndAddLabel(runnerSetWithOverrides.Template.ObjectMeta.Labels, LabelKeyRunnerTemplateHash, templateHash)

	selector := getRunnerSetSelector(runnerSet)
	selector = CloneSelectorAndAddLabel(selector, LabelKeyRunnerTemplateHash, templateHash)
	selector = CloneSelectorAndAddLabel(selector, LabelKeyRunnerSetName, runnerSet.Name)
	selector = CloneSelectorAndAddLabel(selector, LabelKeyPodMutation, LabelValuePodMutation)

	runnerSetWithOverrides.StatefulSetSpec.Selector = selector

	rs := appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: runnerSet.ObjectMeta.Name + "-",
			Namespace:    runnerSet.ObjectMeta.Namespace,
			Labels:       CloneAndAddLabel(runnerSet.ObjectMeta.Labels, LabelKeyRunnerTemplateHash, templateHash),
			Annotations: map[string]string{
				SyncTimeAnnotationKey: time.Now().Format(time.RFC3339),
			},
		},
		Spec: runnerSetWithOverrides.StatefulSetSpec,
	}

	if err := ctrl.SetControllerReference(runnerSet, &rs, r.Scheme); err != nil {
		return &rs, err
	}

	return &rs, nil
}

func (r *RunnerSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "runnerset-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &appsv1.StatefulSet{}, runnerSetOwnerKey, func(rawObj client.Object) []string {
		set := rawObj.(*appsv1.StatefulSet)
		owner := metav1.GetControllerOf(set)
		if owner == nil {
			return nil
		}

		if owner.APIVersion != v1alpha1.GroupVersion.String() || owner.Kind != "RunnerSet" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RunnerSet{}).
		Owns(&appsv1.StatefulSet{}).
		Named(name).
		Complete(r)
}
