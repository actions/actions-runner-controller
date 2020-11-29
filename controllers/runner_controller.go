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
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
)

const (
	containerName = "runner"
	finalizerName = "runner.actions.summerwind.dev"
)

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
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("runner", req.NamespacedName)

	var runner v1alpha1.Runner
	if err := r.Get(ctx, req.NamespacedName, &runner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := runner.Validate()
	if err != nil {
		log.Info("Failed to validate runner spec", "error", err.Error())
		return ctrl.Result{}, nil
	}

	if runner.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runner.ObjectMeta.Finalizers)

		if added {
			newRunner := runner.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Update(ctx, newRunner); err != nil {
				log.Error(err, "Failed to update runner")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	} else {
		finalizers, removed := removeFinalizer(runner.ObjectMeta.Finalizers)

		if removed {
			if len(runner.Status.Registration.Token) > 0 {
				ok, err := r.unregisterRunner(ctx, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
				if err != nil {
					log.Error(err, "Failed to unregister runner")
					return ctrl.Result{}, err
				}

				if !ok {
					log.V(1).Info("Runner no longer exists on GitHub")
				}
			} else {
				log.V(1).Info("Runner was never registered on GitHub")
			}

			newRunner := runner.DeepCopy()
			newRunner.ObjectMeta.Finalizers = finalizers

			if err := r.Update(ctx, newRunner); err != nil {
				log.Error(err, "Failed to update runner")
				return ctrl.Result{}, err
			}

			log.Info("Removed runner from GitHub", "repository", runner.Spec.Repository, "organization", runner.Spec.Organization)
		}

		return ctrl.Result{}, nil
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if updated, err := r.updateRegistrationToken(ctx, runner); err != nil {
			return ctrl.Result{}, err
		} else if updated {
			return ctrl.Result{Requeue: true}, nil
		}

		newPod, err := r.newPod(runner)
		if err != nil {
			log.Error(err, "Could not create pod")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, &newPod); err != nil {
			log.Error(err, "Failed to create pod resource")
			return ctrl.Result{}, err
		}

		r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodCreated", fmt.Sprintf("Created pod '%s'", newPod.Name))
		log.Info("Created runner pod", "repository", runner.Spec.Repository)
	} else {
		// If pod has ended up succeeded we need to restart it
		// Happens e.g. when dind is in runner and run completes
		restart := pod.Status.Phase == corev1.PodSucceeded

		if !restart && runner.Status.Phase != string(pod.Status.Phase) {
			updated := runner.DeepCopy()
			updated.Status.Phase = string(pod.Status.Phase)
			updated.Status.Reason = pod.Status.Reason
			updated.Status.Message = pod.Status.Message

			if err := r.Status().Update(ctx, updated); err != nil {
				log.Error(err, "Failed to update runner status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}

		if !pod.ObjectMeta.DeletionTimestamp.IsZero() {
			return ctrl.Result{}, err
		}

		if pod.Status.Phase == corev1.PodRunning {
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name != containerName {
					continue
				}

				if status.State.Terminated != nil && status.State.Terminated.ExitCode == 0 {
					restart = true
				}
			}
		}

		if updated, err := r.updateRegistrationToken(ctx, runner); err != nil {
			return ctrl.Result{}, err
		} else if updated {
			return ctrl.Result{Requeue: true}, nil
		}

		newPod, err := r.newPod(runner)
		if err != nil {
			log.Error(err, "Could not create pod")
			return ctrl.Result{}, err
		}

		runnerBusy, err := r.isRunnerBusy(ctx, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
		if err != nil {
			log.Error(err, "Failed to check if runner is busy")
			return ctrl.Result{}, nil
		}

		if !runnerBusy && (!reflect.DeepEqual(pod.Spec.Containers[0].Env, newPod.Spec.Containers[0].Env) || pod.Spec.Containers[0].Image != newPod.Spec.Containers[0].Image) {
			restart = true
		}

		if !restart {
			return ctrl.Result{}, err
		}

		if err := r.Delete(ctx, &pod); err != nil {
			log.Error(err, "Failed to delete pod resource")
			return ctrl.Result{}, err
		}

		r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodDeleted", fmt.Sprintf("Deleted pod '%s'", newPod.Name))
		log.Info("Deleted runner pod", "repository", runner.Spec.Repository)
	}

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) isRunnerBusy(ctx context.Context, org, repo, name string) (bool, error) {
	runners, err := r.GitHubClient.ListRunners(ctx, org, repo)
	if err != nil {
		return false, err
	}

	for _, runner := range runners {
		if runner.GetName() == name {
			return runner.GetBusy(), nil
		}
	}

	return false, fmt.Errorf("runner not found")
}

func (r *RunnerReconciler) unregisterRunner(ctx context.Context, org, repo, name string) (bool, error) {
	runners, err := r.GitHubClient.ListRunners(ctx, org, repo)
	if err != nil {
		return false, err
	}

	id := int64(0)
	for _, runner := range runners {
		if runner.GetName() == name {
			if runner.GetBusy() {
				return false, fmt.Errorf("runner is busy")
			}
			id = runner.GetID()
			break
		}
	}

	if id == int64(0) {
		return false, nil
	}

	if err := r.GitHubClient.RemoveRunner(ctx, org, repo, id); err != nil {
		return false, err
	}

	return true, nil
}

func (r *RunnerReconciler) updateRegistrationToken(ctx context.Context, runner v1alpha1.Runner) (bool, error) {
	if runner.IsRegisterable() {
		return false, nil
	}

	log := r.Log.WithValues("runner", runner.Name)

	rt, err := r.GitHubClient.GetRegistrationToken(ctx, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
	if err != nil {
		r.Recorder.Event(&runner, corev1.EventTypeWarning, "FailedUpdateRegistrationToken", "Updating registration token failed")
		log.Error(err, "Failed to get new registration token")
		return false, err
	}

	updated := runner.DeepCopy()
	updated.Status.Registration = v1alpha1.RunnerStatusRegistration{
		Organization: runner.Spec.Organization,
		Repository:   runner.Spec.Repository,
		Labels:       runner.Spec.Labels,
		Token:        rt.GetToken(),
		ExpiresAt:    metav1.NewTime(rt.GetExpiresAt().Time),
	}

	if err := r.Status().Update(ctx, updated); err != nil {
		log.Error(err, "Failed to update runner status")
		return false, err
	}

	r.Recorder.Event(&runner, corev1.EventTypeNormal, "RegistrationTokenUpdated", "Successfully update registration token")
	log.Info("Updated registration token", "repository", runner.Spec.Repository)

	return true, nil
}

func (r *RunnerReconciler) newPod(runner v1alpha1.Runner) (corev1.Pod, error) {
	var (
		privileged      bool = true
		dockerdInRunner bool = runner.Spec.DockerdWithinRunnerContainer != nil && *runner.Spec.DockerdWithinRunnerContainer
		dockerEnabled   bool = runner.Spec.DockerEnabled == nil || *runner.Spec.DockerEnabled
	)

	runnerImage := runner.Spec.Image
	if runnerImage == "" {
		runnerImage = r.RunnerImage
	}

	workDir := runner.Spec.WorkDir
	if workDir == "" {
		workDir = "/runner/_work"
	}

	runnerImagePullPolicy := runner.Spec.ImagePullPolicy
	if runnerImagePullPolicy == "" {
		runnerImagePullPolicy = corev1.PullAlways
	}

	env := []corev1.EnvVar{
		{
			Name:  "RUNNER_NAME",
			Value: runner.Name,
		},
		{
			Name:  "RUNNER_ORG",
			Value: runner.Spec.Organization,
		},
		{
			Name:  "RUNNER_REPO",
			Value: runner.Spec.Repository,
		},
		{
			Name:  "RUNNER_LABELS",
			Value: strings.Join(runner.Spec.Labels, ","),
		},
		{
			Name:  "RUNNER_GROUP",
			Value: runner.Spec.Group,
		},
		{
			Name:  "RUNNER_TOKEN",
			Value: runner.Status.Registration.Token,
		},
		{
			Name:  "DOCKERD_IN_RUNNER",
			Value: fmt.Sprintf("%v", dockerdInRunner),
		},
		{
			Name:  "GITHUB_URL",
			Value: r.GitHubClient.GithubBaseURL,
		},
		{
			Name:  "RUNNER_WORKDIR",
			Value: workDir,
		},
	}

	env = append(env, runner.Spec.Env...)
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        runner.Name,
			Namespace:   runner.Namespace,
			Labels:      runner.Labels,
			Annotations: runner.Annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: "OnFailure",
			Containers: []corev1.Container{
				{
					Name:            containerName,
					Image:           runnerImage,
					ImagePullPolicy: runnerImagePullPolicy,
					Env:             env,
					EnvFrom:         runner.Spec.EnvFrom,
					SecurityContext: &corev1.SecurityContext{
						// Runner need to run privileged if it contains DinD
						Privileged: runner.Spec.DockerdWithinRunnerContainer,
					},
					Resources: runner.Spec.Resources,
				},
			},
		},
	}

	if !dockerdInRunner && dockerEnabled {
		pod.Spec.Volumes = []corev1.Volume{
			{
				Name: "work",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "externals",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "certs-client",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}
		pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "work",
				MountPath: workDir,
			},
			{
				Name:      "externals",
				MountPath: "/runner/externals",
			},
			{
				Name:      "certs-client",
				MountPath: "/certs/client",
				ReadOnly:  true,
			},
		}
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, []corev1.EnvVar{
			{
				Name:  "DOCKER_HOST",
				Value: "tcp://localhost:2376",
			},
			{
				Name:  "DOCKER_TLS_VERIFY",
				Value: "1",
			},
			{
				Name:  "DOCKER_CERT_PATH",
				Value: "/certs/client",
			},
		}...)
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
			Name:  "docker",
			Image: r.DockerImage,
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "work",
					MountPath: workDir,
				},
				{
					Name:      "externals",
					MountPath: "/runner/externals",
				},
				{
					Name:      "certs-client",
					MountPath: "/certs/client",
				},
			},
			Env: []corev1.EnvVar{
				{
					Name:  "DOCKER_TLS_CERTDIR",
					Value: "/certs",
				},
			},
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
		})

	}

	if len(runner.Spec.Containers) != 0 {
		pod.Spec.Containers = runner.Spec.Containers
		for i := 0; i < len(pod.Spec.Containers); i++ {
			if pod.Spec.Containers[i].Name == containerName {
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, env...)
			}
		}
	}

	if len(runner.Spec.VolumeMounts) != 0 {
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, runner.Spec.VolumeMounts...)
	}

	if len(runner.Spec.Volumes) != 0 {
		pod.Spec.Volumes = append(pod.Spec.Volumes, runner.Spec.Volumes...)
	}
	if len(runner.Spec.InitContainers) != 0 {
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, runner.Spec.InitContainers...)
	}

	if runner.Spec.NodeSelector != nil {
		pod.Spec.NodeSelector = runner.Spec.NodeSelector
	}
	if runner.Spec.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = runner.Spec.ServiceAccountName
	}
	if runner.Spec.AutomountServiceAccountToken != nil {
		pod.Spec.AutomountServiceAccountToken = runner.Spec.AutomountServiceAccountToken
	}

	if len(runner.Spec.SidecarContainers) != 0 {
		pod.Spec.Containers = append(pod.Spec.Containers, runner.Spec.SidecarContainers...)
	}

	if runner.Spec.SecurityContext != nil {
		pod.Spec.SecurityContext = runner.Spec.SecurityContext
	}

	if len(runner.Spec.ImagePullSecrets) != 0 {
		pod.Spec.ImagePullSecrets = runner.Spec.ImagePullSecrets
	}

	if runner.Spec.Affinity != nil {
		pod.Spec.Affinity = runner.Spec.Affinity
	}

	if len(runner.Spec.Tolerations) != 0 {
		pod.Spec.Tolerations = runner.Spec.Tolerations
	}

	if len(runner.Spec.EphemeralContainers) != 0 {
		pod.Spec.EphemeralContainers = runner.Spec.EphemeralContainers
	}

	if runner.Spec.TerminationGracePeriodSeconds != nil {
		pod.Spec.TerminationGracePeriodSeconds = runner.Spec.TerminationGracePeriodSeconds
	}

	if err := ctrl.SetControllerReference(&runner, &pod, r.Scheme); err != nil {
		return pod, err
	}

	return pod, nil
}

func (r *RunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("runner-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Runner{}).
		Owns(&corev1.Pod{}).
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
