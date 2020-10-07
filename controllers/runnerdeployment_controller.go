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
	"hash/fnv"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

const (
	LabelKeyRunnerTemplateHash = "runner-template-hash"

	runnerSetOwnerKey = ".metadata.controller"
)

// RunnerDeploymentReconciler reconciles a Runner object
type RunnerDeploymentReconciler struct {
	client.Client
	Log      logr.Logger
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerdeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerdeployments/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerdeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerreplicasets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerreplicasets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerDeploymentReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("runnerdeployment", req.NamespacedName)

	var rd v1alpha1.RunnerDeployment
	if err := r.Get(ctx, req.NamespacedName, &rd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rd.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var myRunnerReplicaSetList v1alpha1.RunnerReplicaSetList
	if err := r.List(ctx, &myRunnerReplicaSetList, client.InNamespace(req.Namespace), client.MatchingFields{runnerSetOwnerKey: req.Name}); err != nil {
		return ctrl.Result{}, err
	}

	myRunnerReplicaSets := myRunnerReplicaSetList.Items

	sort.Slice(myRunnerReplicaSets, func(i, j int) bool {
		return myRunnerReplicaSets[i].GetCreationTimestamp().After(myRunnerReplicaSets[j].GetCreationTimestamp().Time)
	})

	var newestSet *v1alpha1.RunnerReplicaSet

	var oldSets []v1alpha1.RunnerReplicaSet

	if len(myRunnerReplicaSets) > 0 {
		newestSet = &myRunnerReplicaSets[0]
	}

	if len(myRunnerReplicaSets) > 1 {
		oldSets = myRunnerReplicaSets[1:]
	}

	desiredRS, err := r.newRunnerReplicaSet(rd)
	if err != nil {
		r.Recorder.Event(&rd, corev1.EventTypeNormal, "RunnerAutoscalingFailure", err.Error())

		log.Error(err, "Could not create runnerreplicaset")

		return ctrl.Result{}, err
	}

	if newestSet == nil {
		if err := r.Client.Create(ctx, desiredRS); err != nil {
			log.Error(err, "Failed to create runnerreplicaset resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	newestTemplateHash, ok := getTemplateHash(newestSet)
	if !ok {
		log.Info("Failed to get template hash of newest runnerreplicaset resource. It must be in an invalid state. Please manually delete the runnerreplicaset so that it is recreated")

		return ctrl.Result{}, nil
	}

	desiredTemplateHash, ok := getTemplateHash(desiredRS)
	if !ok {
		log.Info("Failed to get template hash of desired runnerreplicaset resource. It must be in an invalid state. Please manually delete the runnerreplicaset so that it is recreated")

		return ctrl.Result{}, nil
	}

	if newestTemplateHash != desiredTemplateHash {
		if err := r.Client.Create(ctx, desiredRS); err != nil {
			log.Error(err, "Failed to create runnerreplicaset resource")

			return ctrl.Result{}, err
		}

		// We requeue in order to clean up old runner replica sets later.
		// Otherwise, they aren't cleaned up until the next re-sync interval.
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	const defaultReplicas = 1

	currentDesiredReplicas := getIntOrDefault(newestSet.Spec.Replicas, defaultReplicas)
	newDesiredReplicas := getIntOrDefault(desiredRS.Spec.Replicas, defaultReplicas)

	// Please add more conditions that we can in-place update the newest runnerreplicaset without disruption
	if currentDesiredReplicas != newDesiredReplicas {
		newestSet.Spec.Replicas = &newDesiredReplicas

		if err := r.Client.Update(ctx, newestSet); err != nil {
			log.Error(err, "Failed to update runnerreplicaset resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Do we old runner replica sets that should eventually deleted?
	if len(oldSets) > 0 {
		readyReplicas := newestSet.Status.ReadyReplicas

		if readyReplicas < currentDesiredReplicas {
			log.WithValues("runnerreplicaset", types.NamespacedName{
				Namespace: newestSet.Namespace,
				Name:      newestSet.Name,
			}).
				Info("Waiting until the newest runner replica set to be 100% available")

			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		for i := range oldSets {
			rs := oldSets[i]

			if err := r.Client.Delete(ctx, &rs); err != nil {
				log.Error(err, "Failed to delete runner resource")

				return ctrl.Result{}, err
			}

			r.Recorder.Event(&rd, corev1.EventTypeNormal, "RunnerReplicaSetDeleted", fmt.Sprintf("Deleted runnerreplicaset '%s'", rs.Name))

			log.Info("Deleted runnerreplicaset", "runnerdeployment", rd.ObjectMeta.Name, "runnerreplicaset", rs.Name)
		}
	}

	if rd.Spec.Replicas == nil && desiredRS.Spec.Replicas != nil {
		updated := rd.DeepCopy()
		updated.Status.Replicas = desiredRS.Spec.Replicas

		if err := r.Status().Update(ctx, updated); err != nil {
			log.Error(err, "Failed to update runnerdeployment status")

			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func getIntOrDefault(p *int, d int) int {
	if p == nil {
		return d
	}

	return *p
}

func getTemplateHash(rs *v1alpha1.RunnerReplicaSet) (string, bool) {
	hash, ok := rs.Labels[LabelKeyRunnerTemplateHash]

	return hash, ok
}

// ComputeHash returns a hash value calculated from pod template and
// a collisionCount to avoid hash collision. The hash will be safe encoded to
// avoid bad words.
//
// Proudly modified and adopted from k8s.io/kubernetes/pkg/util/hash.DeepHashObject and
// k8s.io/kubernetes/pkg/controller.ComputeHash.
func ComputeHash(template interface{}) string {
	hasher := fnv.New32a()

	hasher.Reset()

	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", template)

	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}

// Clones the given map and returns a new map with the given key and value added.
// Returns the given map, if labelKey is empty.
//
// Proudly copied from k8s.io/kubernetes/pkg/util/labels.CloneAndAddLabel
func CloneAndAddLabel(labels map[string]string, labelKey, labelValue string) map[string]string {
	if labelKey == "" {
		// Don't need to add a label.
		return labels
	}
	// Clone.
	newLabels := map[string]string{}
	for key, value := range labels {
		newLabels[key] = value
	}
	newLabels[labelKey] = labelValue
	return newLabels
}

func (r *RunnerDeploymentReconciler) newRunnerReplicaSet(rd v1alpha1.RunnerDeployment) (*v1alpha1.RunnerReplicaSet, error) {
	newRSTemplate := *rd.Spec.Template.DeepCopy()
	templateHash := ComputeHash(&newRSTemplate)
	// Add template hash label to selector.
	labels := CloneAndAddLabel(rd.Spec.Template.Labels, LabelKeyRunnerTemplateHash, templateHash)

	newRSTemplate.Labels = labels

	rs := v1alpha1.RunnerReplicaSet{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: rd.ObjectMeta.Name + "-",
			Namespace:    rd.ObjectMeta.Namespace,
			Labels:       labels,
		},
		Spec: v1alpha1.RunnerReplicaSetSpec{
			Replicas: rd.Spec.Replicas,
			Template: newRSTemplate,
		},
	}

	if err := ctrl.SetControllerReference(&rd, &rs, r.Scheme); err != nil {
		return &rs, err
	}

	return &rs, nil
}

func (r *RunnerDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("runnerdeployment-controller")

	if err := mgr.GetFieldIndexer().IndexField(&v1alpha1.RunnerReplicaSet{}, runnerSetOwnerKey, func(rawObj runtime.Object) []string {
		runnerSet := rawObj.(*v1alpha1.RunnerReplicaSet)
		owner := metav1.GetControllerOf(runnerSet)
		if owner == nil {
			return nil
		}

		if owner.APIVersion != v1alpha1.GroupVersion.String() || owner.Kind != "RunnerDeployment" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RunnerDeployment{}).
		Owns(&v1alpha1.RunnerReplicaSet{}).
		Complete(r)
}
