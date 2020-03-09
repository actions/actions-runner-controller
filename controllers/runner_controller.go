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
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
)

const finalizerName = "runner.actions.summerwind.dev"

// RunnerReconciler reconciles a Runner object
type RunnerReconciler struct {
	client.Client
	Log          logr.Logger
	Recorder     record.EventRecorder
	Scheme       *runtime.Scheme
	GitHubClient *github.Client
	RunnerImage  string
	DockerImage  string
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RunnerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("runner", req.NamespacedName)

	var runner v1alpha1.Runner
	if err := r.Get(ctx, req.NamespacedName, &runner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if runner.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runner.ObjectMeta.Finalizers)

		if added {
			newRunner := runner.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Update(ctx, newRunner); err != nil {
				log.Error(err, "Failed to update Runner")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	} else {
		finalizers, removed := removeFinalizer(runner.ObjectMeta.Finalizers)

		if removed {
			ok, err := r.GitHubClient.RemoveRunner(ctx, runner.Spec.Repository, runner.Name)
			if err != nil {
				log.Error(err, "Failed to remove runner from GitHub")
				return ctrl.Result{}, err
			}

			if !ok {
				log.V(1).Info("Runner no longer exists on GitHub")
			}

			newRunner := runner.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Update(ctx, newRunner); err != nil {
				log.Error(err, "Failed to update Runner")
				return ctrl.Result{}, err
			}

			log.Info("Removed runner from GitHub", "repository", runner.Spec.Repository)
		}

		return ctrl.Result{}, nil
	}

	var statefulSet appsv1.StatefulSet
	if err := r.Get(ctx, req.NamespacedName, &statefulSet); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		newStatefulSet, err := r.newStatefulSet(runner)
		if err != nil {
			log.Error(err, "Failed to generate StatefulSet from Runner")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, &newStatefulSet); err != nil {
			log.Error(err, "Failed to create StatefulSet")
			return ctrl.Result{}, err
		}

		r.Recorder.Event(&runner, corev1.EventTypeNormal, "StatefulSetCreated", fmt.Sprintf("Created StatefulSet '%s'", newStatefulSet.Name))
		log.Info("Created StatefulSet")
	} else {
		if !statefulSet.ObjectMeta.DeletionTimestamp.IsZero() {
			return ctrl.Result{}, err
		}

		if !isSpecInSync(runner.Spec, statefulSet.Spec) {
			newStatefulSet := statefulSet.DeepCopy()
			newStatefulSet.Spec.Template.Spec.Containers[0].Image = runner.Spec.Image
			newStatefulSet.Spec.Template.Spec.Containers[0].Env = runner.Spec.Env
			newStatefulSet.Spec.Replicas = runner.Spec.Replicas

			if err := r.Update(ctx, newStatefulSet); err != nil {
				log.Error(err, "Failed to update StatefulSet")
				return ctrl.Result{}, err
			}

			r.Recorder.Event(&runner, corev1.EventTypeNormal, "StatefulSetUpdated", fmt.Sprintf("Updated StatefulSet '%s'", newStatefulSet.Name))
			log.Info("Updated StatefulSet")

			return ctrl.Result{}, nil
		}

		if !isStatusInSync(runner.Status, statefulSet.Status) {
			newRunner := runner.DeepCopy()
			newRunner.Status.Replicas = statefulSet.Status.Replicas
			newRunner.Status.ReadyReplicas = statefulSet.Status.ReadyReplicas
			newRunner.Status.CurrentReplicas = statefulSet.Status.CurrentReplicas
			newRunner.Status.UpdatedReplicas = statefulSet.Status.UpdatedReplicas

			if err := r.Status().Update(ctx, newRunner); err != nil {
				log.Error(err, "Failed to update Runner status")
				return ctrl.Result{}, err
			}

			log.V(1).Info("Updated Runner status")

			return ctrl.Result{}, nil
		}

		pods, err := r.getPods(statefulSet)
		if err != nil {
			log.Error(err, "Failed to get Pod list")
			return ctrl.Result{}, err
		}

		for _, pod := range pods {
			restart := false

			if !pod.ObjectMeta.DeletionTimestamp.IsZero() {
				continue
			}

			if pod.Status.Phase == corev1.PodRunning {
				for _, status := range pod.Status.ContainerStatuses {
					if status.Name != v1alpha1.ContainerName {
						continue
					}

					if status.State.Terminated != nil && status.State.Terminated.ExitCode == 0 {
						restart = true
					}
				}
			}

			if restart {
				if err := r.Delete(ctx, &pod); err != nil {
					log.Error(err, "Failed to delete pod")
					return ctrl.Result{}, err
				}

				r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodDeleted", fmt.Sprintf("Deleted Pod '%s'", pod.Name))
				log.Info("Deleted Pod", "pod", pod.Name)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) newStatefulSet(runner v1alpha1.Runner) (appsv1.StatefulSet, error) {
	var (
		privileged bool  = true
		group      int64 = 0
	)

	runnerImage := runner.Spec.Image
	if runnerImage == "" {
		runnerImage = r.RunnerImage
	}

	statefulSet := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runner.Name,
			Namespace: runner.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: runner.Name,
			Replicas:    runner.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					v1alpha1.KeyRunnerName: runner.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.KeyRunnerName: runner.Name,
					},
					Annotations: map[string]string{
						v1alpha1.KeyRunnerRepository: runner.Spec.Repository,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            v1alpha1.ContainerName,
							Image:           runnerImage,
							ImagePullPolicy: "Always",
							Env:             runner.Spec.Env,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "docker",
									MountPath: "/var/run",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsGroup: &group,
							},
						},
						{
							Name:  "docker",
							Image: r.DockerImage,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "docker",
									MountPath: "/var/run",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
						},
					},
					Volumes: []corev1.Volume{
						corev1.Volume{
							Name: "docker",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(&runner, &statefulSet, r.Scheme); err != nil {
		return statefulSet, err
	}

	return statefulSet, nil
}

func (r *RunnerReconciler) getPods(statefulSet appsv1.StatefulSet) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}
	opts := []client.ListOption{
		client.InNamespace(statefulSet.Namespace),
		client.MatchingLabels(map[string]string{
			v1alpha1.KeyRunnerName: statefulSet.Name,
		}),
	}

	if err := r.List(context.Background(), podList, opts...); err != nil {
		return nil, err
	}

	return podList.Items, nil
}

