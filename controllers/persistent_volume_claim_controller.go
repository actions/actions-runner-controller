/*
Copyright 2022 The actions-runner-controller authors.

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

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

// RunnerPersistentVolumeClaimReconciler reconciles a PersistentVolume object
type RunnerPersistentVolumeClaimReconciler struct {
	client.Client
	Log      logr.Logger
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
	Name     string
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerPersistentVolumeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("pvc", req.NamespacedName)

	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	res, err := syncPVC(ctx, r.Client, log, req.Namespace, &pvc)

	if res == nil {
		res = &ctrl.Result{}
	}

	return *res, err
}

func (r *RunnerPersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "runnerpersistentvolumeclaim-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Named(name).
		Complete(r)
}
