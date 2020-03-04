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
	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"hash/fnv"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sort"

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
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerdeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnersets/status,verbs=get;update;patch

func (r *RunnerDeploymentReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("runnerset", req.NamespacedName)

	var rd v1alpha1.RunnerDeployment
	if err := r.Get(ctx, req.NamespacedName, &rd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rd.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var myRunnerSetList v1alpha1.RunnerSetList
	if err := r.List(ctx, &myRunnerSetList, client.InNamespace(req.Namespace), client.MatchingFields{runnerSetOwnerKey: req.Name}); err != nil {
		return ctrl.Result{}, err
	}

	myRunnerSets := myRunnerSetList.Items

	sort.Slice(myRunnerSets, func(i, j int) bool {
		return myRunnerSets[i].GetCreationTimestamp().After(myRunnerSets[j].GetCreationTimestamp().Time)
	})

	var newestSet *v1alpha1.RunnerSet

	var oldSets []v1alpha1.RunnerSet

	if len(myRunnerSets) > 0 {
		newestSet = &myRunnerSets[0]
	}

	if len(myRunnerSets) > 1 {
		oldSets = myRunnerSets[1:]
	}

	desiredRS, err := r.newRunnerSet(rd)
	if err != nil {
		log.Error(err, "Could not create runnerset")

		return ctrl.Result{}, err
	}

	if newestSet == nil {
		if err := r.Client.Create(ctx, &desiredRS); err != nil {
			log.Error(err, "Failed to create runnerset resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	newestTemplateHash, ok := getTemplateHash(newestSet)
	if !ok {
		log.Info("Failed to get template hash of newest runnerset resource. It must be in an invalid state. Please manually delete the runnerset so that it is recreated")

		return ctrl.Result{}, nil
	}

	desiredTemplateHash, ok := getTemplateHash(&desiredRS)
	if !ok {
		log.Info("Failed to get template hash of desired runnerset resource. It must be in an invalid state. Please manually delete the runnerset so that it is recreated")

		return ctrl.Result{}, nil
	}

	if newestTemplateHash != desiredTemplateHash {
		if err := r.Client.Create(ctx, &desiredRS); err != nil {
			log.Error(err, "Failed to create runnerset resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// Please add more conditions that we can in-place update the newest runnerset without disruption
	if newestSet.Spec.Replicas != desiredRS.Spec.Replicas {
		newestSet.Spec.Replicas = desiredRS.Spec.Replicas

		if err := r.Client.Update(ctx, newestSet); err != nil {
			log.Error(err, "Failed to update runnerset resource")

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	for i := range oldSets {
		rs := oldSets[i]

		if err := r.Client.Delete(ctx, &rs); err != nil {
			log.Error(err, "Failed to delete runner resource")

			return ctrl.Result{}, err
		}

		r.Recorder.Event(&rd, corev1.EventTypeNormal, "RunnerSetDeleted", fmt.Sprintf("Deleted runnerset '%s'", rs.Name))
		log.Info("Deleted runnerset", "runnerdeployment", rd.ObjectMeta.Name, "runnerset", rs.Name)
	}

	return ctrl.Result{}, nil
}

func getTemplateHash(rs *v1alpha1.RunnerSet) (string, bool) {
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

func (r *RunnerDeploymentReconciler) newRunnerSet(rd v1alpha1.RunnerDeployment) (v1alpha1.RunnerSet, error) {
	newRSTemplate := *rd.Spec.Template.DeepCopy()
	templateHash := ComputeHash(&newRSTemplate)
	// Add template hash label to selector.
	labels := CloneAndAddLabel(rd.Spec.Template.Labels, LabelKeyRunnerTemplateHash, templateHash)

	newRSTemplate.Labels = labels

	rs := v1alpha1.RunnerSet{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: rd.ObjectMeta.Name,
			Namespace:    rd.ObjectMeta.Namespace,
			Labels:       labels,
		},
		Spec: v1alpha1.RunnerSetSpec{
			Replicas: rd.Spec.Replicas,
			Template: newRSTemplate,
		},
	}

	if err := ctrl.SetControllerReference(&rd, &rs, r.Scheme); err != nil {
		return rs, err
	}

	return rs, nil
}

func (r *RunnerDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("runnerdeployment-controller")

	if err := mgr.GetFieldIndexer().IndexField(&v1alpha1.RunnerSet{}, runnerSetOwnerKey, func(rawObj runtime.Object) []string {
		runnerSet := rawObj.(*v1alpha1.RunnerSet)
		owner := metav1.GetControllerOf(runnerSet)
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
		For(&v1alpha1.RunnerDeployment{}).
		Owns(&v1alpha1.RunnerSet{}).
		Complete(r)
}
