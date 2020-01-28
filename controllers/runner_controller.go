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
		log.Error(err, "unable to fetch Runner")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if !runner.IsRegisterable() {
			reg, err := r.newRegistration(ctx, runner.Spec.Repository)
			if err != nil {
				log.Error(err, "failed to get new registration")
				return ctrl.Result{}, err
			}

			updated := runner.DeepCopy()
			updated.Status.Registration = reg

			if err := r.Status().Update(ctx, updated); err != nil {
				log.Error(err, "unable to update Runner status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}

		pod := r.newPod(runner)
		err = r.Create(ctx, &pod)
		if err != nil {
			log.Error(err, "failed to create a new pod")
			return ctrl.Result{}, err
		}
	} else {
		newPod := r.newPod(runner)
		if reflect.DeepEqual(pod.Spec, newPod.Spec) {
			return ctrl.Result{}, nil
		}

		if !runner.IsRegisterable() {
			reg, err := r.newRegistration(ctx, runner.Spec.Repository)
			if err != nil {
				log.Error(err, "failed to get new registration")
				return ctrl.Result{}, err
			}

			updated := runner.DeepCopy()
			updated.Status.Registration = reg

			if err := r.Status().Update(ctx, updated); err != nil {
				log.Error(err, "unable to update Runner status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}

		// TODO: Do not update.
		updatedPod := pod.DeepCopy()
		updatedPod.Spec = newPod.Spec

		if err := r.Update(ctx, updatedPod); err != nil {
			log.Error(err, "unable to update pod")
			return ctrl.Result{}, err
		}
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

func (r *RunnerReconciler) newPod(runner v1alpha1.Runner) corev1.Pod {
	image := runner.Spec.Image
	if image == "" {
		image = defaultImage
	}

	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runner.Name,
			Namespace: runner.Namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: "Never",
			Containers: []corev1.Container{
				{
					Name:            "runner",
					Image:           image,
					ImagePullPolicy: "Always",
					Env: []corev1.EnvVar{
						corev1.EnvVar{
							Name:  "RUNNER_NAME",
							Value: runner.Name,
						},
						corev1.EnvVar{
							Name:  "RUNNER_REPO",
							Value: runner.Spec.Repository,
						},
						corev1.EnvVar{
							Name:  "RUNNER_TOKEN",
							Value: runner.Status.Registration.Token,
						},
					},
				},
			},
		},
	}
}

func (r *RunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Runner{}).
		Complete(r)
}
