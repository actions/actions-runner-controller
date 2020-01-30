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
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-github/v29/github"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

const (
	defaultImage = "summerwind/actions-runner:latest"
)

type RegistrationToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// RunnerReconciler reconciles a Runner object
type RunnerReconciler struct {
	client.Client
	Log          logr.Logger
	Scheme       *runtime.Scheme
	GitHubClient *github.Client
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch

func (r *RunnerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("runner", req.NamespacedName)

	var runner v1alpha1.Runner
	if err := r.Get(ctx, req.NamespacedName, &runner); err != nil {
		log.Error(err, "Unable to fetch Runner")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !runner.IsRegisterable() {
		reg, err := r.newRegistration(ctx, runner.Spec.Repository)
		if err != nil {
			log.Error(err, "Failed to get new registration")
			return ctrl.Result{}, err
		}

		updated := runner.DeepCopy()
		updated.Status.Registration = reg

		if err := r.Status().Update(ctx, updated); err != nil {
			log.Error(err, "Unable to update Runner status")
			return ctrl.Result{}, err
		}

		log.Info("Updated registration token", "repository", runner.Spec.Repository)
		return ctrl.Result{}, nil
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		newPod, err := r.newPod(runner)
		if err != nil {
			log.Error(err, "could not create pod")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, &newPod); err != nil {
			log.Error(err, "failed to create pod resource")
			return ctrl.Result{}, err
		}

		log.Info("Created a runner pod", "repository", runner.Spec.Repository)
	} else {
		newPod, err := r.newPod(runner)
		if err != nil {
			log.Error(err, "could not create pod")
			return ctrl.Result{}, err
		}

		update := false
		if pod.Spec.Containers[0].Image != newPod.Spec.Containers[0].Image {
			update = true
		}
		if !reflect.DeepEqual(pod.Spec.Containers[0].Env, newPod.Spec.Containers[0].Env) {
			update = true
		}
		if !update {
			return ctrl.Result{}, err
		}

		if err := r.Delete(ctx, &pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			log.Error(err, "failed to delete pod resource")
			return ctrl.Result{}, err
		}

		log.Info("Deleted a runner pod for updating", "repository", runner.Spec.Repository)
	}

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) newRegistration(ctx context.Context, repo string) (v1alpha1.RunnerStatusRegistration, error) {
	var reg v1alpha1.RunnerStatusRegistration

	rt, err := r.getRegistrationToken(ctx, repo)
	if err != nil {
		return reg, err
	}

	expiresAt, err := time.Parse(time.RFC3339, rt.ExpiresAt)
	if err != nil {
		return reg, err
	}

	reg.Repository = repo
	reg.Token = rt.Token
	reg.ExpiresAt = metav1.NewTime(expiresAt)

	return reg, err
}

func (r *RunnerReconciler) getRegistrationToken(ctx context.Context, repo string) (RegistrationToken, error) {
	var regToken RegistrationToken

	req, err := r.GitHubClient.NewRequest("POST", fmt.Sprintf("/repos/%s/actions/runners/registration-token", repo), nil)
	if err != nil {
		return regToken, err
	}

	res, err := r.GitHubClient.Do(ctx, req, &regToken)
	if err != nil {
		return regToken, err
	}

	if res.StatusCode != 201 {
		return regToken, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return regToken, nil
}

func (r *RunnerReconciler) newPod(runner v1alpha1.Runner) (corev1.Pod, error) {
	var (
		privileged bool  = true
		group      int64 = 0
	)

	image := runner.Spec.Image
	if image == "" {
		image = defaultImage
	}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runner.Name,
			Namespace: runner.Namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: "OnFailure",
			Containers: []corev1.Container{
				{
					Name:            "runner",
					Image:           image,
					ImagePullPolicy: "Always",
					Env: []corev1.EnvVar{
						{
							Name:  "RUNNER_NAME",
							Value: runner.Name,
						},
						{
							Name:  "RUNNER_REPO",
							Value: runner.Spec.Repository,
						},
						{
							Name:  "RUNNER_TOKEN",
							Value: runner.Status.Registration.Token,
						},
					},
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
					Image: "docker:19.03.5-dind",
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
	}

	if err := ctrl.SetControllerReference(&runner, &pod, r.Scheme); err != nil {
		return pod, err
	}

	return pod, nil
}

func (r *RunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Runner{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
