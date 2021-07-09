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

package controllers

import (
	"context"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/controllers/metrics"
)

const (
	LabelKeyRunnerSetName = "runnerset-name"
)

// RunnerSetReconciler reconciles a Runner object
type RunnerSetReconciler struct {
	Name string

	client.Client
	Log      logr.Logger
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme

	CommonRunnerLabels   []string
	GitHubBaseURL        string
	RunnerImage          string
	DockerImage          string
	DockerRegistryMirror string
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get;update;patch
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
		return ctrl.Result{}, nil
	}

	metrics.SetRunnerSet(*runnerSet)

	desiredStatefulSet, err := r.newStatefulSet(runnerSet)
	if err != nil {
		r.Recorder.Event(runnerSet, corev1.EventTypeNormal, "RunnerAutoscalingFailure", err.Error())

		log.Error(err, "Could not create statefulset")

		return ctrl.Result{}, err
	}

	liveStatefulSet := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: runnerSet.Namespace, Name: runnerSet.Name}, liveStatefulSet); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get live statefulset")

			return ctrl.Result{}, err
		}

		if err := r.Client.Create(ctx, desiredStatefulSet); err != nil {
			log.Error(err, "Failed to create statefulset resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	liveTemplateHash, ok := getStatefulSetTemplateHash(liveStatefulSet)
	if !ok {
		log.Info("Failed to get template hash of newest statefulset resource. It must be in an invalid state. Please manually delete the statefulset so that it is recreated")

		return ctrl.Result{}, nil
	}

	desiredTemplateHash, ok := getStatefulSetTemplateHash(desiredStatefulSet)
	if !ok {
		log.Info("Failed to get template hash of desired statefulset. It must be in an invalid state. Please manually delete the statefulset so that it is recreated")

		return ctrl.Result{}, nil
	}

	if liveTemplateHash != desiredTemplateHash {
		copy := liveStatefulSet.DeepCopy()
		copy.Spec = desiredStatefulSet.Spec

		if err := r.Client.Patch(ctx, copy, client.MergeFrom(liveStatefulSet)); err != nil {
			log.Error(err, "Failed to patch statefulset", "reason", errors.ReasonForError(err))

			if errors.IsInvalid(err) {
				// NOTE: This might not be ideal but deal the forbidden error by recreating the statefulset
				// Probably we'd better create a registration-only runner to prevent queued jobs from immediately failing.
				//
				// 2021-06-13T07:19:52.760Z        ERROR   actions-runner-controller.runnerset     Failed to patch statefulset
				// {"runnerset": "default/example-runnerset", "error": "StatefulSet.apps \"example-runnerset\" is invalid: s
				// pec: Forbidden: updates to statefulset spec for fields other than 'replicas', 'template', and 'updateStrategy'
				// are forbidden"}
				//
				// Even though the error message includes "Forbidden", this error's reason is "Invalid".
				// That's why we're using errors.IsInvalid above.

				if err := r.Client.Delete(ctx, liveStatefulSet); err != nil {
					log.Error(err, "Failed to delete statefulset for force-update")
					return ctrl.Result{}, err
				}
				log.Info("Deleted statefulset for force-update")
			}

			return ctrl.Result{}, err
		}

		// We requeue in order to clean up old runner replica sets later.
		// Otherwise, they aren't cleaned up until the next re-sync interval.
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	const defaultReplicas = 1

	var replicasOfLiveStatefulSet *int
	if liveStatefulSet.Spec.Replicas != nil {
		v := int(*liveStatefulSet.Spec.Replicas)
		replicasOfLiveStatefulSet = &v
	}

	var replicasOfDesiredStatefulSet *int
	if desiredStatefulSet.Spec.Replicas != nil {
		v := int(*desiredStatefulSet.Spec.Replicas)
		replicasOfDesiredStatefulSet = &v
	}

	currentDesiredReplicas := getIntOrDefault(replicasOfLiveStatefulSet, defaultReplicas)
	newDesiredReplicas := getIntOrDefault(replicasOfDesiredStatefulSet, defaultReplicas)

	// Please add more conditions that we can in-place update the newest runnerreplicaset without disruption
	if currentDesiredReplicas != newDesiredReplicas {
		v := int32(newDesiredReplicas)

		updated := liveStatefulSet.DeepCopy()
		updated.Spec.Replicas = &v

		if err := r.Client.Patch(ctx, updated, client.MergeFrom(liveStatefulSet)); err != nil {
			log.Error(err, "Failed to update statefulset")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	statusReplicas := int(liveStatefulSet.Status.Replicas)
	statusReadyReplicas := int(liveStatefulSet.Status.ReadyReplicas)
	totalCurrentReplicas := int(liveStatefulSet.Status.CurrentReplicas)
	updatedReplicas := int(liveStatefulSet.Status.UpdatedReplicas)

	status := runnerSet.Status.DeepCopy()

	status.CurrentReplicas = &totalCurrentReplicas
	status.ReadyReplicas = &statusReadyReplicas
	status.DesiredReplicas = &newDesiredReplicas
	status.Replicas = &statusReplicas
	status.UpdatedReplicas = &updatedReplicas

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

func getStatefulSetTemplateHash(rs *appsv1.StatefulSet) (string, bool) {
	hash, ok := rs.Labels[LabelKeyRunnerTemplateHash]

	return hash, ok
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

func (r *RunnerSetReconciler) newStatefulSet(runnerSet *v1alpha1.RunnerSet) (*appsv1.StatefulSet, error) {
	runnerSetWithOverrides := *runnerSet.Spec.DeepCopy()

	for _, l := range r.CommonRunnerLabels {
		runnerSetWithOverrides.Labels = append(runnerSetWithOverrides.Labels, l)
	}

	// This label selector is used by default when rd.Spec.Selector is empty.
	runnerSetWithOverrides.Template.ObjectMeta.Labels = CloneAndAddLabel(runnerSetWithOverrides.Template.ObjectMeta.Labels, LabelKeyRunnerSetName, runnerSet.Name)

	runnerSetWithOverrides.Template.ObjectMeta.Labels = CloneAndAddLabel(runnerSetWithOverrides.Template.ObjectMeta.Labels, LabelKeyPodMutation, LabelValuePodMutation)

	template := corev1.Pod{
		ObjectMeta: runnerSetWithOverrides.StatefulSetSpec.Template.ObjectMeta,
		Spec:       runnerSetWithOverrides.StatefulSetSpec.Template.Spec,
	}

	pod, err := newRunnerPod(template, runnerSet.Spec.RunnerConfig, r.RunnerImage, r.DockerImage, r.DockerRegistryMirror, r.GitHubBaseURL, false)
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
			Name:      runnerSet.ObjectMeta.Name,
			Namespace: runnerSet.ObjectMeta.Namespace,
			Labels:    CloneAndAddLabel(runnerSet.ObjectMeta.Labels, LabelKeyRunnerTemplateHash, templateHash),
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RunnerSet{}).
		Owns(&appsv1.StatefulSet{}).
		Named(name).
		Complete(r)
}
