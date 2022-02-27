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
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/controllers/metrics"
	"github.com/go-logr/logr"
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

	CommonRunnerLabels     []string
	GitHubBaseURL          string
	RunnerImage            string
	RunnerImagePullSecrets []string
	DockerImage            string
	DockerRegistryMirror   string
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

	var statefulsetList appsv1.StatefulSetList
	if err := r.List(ctx, &statefulsetList, client.InNamespace(req.Namespace), client.MatchingFields{runnerSetOwnerKey: req.Name}); err != nil {
		return ctrl.Result{}, err
	}

	statefulsets := statefulsetList.Items

	if len(statefulsets) > 1000 {
		log.Info("Postponed reconcilation to prevent potential infinite loop. If you're really scaling more than 1000 statefulsets, do change this hard-coded threshold!")
		return ctrl.Result{}, nil
	}

	desiredTemplateHash, ok := getStatefulSetTemplateHash(desiredStatefulSet)
	if !ok {
		log.Info("Failed to get template hash of desired statefulset. It must be in an invalid state. Please manually delete the statefulset so that it is recreated")

		return ctrl.Result{}, nil
	}

	statefulsetsPerTemplateHash := map[string][]*podsForStatefulset{}

	// # Why do we recreate statefulsets instead of updating their desired replicas?
	//
	// A statefulset cannot add more pods when not all the pods are running.
	// Our ephemeral runners' pods that have finished running become Completed(Phase=Succeeded).
	// So creating one statefulset per a batch of ephemeral runners is the only way for us to add more replicas.
	//
	// # Why do we recreate statefulsets instead of updating fields other than replicas?
	//
	// That's because Kubernetes doesn't allow updating anything other than replicas, template, and updateStrategy.
	// And the nature of ephemeral runner pods requires you to create a statefulset per a batch of new runner pods so
	// we have really no other choice.
	//
	// If you're curious, the below is the error message you will get when you tried to update forbidden StatefulSet field(s):
	//
	// 2021-06-13T07:19:52.760Z        ERROR   actions-runner-controller.runnerset     Failed to patch statefulset
	// {"runnerset": "default/example-runnerset", "error": "StatefulSet.apps \"example-runnerset\" is invalid: s
	// pec: Forbidden: updates to statefulset spec for fields other than 'replicas', 'template', and 'updateStrategy'
	// are forbidden"}
	//
	// Even though the error message includes "Forbidden", this error's reason is "Invalid".
	// So we used to match these errors by using errors.IsInvalid. But that's another story...

	var lastSyncTime *time.Time

	for _, ss := range statefulsets {
		ss := ss

		log := log.WithValues("statefulset", types.NamespacedName{Namespace: ss.Namespace, Name: ss.Name})

		res, err := r.getPodsForStatefulset(ctx, log, &ss)
		if err != nil {
			return ctrl.Result{}, err
		}

		if !res.statefulset.DeletionTimestamp.IsZero() {
			continue
		}

		if res.statefulset.Annotations != nil {
			if a, ok := res.statefulset.Annotations[SyncTimeAnnotationKey]; ok {
				t, err := time.Parse(time.RFC3339, a)
				if err == nil {
					if lastSyncTime == nil || lastSyncTime.Before(t) {
						lastSyncTime = &t
					}
				}
			}
		}

		statefulsetsPerTemplateHash[res.templateHash] = append(statefulsetsPerTemplateHash[res.templateHash], res)

		if res.total > 0 && res.total == res.completed {
			if err := r.Client.Delete(ctx, &ss); err != nil {
				log.Error(err, "Unable to delete statefulset")
				return ctrl.Result{}, err
			}

			log.V(2).Info("Deleted completed statefulset")

			return ctrl.Result{}, nil
		}

		var replicas int32 = 1
		if ss.Spec.Replicas != nil {
			replicas = *ss.Spec.Replicas
		}

		if ss.Status.Replicas != replicas {
			log.V(2).Info("Waiting for statefulset to sync", "desiredReplicas", replicas, "currentReplicas", ss.Status.Replicas)
			return ctrl.Result{}, nil
		}
	}

	currentStatefulSets := statefulsetsPerTemplateHash[desiredTemplateHash]

	sort.SliceStable(currentStatefulSets, func(i, j int) bool {
		return currentStatefulSets[i].statefulset.CreationTimestamp.Before(&currentStatefulSets[j].statefulset.CreationTimestamp)
	})

	if len(currentStatefulSets) > 0 {
		timestampFirst := currentStatefulSets[0].statefulset.CreationTimestamp
		timestampLast := currentStatefulSets[len(currentStatefulSets)-1].statefulset.CreationTimestamp
		var names []string
		for _, ss := range currentStatefulSets {
			names = append(names, ss.statefulset.Name)
		}
		log.V(2).Info("Detected some current statefulsets", "creationTimestampFirst", timestampFirst, "creationTimestampLast", timestampLast, "statefulsets", names)
	}

	var pending, running int

	for _, ss := range currentStatefulSets {
		pending += ss.pending
		running += ss.running
	}

	const defaultReplicas = 1

	var replicasOfDesiredStatefulSet *int
	if desiredStatefulSet.Spec.Replicas != nil {
		v := int(*desiredStatefulSet.Spec.Replicas)
		replicasOfDesiredStatefulSet = &v
	}

	newDesiredReplicas := getIntOrDefault(replicasOfDesiredStatefulSet, defaultReplicas)

	log.V(2).Info("Found some pods across statefulset(s)", "pending", pending, "running", running, "desired", newDesiredReplicas, "statefulsets", len(statefulsets))

	effectiveTime := runnerSet.Spec.EffectiveTime
	ephemeral := runnerSet.Spec.Ephemeral == nil || *runnerSet.Spec.Ephemeral

	if newDesiredReplicas > pending+running && ephemeral && lastSyncTime != nil && effectiveTime != nil && lastSyncTime.After(effectiveTime.Time) {
		log.V(2).Info("Detected that some ephemeral runners have disappeared. Usually this is due to that ephemeral runner completions so ARC does not create new runners until EffectiveTime is updated.", "lastSyncTime", metav1.Time{Time: *lastSyncTime}, "effectiveTime", *effectiveTime, "desired", newDesiredReplicas, "pending", pending, "running", running)
	} else if newDesiredReplicas > pending+running {
		num := newDesiredReplicas - (pending + running)

		for i := 0; i < num; i++ {
			// Add more replicas
			addedReplicas := int32(1)

			create := desiredStatefulSet.DeepCopy()
			create.Spec.Replicas = &addedReplicas
			if err := r.Client.Create(ctx, create); err != nil {
				return ctrl.Result{}, err
			}
		}

		log.V(2).Info("Created statefulset(s) to add more replicas", "num", num)

		return ctrl.Result{}, nil
	} else if newDesiredReplicas < running {
		var retained int
		var lastIndex int
		for i := len(currentStatefulSets) - 1; i >= 0; i-- {
			ss := currentStatefulSets[i]
			retained += ss.running
			if retained >= newDesiredReplicas {
				lastIndex = i
				break
			}
		}

		if retained == newDesiredReplicas {
			for i := 0; i < lastIndex; i++ {
				ss := currentStatefulSets[i]
				log := log.WithValues("statefulset", types.NamespacedName{Namespace: ss.statefulset.Namespace, Name: ss.statefulset.Name})
				if err := r.Client.Delete(ctx, ss.statefulset); err != nil {
					return ctrl.Result{}, err
				}
				log.V(2).Info("Deleted redundant statefulset", "i", i, "lastIndex", lastIndex)
			}
			return ctrl.Result{}, err
		} else if retained > newDesiredReplicas {
			log.V(2).Info("Waiting sync before scale down", "retained", retained, "newDesiredReplicas", newDesiredReplicas, "lastIndex", lastIndex)

			return ctrl.Result{}, nil
		} else {
			log.Info("Invalid state", "retained", retained, "newDesiredReplicas", newDesiredReplicas, "lastIndex", lastIndex)
			panic("crashed due to invalid state")
		}
	}

	for _, sss := range statefulsetsPerTemplateHash {
		for _, ss := range sss {
			if ss.templateHash != desiredTemplateHash {
				if ss.statefulset.DeletionTimestamp.IsZero() {
					if err := r.Client.Delete(ctx, ss.statefulset); err != nil {
						log.Error(err, "Unable to delete statefulset")
						return ctrl.Result{}, err
					}

					log.V(2).Info("Deleted redundant and outdated statefulset")
				}

				return ctrl.Result{}, nil
			}
		}
	}

	var statusReplicas, statusReadyReplicas, totalCurrentReplicas, updatedReplicas int

	for _, ss := range currentStatefulSets {
		statusReplicas += int(ss.statefulset.Status.Replicas)
		statusReadyReplicas += int(ss.statefulset.Status.ReadyReplicas)
		totalCurrentReplicas += int(ss.statefulset.Status.CurrentReplicas)
		updatedReplicas += int(ss.statefulset.Status.UpdatedReplicas)
	}

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

