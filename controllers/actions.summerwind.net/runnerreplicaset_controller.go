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
	"reflect"
	"time"

	"github.com/go-logr/logr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
)

// RunnerReplicaSetReconciler reconciles a Runner object
type RunnerReplicaSetReconciler struct {
	client.Client
	Log      logr.Logger
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
	Name     string
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

	if !rs.DeletionTimestamp.IsZero() {
		// RunnerReplicaSet cannot be gracefuly removed.
		// That means any runner that is running a job can be prematurely terminated.
		// To gracefully remove a RunnerReplicaSet, scale it down to zero first, observe RunnerReplicaSet's status replicas,
		// and remove it only after the status replicas becomes zero.
		return ctrl.Result{}, nil
	}

	if rs.Labels == nil {
		rs.Labels = map[string]string{}
	}

	// Template hash is usually set by the upstream controller(RunnerDeplloyment controller) on authoring
	// RunerReplicaset resource, but it may be missing when the user directly created RunnerReplicaSet.
	// As a template hash is required by by the runner replica management, we dynamically add it here without ever persisting it.
	if rs.Labels[LabelKeyRunnerTemplateHash] == "" {
		template := rs.Spec.DeepCopy()
		template.Replicas = nil
		template.EffectiveTime = nil
		templateHash := ComputeHash(template)

		log.Info("Using auto-generated template hash", "value", templateHash)

		rs.Labels = CloneAndAddLabel(rs.Labels, LabelKeyRunnerTemplateHash, templateHash)
		rs.Spec.Template.Labels = CloneAndAddLabel(rs.Spec.Template.Labels, LabelKeyRunnerTemplateHash, templateHash)
	}

	selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Get the Runners managed by the target RunnerReplicaSet
	var runnerList v1alpha1.RunnerList
	if err := r.List(
		ctx,
		&runnerList,
		client.InNamespace(req.Namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	replicas := 1
	if rs.Spec.Replicas != nil {
		replicas = *rs.Spec.Replicas
	}

	effectiveTime := rs.Spec.EffectiveTime
	ephemeral := rs.Spec.Template.Spec.Ephemeral == nil || *rs.Spec.Template.Spec.Ephemeral

	desired, err := r.newRunner(rs)
	if err != nil {
		log.Error(err, "Could not create runner")

		return ctrl.Result{}, err
	}

	var live []client.Object
	for _, r := range runnerList.Items {
		r := r
		live = append(live, &r)
	}

	res, err := syncRunnerPodsOwners(ctx, r.Client, log, effectiveTime, replicas, func() client.Object { return desired.DeepCopy() }, ephemeral, live)
	if err != nil || res == nil {
		return ctrl.Result{}, err
	}

	var (
		status v1alpha1.RunnerReplicaSetStatus

		current, available, ready int
	)

	for _, o := range res.currentObjects {
		current += o.total
		available += o.running
		ready += o.running
	}

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
	// Note that the upstream controller (runnerdeployment) is expected to add
	// the "runner template hash" label to the template.meta which is necessary to make this controller work correctly
	objectMeta := rs.Spec.Template.ObjectMeta.DeepCopy()

	objectMeta.GenerateName = rs.Name + "-"
	objectMeta.Namespace = rs.Namespace
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