func (r *RunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("runner-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Runner{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}

func addFinalizer(finalizers []string) ([]string, bool) {
	exists := false
	for _, name := range finalizers {
		if name == finalizerName {
			exists = true
		}
	}

	if exists {
		return finalizers, false
	}

	return append(finalizers, finalizerName), true
}

func removeFinalizer(finalizers []string) ([]string, bool) {
	removed := false
	result := []string{}

	for _, name := range finalizers {
		if name == finalizerName {
			removed = true
			continue
		}
		result = append(result, name)
	}

	return result, removed
}

func isSpecInSync(rs v1alpha1.RunnerSpec, ss appsv1.StatefulSetSpec) bool {
	if rs.Image != ss.Template.Spec.Containers[0].Image {
		return false
	}
	if !reflect.DeepEqual(rs.Env, ss.Template.Spec.Containers[0].Env) {
		return false
	}
	if *rs.Replicas != *ss.Replicas {
		return false
	}

	return true
}

func isStatusInSync(rs v1alpha1.RunnerStatus, ss appsv1.StatefulSetStatus) bool {
	if rs.Replicas != ss.Replicas {
		return false
	}
	if rs.ReadyReplicas != ss.ReadyReplicas {
		return false
	}
	if rs.CurrentReplicas != ss.CurrentReplicas {
		return false
	}
	if rs.UpdatedReplicas != ss.UpdatedReplicas {
		return false
	}

	return true
}
