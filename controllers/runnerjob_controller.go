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
	"time"

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

const jobOwnerKey string = ".metadata.controller"

var apiGVStr = v1alpha1.GroupVersion.String()

//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerjobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerjobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get

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

	log.Info("reading the runner job", "RunnerJob", req.NamespacedName)
	defer log.Info("finishing reconciling")

	var runnerJob v1alpha1.RunnerJob
	if err := r.Get(ctx, req.NamespacedName, &runnerJob); err != nil {
		log.Info("resource could not be loaded", "error", err.Error())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !runnerJob.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var childJobs batchv1.JobList
	if err := r.List(ctx, &childJobs, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name}); err != nil {
		log.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}

	for _, childJob := range childJobs.Items {
		log.Info("Child Job", "Status", childJob.Status)
	}

	switch len(childJobs.Items) {
	case 0:
		log.Info("Create job")
		if err := r.createJob(ctx, runnerJob); err != nil {
			log.Info("Failed to create a job from a RunnerJob, requeueing in 30s", runnerJob.Name, err.Error())
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	case 1:
		log.Info("We have 1 child job")
		childJob := childJobs.Items[0]
		// if runner pod is running, don't reconcile again on this state
		if childJob.Status.Active > 0 {
			return ctrl.Result{}, nil
		}

		if childJob.Status.Succeeded > 0 {
			// cleanup runner job
			if err := r.Delete(ctx, &runnerJob); err != nil {
				log.Info("Failed to delete runner job", "RunnerJob", runnerJob.Name)
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, nil
		}

	default:
		log.V(0).Info("Found runner job with more than one kubernetes job", "RunnerJob", runnerJob.Name)
		return ctrl.Result{}, fmt.Errorf("Found runner job with more than one kubernetes job")
	}

	return ctrl.Result{}, nil
}

func (r *RunnerJobReconciler) createJob(ctx context.Context, runnerJob v1alpha1.RunnerJob) error {
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
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	if err := r.Create(ctx, job); err != nil {
		return fmt.Errorf("call to create failed: %w", err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &batchv1.Job{}, jobOwnerKey, func(rawObj client.Object) []string {
		// grab the job object, extract the owner...
		job := rawObj.(*batchv1.Job)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ...make sure it's a CronJob...
		if owner.APIVersion != apiGVStr || owner.Kind != "RunnerJob" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RunnerJob{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