type podsForStatefulset struct {
	total        int
	completed    int
	running      int
	terminating  int
	pending      int
	templateHash string
	statefulset  *appsv1.StatefulSet
	pods         []corev1.Pod
}

func (r *RunnerSetReconciler) getPodsForStatefulset(ctx context.Context, log logr.Logger, ss *appsv1.StatefulSet) (*podsForStatefulset, error) {
	var podList corev1.PodList

	if err := r.Client.List(ctx, &podList, client.MatchingLabels(ss.Spec.Template.ObjectMeta.Labels)); err != nil {
		log.Error(err, "Failed to list pods managed by statefulset")
		return nil, err
	}

	var completed, running, terminating, pending, total int

	for _, pod := range podList.Items {
		if owner := metav1.GetControllerOf(&pod); owner == nil || owner.Kind != "StatefulSet" || owner.Name != ss.Name {
			continue
		}

		total++

		if runnerPodOrContainerIsStopped(&pod) {
			completed++
		} else if pod.Status.Phase == corev1.PodRunning {
			running++
		} else if !pod.DeletionTimestamp.IsZero() {
			terminating++
		} else {
			pending++
		}
	}

	templateHash, ok := getStatefulSetTemplateHash(ss)
	if !ok {
		log.Info("Failed to get template hash of statefulset. It must be in an invalid state. Please manually delete the statefulset so that it is recreated")

		return nil, nil
	}

	return &podsForStatefulset{
		total:        total,
		completed:    completed,
		running:      running,
		terminating:  terminating,
		pending:      pending,
		templateHash: templateHash,
		statefulset:  ss,
		pods:         podList.Items,
	}, nil
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

	pod, err := newRunnerPod(template, runnerSet.Spec.RunnerConfig, r.RunnerImage, r.RunnerImagePullSecrets, r.DockerImage, r.DockerRegistryMirror, r.GitHubBaseURL, false)
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
