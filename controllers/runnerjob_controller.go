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

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RunnerJobReconciler reconciles a RunnerJob object
type RunnerJobReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerjobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerjobs/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RunnerJob object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.6.4/pkg/reconcile
func (r *RunnerJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runnerjob", req.NamespacedName)

	var runnerJob v1alpha1.RunnerJob
	if err := r.Get(ctx, req.NamespacedName, &runnerJob); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !runnerJob.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
			Name:        fmt.Sprintf("%s-runnerjob", runnerJob.Name), // TODO: how to name it?
			Namespace:   runnerJob.Namespace,
		},
		Spec: *runnerJob.Spec.JobSpec.DeepCopy(),
	}

	if err := ctrl.SetControllerReference(&runnerJob, job, r.Scheme); err != nil {
		log.Info("failed to set controller reference", "error", err.Error())
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, job); err != nil {
		log.Info("failed to create job", "error", err.Error())
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RunnerJob{}).
		Complete(r)
}
