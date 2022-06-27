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
	"strings"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/hash"
	"github.com/go-logr/logr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
)

const (
	containerName = "runner"
	finalizerName = "runner.actions.summerwind.dev"

	LabelKeyPodTemplateHash = "pod-template-hash"

	retryDelayOnGitHubAPIRateLimitError = 30 * time.Second

	EnvVarOrg        = "RUNNER_ORG"
	EnvVarRepo       = "RUNNER_REPO"
	EnvVarEnterprise = "RUNNER_ENTERPRISE"
	EnvVarEphemeral  = "RUNNER_EPHEMERAL"
	EnvVarTrue       = "true"
)

// RunnerReconciler reconciles a Runner object
type RunnerReconciler struct {
	client.Client
	Log                         logr.Logger
	Recorder                    record.EventRecorder
	Scheme                      *runtime.Scheme
	GitHubClient                *github.Client
	RunnerImage                 string
	RunnerImagePullSecrets      []string
	DockerImage                 string
	DockerRegistryMirror        string
	Name                        string
	RegistrationRecheckInterval time.Duration
	RegistrationRecheckJitter   time.Duration

	UnregistrationRetryDelay time.Duration
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

func (r *RunnerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("runner", req.NamespacedName)

	var runner v1alpha1.Runner
	if err := r.Get(ctx, req.NamespacedName, &runner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if runner.ObjectMeta.DeletionTimestamp.IsZero() {
		finalizers, added := addFinalizer(runner.ObjectMeta.Finalizers, finalizerName)

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
		// Request to remove a runner. DeletionTimestamp was set in the runner - we need to unregister runner
		var pod corev1.Pod
		if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("Retrying soon as we failed to get runner pod: %v", err))
				return ctrl.Result{Requeue: true}, nil
			}
			// Pod was not found
			return r.processRunnerDeletion(runner, ctx, log, nil)
		}
		return r.processRunnerDeletion(runner, ctx, log, &pod)
	}

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !kerrors.IsNotFound(err) {
			// An error ocurred
			return ctrl.Result{}, err
		}
		return r.processRunnerCreation(ctx, runner, log)
	}

	phase := string(pod.Status.Phase)
	if phase == "" {
		phase = "Created"
	}

	ready := runnerPodReady(&pod)

	if runner.Status.Phase != phase || runner.Status.Ready != ready {
		if pod.Status.Phase == corev1.PodRunning {
			// Seeing this message, you can expect the runner to become `Running` soon.
			log.V(1).Info(
				"Runner appears to have been registered and running.",
				"podCreationTimestamp", pod.CreationTimestamp,
			)
		}

		updated := runner.DeepCopy()
		updated.Status.Phase = phase
		updated.Status.Ready = ready
		updated.Status.Reason = pod.Status.Reason
		updated.Status.Message = pod.Status.Message

		if err := r.Status().Patch(ctx, updated, client.MergeFrom(&runner)); err != nil {
			log.Error(err, "Failed to update runner status for Phase/Reason/Message")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func runnerPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type != corev1.PodReady {
			continue
		}

		return c.Status == corev1.ConditionTrue
	}

	return false
}

func runnerContainerExitCode(pod *corev1.Pod) *int32 {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != containerName {
			continue
		}

		if status.State.Terminated != nil {
			return &status.State.Terminated.ExitCode
		}
	}

	return nil
}

func runnerPodOrContainerIsStopped(pod *corev1.Pod) bool {
	// If pod has ended up succeeded we need to restart it
	// Happens e.g. when dind is in runner and run completes
	stopped := pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed

	if !stopped {
		if pod.Status.Phase == corev1.PodRunning {
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name != containerName {
					continue
				}

				if status.State.Terminated != nil {
					stopped = true
				}
			}
		}
	}

	return stopped
}

func ephemeralRunnerContainerStatus(pod *corev1.Pod) *corev1.ContainerStatus {
	if getRunnerEnv(pod, "RUNNER_EPHEMERAL") != "true" {
		return nil
	}

	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != containerName {
			continue
		}

		status := status

		return &status
	}

	return nil
}

func (r *RunnerReconciler) processRunnerDeletion(runner v1alpha1.Runner, ctx context.Context, log logr.Logger, pod *corev1.Pod) (reconcile.Result, error) {
	finalizers, removed := removeFinalizer(runner.ObjectMeta.Finalizers, finalizerName)

	if removed {
		newRunner := runner.DeepCopy()
		newRunner.ObjectMeta.Finalizers = finalizers

		if err := r.Patch(ctx, newRunner, client.MergeFrom(&runner)); err != nil {
			log.Error(err, "Unable to remove finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Removed finalizer")
	}

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) processRunnerCreation(ctx context.Context, runner v1alpha1.Runner, log logr.Logger) (reconcile.Result, error) {
	if updated, err := r.updateRegistrationToken(ctx, runner); err != nil {
		return ctrl.Result{RequeueAfter: RetryDelayOnCreateRegistrationError}, nil
	} else if updated {
		return ctrl.Result{Requeue: true}, nil
	}

	newPod, err := r.newPod(runner)
	if err != nil {
		log.Error(err, "Could not create pod")
		return ctrl.Result{}, err
	}

	if err := r.Create(ctx, &newPod); err != nil {
		if kerrors.IsAlreadyExists(err) {
			// Gracefully handle pod-already-exists errors due to informer cache delay.
			// Without this we got a few errors like the below on new runner pod:
			// 2021-03-16T00:23:10.116Z        ERROR   controller-runtime.controller   Reconciler error      {"controller": "runner-controller", "request": "default/example-runnerdeploy-b2g2g-j4mcp", "error": "pods \"example-runnerdeploy-b2g2g-j4mcp\" already exists"}
			log.Info(
				"Failed to create pod due to AlreadyExists error. Probably this pod has been already created in previous reconcilation but is still not in the informer cache. Will retry on pod created. If it doesn't repeat, there's no problem",
			)
			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to create pod resource")

		return ctrl.Result{}, err
	}

	r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodCreated", fmt.Sprintf("Created pod '%s'", newPod.Name))
	log.Info("Created runner pod", "repository", runner.Spec.Repository)

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) updateRegistrationToken(ctx context.Context, runner v1alpha1.Runner) (bool, error) {
	if runner.IsRegisterable() {
		return false, nil
	}

	log := r.Log.WithValues("runner", runner.Name)

	rt, err := r.GitHubClient.GetRegistrationToken(ctx, runner.Spec.Enterprise, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
	if err != nil {
		// An error can be a permanent, permission issue like the below:
		//    POST https://api.github.com/enterprises/YOUR_ENTERPRISE/actions/runners/registration-token: 403 Resource not accessible by integration []
		// In such case retrying in seconds might not make much sense.

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

	if err := r.Status().Patch(ctx, updated, client.MergeFrom(&runner)); err != nil {
		log.Error(err, "Failed to update runner status for Registration")
		return false, err
	}

	r.Recorder.Event(&runner, corev1.EventTypeNormal, "RegistrationTokenUpdated", "Successfully update registration token")
	log.Info("Updated registration token", "repository", runner.Spec.Repository)

	return true, nil
}

func (r *RunnerReconciler) newPod(runner v1alpha1.Runner) (corev1.Pod, error) {
	var template corev1.Pod

	labels := map[string]string{}

	for k, v := range runner.ObjectMeta.Labels {
		labels[k] = v
	}

	// This implies that...
	//
	// (1) We recreate the runner pod whenever the runner has changes in:
	// - metadata.labels (excluding "runner-template-hash" added by the parent RunnerReplicaSet
	// - metadata.annotations
	// - metadata.spec (including image, env, organization, repository, group, and so on)
	// - GithubBaseURL setting of the controller (can be configured via GITHUB_ENTERPRISE_URL)
	//
	// (2) We don't recreate the runner pod when there are changes in:
	// - runner.status.registration.token
	//   - This token expires and changes hourly, but you don't need to recreate the pod due to that.
	//     It's the opposite.
	//     An unexpired token is required only when the runner agent is registering itself on launch.
	//
	//     In other words, the registered runner doesn't get invalidated on registration token expiration.
	//     A registered runner's session and the a registration token seem to have two different and independent
	//     lifecycles.
	//
	//     See https://github.com/actions-runner-controller/actions-runner-controller/issues/143 for more context.
	labels[LabelKeyPodTemplateHash] = hash.FNVHashStringObjects(
		filterLabels(runner.ObjectMeta.Labels, LabelKeyRunnerTemplateHash),
		runner.ObjectMeta.Annotations,
		runner.Spec,
		r.GitHubClient.GithubBaseURL,
		// Token change should trigger replacement.
		// We need to include this explicitly here because
		// runner.Spec does not contain the possibly updated token stored in the
		// runner status yet.
		runner.Status.Registration.Token,
	)

	objectMeta := metav1.ObjectMeta{
		Name:        runner.ObjectMeta.Name,
		Namespace:   runner.ObjectMeta.Namespace,
		Labels:      labels,
		Annotations: runner.ObjectMeta.Annotations,
	}

	template.ObjectMeta = objectMeta

	if len(runner.Spec.Containers) == 0 {
		template.Spec.Containers = append(template.Spec.Containers, corev1.Container{
			Name: "runner",
		})

		if (runner.Spec.DockerEnabled == nil || *runner.Spec.DockerEnabled) && (runner.Spec.DockerdWithinRunnerContainer == nil || !*runner.Spec.DockerdWithinRunnerContainer) {
			template.Spec.Containers = append(template.Spec.Containers, corev1.Container{
				Name: "docker",
			})
		}
	} else {
		template.Spec.Containers = runner.Spec.Containers
	}

	for i, c := range template.Spec.Containers {
		switch c.Name {
		case "runner":
			if c.ImagePullPolicy == "" {
				template.Spec.Containers[i].ImagePullPolicy = runner.Spec.ImagePullPolicy
			}
			if len(c.EnvFrom) == 0 {
				template.Spec.Containers[i].EnvFrom = runner.Spec.EnvFrom
			}
			if len(c.Env) == 0 {
				template.Spec.Containers[i].Env = runner.Spec.Env
			}
			if len(c.Resources.Requests) == 0 {
				template.Spec.Containers[i].Resources.Requests = runner.Spec.Resources.Requests
			}
			if len(c.Resources.Limits) == 0 {
				template.Spec.Containers[i].Resources.Limits = runner.Spec.Resources.Limits
			}
		case "docker":
			if len(c.VolumeMounts) == 0 {
				template.Spec.Containers[i].VolumeMounts = runner.Spec.DockerVolumeMounts
			}
			if len(c.Resources.Limits) == 0 {
				template.Spec.Containers[i].Resources.Limits = runner.Spec.DockerdContainerResources.Limits
			}
			if len(c.Resources.Requests) == 0 {
				template.Spec.Containers[i].Resources.Requests = runner.Spec.DockerdContainerResources.Requests
			}
			if len(c.Env) == 0 {
				template.Spec.Containers[i].Env = runner.Spec.DockerEnv
			}
		}
	}

	template.Spec.SecurityContext = runner.Spec.SecurityContext
	template.Spec.EnableServiceLinks = runner.Spec.EnableServiceLinks

	pod, err := newRunnerPod(runner.Name, template, runner.Spec.RunnerConfig, r.RunnerImage, r.RunnerImagePullSecrets, r.DockerImage, r.DockerRegistryMirror, r.GitHubClient.GithubBaseURL)
	if err != nil {
		return pod, err
	}

	// Customize the pod spec according to the runner spec
	runnerSpec := runner.Spec

	if len(runnerSpec.VolumeMounts) != 0 {
		// if operater provides a work volume mount, use that
		isPresent, _ := workVolumeMountPresent(runnerSpec.VolumeMounts)
		if isPresent {
			// remove work volume since it will be provided from runnerSpec.Volumes
			// if we don't remove it here we would get a duplicate key error, i.e. two volumes named work
			_, index := workVolumeMountPresent(pod.Spec.Containers[0].VolumeMounts)
			pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts[:index], pod.Spec.Containers[0].VolumeMounts[index+1:]...)
		}

		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, runnerSpec.VolumeMounts...)
	}

	if len(runnerSpec.Volumes) != 0 {
		// if operator provides a work volume. use that
		isPresent, _ := workVolumePresent(runnerSpec.Volumes)
		if isPresent {
			_, index := workVolumePresent(pod.Spec.Volumes)

			// remove work volume since it will be provided from runnerSpec.Volumes
			// if we don't remove it here we would get a duplicate key error, i.e. two volumes named work
			pod.Spec.Volumes = append(pod.Spec.Volumes[:index], pod.Spec.Volumes[index+1:]...)
		}

		pod.Spec.Volumes = append(pod.Spec.Volumes, runnerSpec.Volumes...)
	}
	if len(runnerSpec.InitContainers) != 0 {
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, runnerSpec.InitContainers...)
	}

	if runnerSpec.NodeSelector != nil {
		pod.Spec.NodeSelector = runnerSpec.NodeSelector
	}
	if runnerSpec.ServiceAccountName != "" {
		pod.Spec.ServiceAccountName = runnerSpec.ServiceAccountName
	}
	if runnerSpec.AutomountServiceAccountToken != nil {
		pod.Spec.AutomountServiceAccountToken = runnerSpec.AutomountServiceAccountToken
	}

	if len(runnerSpec.SidecarContainers) != 0 {
		pod.Spec.Containers = append(pod.Spec.Containers, runnerSpec.SidecarContainers...)
	}

	if len(runnerSpec.ImagePullSecrets) != 0 {
		pod.Spec.ImagePullSecrets = runnerSpec.ImagePullSecrets
	}

	if runnerSpec.Affinity != nil {
		pod.Spec.Affinity = runnerSpec.Affinity
	}

	if len(runnerSpec.Tolerations) != 0 {
		pod.Spec.Tolerations = runnerSpec.Tolerations
	}

	if runnerSpec.PriorityClassName != "" {
		pod.Spec.PriorityClassName = runnerSpec.PriorityClassName
	}

	if len(runnerSpec.TopologySpreadConstraints) != 0 {
		pod.Spec.TopologySpreadConstraints = runnerSpec.TopologySpreadConstraints
	}

	if len(runnerSpec.EphemeralContainers) != 0 {
		pod.Spec.EphemeralContainers = runnerSpec.EphemeralContainers
	}

	if runnerSpec.TerminationGracePeriodSeconds != nil {
		pod.Spec.TerminationGracePeriodSeconds = runnerSpec.TerminationGracePeriodSeconds
	}

	if len(runnerSpec.HostAliases) != 0 {
		pod.Spec.HostAliases = runnerSpec.HostAliases
	}

	if runnerSpec.DnsConfig != nil {
		pod.Spec.DNSConfig = runnerSpec.DnsConfig
	}

	if runnerSpec.RuntimeClassName != nil {
		pod.Spec.RuntimeClassName = runnerSpec.RuntimeClassName
	}

	pod.ObjectMeta.Name = runner.ObjectMeta.Name

	// Inject the registration token and the runner name
	updated := mutatePod(&pod, runner.Status.Registration.Token)

	if err := ctrl.SetControllerReference(&runner, updated, r.Scheme); err != nil {
		return pod, err
	}

	return *updated, nil
}

func mutatePod(pod *corev1.Pod, token string) *corev1.Pod {
	updated := pod.DeepCopy()

	if getRunnerEnv(pod, EnvVarRunnerName) == "" {
		setRunnerEnv(updated, EnvVarRunnerName, pod.ObjectMeta.Name)
	}

	if getRunnerEnv(pod, EnvVarRunnerToken) == "" {
		setRunnerEnv(updated, EnvVarRunnerToken, token)
	}

	return updated
}

func newRunnerPod(runnerName string, template corev1.Pod, runnerSpec v1alpha1.RunnerConfig, defaultRunnerImage string, defaultRunnerImagePullSecrets []string, defaultDockerImage, defaultDockerRegistryMirror string, githubBaseURL string) (corev1.Pod, error) {
	var (
		privileged                bool = true
		dockerdInRunner           bool = runnerSpec.DockerdWithinRunnerContainer != nil && *runnerSpec.DockerdWithinRunnerContainer
		dockerEnabled             bool = runnerSpec.DockerEnabled == nil || *runnerSpec.DockerEnabled
		ephemeral                 bool = runnerSpec.Ephemeral == nil || *runnerSpec.Ephemeral
		dockerdInRunnerPrivileged bool = dockerdInRunner
	)

	template = *template.DeepCopy()

	// This label selector is used by default when rd.Spec.Selector is empty.
	template.ObjectMeta.Labels = CloneAndAddLabel(template.ObjectMeta.Labels, LabelKeyRunnerSetName, runnerName)
	template.ObjectMeta.Labels = CloneAndAddLabel(template.ObjectMeta.Labels, LabelKeyPodMutation, LabelValuePodMutation)

	workDir := runnerSpec.WorkDir
	if workDir == "" {
		workDir = "/runner/_work"
	}

	var dockerRegistryMirror string
	if runnerSpec.DockerRegistryMirror == nil {
		dockerRegistryMirror = defaultDockerRegistryMirror
	} else {
		dockerRegistryMirror = *runnerSpec.DockerRegistryMirror
	}

	// Be aware some of the environment variables are used
	// in the runner entrypoint script
	env := []corev1.EnvVar{
		{
			Name:  EnvVarOrg,
			Value: runnerSpec.Organization,
		},
		{
			Name:  EnvVarRepo,
			Value: runnerSpec.Repository,
		},
		{
			Name:  EnvVarEnterprise,
			Value: runnerSpec.Enterprise,
		},
		{
			Name:  "RUNNER_LABELS",
			Value: strings.Join(runnerSpec.Labels, ","),
		},
		{
			Name:  "RUNNER_GROUP",
			Value: runnerSpec.Group,
		},
		{
			Name:  "DOCKER_ENABLED",
			Value: fmt.Sprintf("%v", dockerEnabled || dockerdInRunner),
		},
		{
			Name:  "DOCKERD_IN_RUNNER",
			Value: fmt.Sprintf("%v", dockerdInRunner),
		},
		{
			Name:  "GITHUB_URL",
			Value: githubBaseURL,
		},
		{
			Name:  "RUNNER_WORKDIR",
			Value: workDir,
		},
		{
			Name:  EnvVarEphemeral,
			Value: fmt.Sprintf("%v", ephemeral),
		},
	}

	var seLinuxOptions *corev1.SELinuxOptions
	if template.Spec.SecurityContext != nil {
		seLinuxOptions = template.Spec.SecurityContext.SELinuxOptions
		if seLinuxOptions != nil {
			privileged = false
			dockerdInRunnerPrivileged = false
		}
	}

	var runnerContainerIndex, dockerdContainerIndex int
	var runnerContainer, dockerdContainer *corev1.Container

	for i := range template.Spec.Containers {
		c := template.Spec.Containers[i]
		if c.Name == containerName {
			runnerContainerIndex = i
			runnerContainer = &c
		} else if c.Name == "docker" {
			dockerdContainerIndex = i
			dockerdContainer = &c
		}
	}

	if runnerContainer == nil {
		runnerContainerIndex = -1
		runnerContainer = &corev1.Container{
			Name: containerName,
			SecurityContext: &corev1.SecurityContext{
				// Runner need to run privileged if it contains DinD
				Privileged: &dockerdInRunnerPrivileged,
			},
		}
	}

	if dockerdContainer == nil {
		dockerdContainerIndex = -1
		dockerdContainer = &corev1.Container{
			Name: "docker",
		}
	}

	if runnerSpec.Image != "" {
		runnerContainer.Image = runnerSpec.Image
	}
	if runnerContainer.Image == "" {
		runnerContainer.Image = defaultRunnerImage
	}

	if runnerContainer.ImagePullPolicy == "" {
		runnerContainer.ImagePullPolicy = corev1.PullAlways
	}

	runnerContainer.Env = append(runnerContainer.Env, env...)

	if runnerContainer.SecurityContext == nil {
		runnerContainer.SecurityContext = &corev1.SecurityContext{}
	}

	if runnerContainer.SecurityContext.Privileged == nil {
		// Runner need to run privileged if it contains DinD
		runnerContainer.SecurityContext.Privileged = &dockerdInRunnerPrivileged
	}

	pod := template.DeepCopy()

	forceRunnerPodRestartPolicyNever(pod)

	if mtu := runnerSpec.DockerMTU; mtu != nil && dockerdInRunner {
		runnerContainer.Env = append(runnerContainer.Env, []corev1.EnvVar{
			{
				Name:  "MTU",
				Value: fmt.Sprintf("%d", *runnerSpec.DockerMTU),
			},
		}...)
	}

	if len(pod.Spec.ImagePullSecrets) == 0 && len(defaultRunnerImagePullSecrets) > 0 {
		// runner spec didn't provide custom values and default image pull secrets are provided
		for _, imagePullSecret := range defaultRunnerImagePullSecrets {
			pod.Spec.ImagePullSecrets = append(pod.Spec.ImagePullSecrets, corev1.LocalObjectReference{
				Name: imagePullSecret,
			})
		}
	}

	if dockerRegistryMirror != "" && dockerdInRunner {
		runnerContainer.Env = append(runnerContainer.Env, []corev1.EnvVar{
			{
				Name:  "DOCKER_REGISTRY_MIRROR",
				Value: dockerRegistryMirror,
			},
		}...)
	}

	//
	// /runner must be generated on runtime from /runnertmp embedded in the container image.
	//
	// When you're NOT using dindWithinRunner=true,
	// it must also be shared with the dind container as it seems like required to run docker steps.
	//
	// Setting VolumeSizeLimit to zero will disable /runner emptydir mount
	//
	// VolumeStorageMedium defines ways that storage can be allocated to a volume: "", "Memory", "HugePages", "HugePages-<size>"
	//

	runnerVolumeName := "runner"
	runnerVolumeMountPath := "/runner"
	runnerVolumeEmptyDir := &corev1.EmptyDirVolumeSource{}

	if runnerSpec.VolumeStorageMedium != nil {
		runnerVolumeEmptyDir.Medium = corev1.StorageMedium(*runnerSpec.VolumeStorageMedium)
	}

	if runnerSpec.VolumeSizeLimit != nil {
		runnerVolumeEmptyDir.SizeLimit = runnerSpec.VolumeSizeLimit
	}

	if runnerSpec.VolumeSizeLimit == nil || !runnerSpec.VolumeSizeLimit.IsZero() {
		pod.Spec.Volumes = append(pod.Spec.Volumes,
			corev1.Volume{
				Name: runnerVolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: runnerVolumeEmptyDir,
				},
			},
		)

		runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts,
			corev1.VolumeMount{
				Name:      runnerVolumeName,
				MountPath: runnerVolumeMountPath,
			},
		)
	}

	if !dockerdInRunner && dockerEnabled {
		if runnerSpec.VolumeSizeLimit != nil && runnerSpec.VolumeSizeLimit.IsZero() {
			return *pod, fmt.Errorf(
				"%s volume can't be disabled because it is required to share the working directory between the runner and the dockerd containers",
				runnerVolumeName,
			)
		}

		if ok, _ := workVolumePresent(pod.Spec.Volumes); !ok {
			pod.Spec.Volumes = append(pod.Spec.Volumes,
				corev1.Volume{
					Name: "work",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			)
		}

		pod.Spec.Volumes = append(pod.Spec.Volumes,
			corev1.Volume{
				Name: "certs-client",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)

		if ok, _ := workVolumeMountPresent(runnerContainer.VolumeMounts); !ok {
			runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts,
				corev1.VolumeMount{
					Name:      "work",
					MountPath: workDir,
				},
			)
		}

		runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts,
			corev1.VolumeMount{
				Name:      "certs-client",
				MountPath: "/certs/client",
				ReadOnly:  true,
			},
		)

		runnerContainer.Env = append(runnerContainer.Env, []corev1.EnvVar{
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

		// Determine the volume mounts assigned to the docker sidecar. In case extra mounts are included in the RunnerSpec, append them to the standard
		// set of mounts. See https://github.com/actions-runner-controller/actions-runner-controller/issues/435 for context.
		dockerVolumeMounts := []corev1.VolumeMount{
			{
				Name:      runnerVolumeName,
				MountPath: runnerVolumeMountPath,
			},
			{
				Name:      "certs-client",
				MountPath: "/certs/client",
			},
		}

		mountPresent, _ := workVolumeMountPresent(dockerdContainer.VolumeMounts)
		if !mountPresent {
			dockerVolumeMounts = append(dockerVolumeMounts, corev1.VolumeMount{
				Name:      "work",
				MountPath: workDir,
			})
		}

		if dockerdContainer.Image == "" {
			dockerdContainer.Image = defaultDockerImage
		}

		dockerdContainer.Env = append(dockerdContainer.Env, corev1.EnvVar{
			Name:  "DOCKER_TLS_CERTDIR",
			Value: "/certs",
		})

		if dockerdContainer.SecurityContext == nil {
			dockerdContainer.SecurityContext = &corev1.SecurityContext{
				Privileged:     &privileged,
				SELinuxOptions: seLinuxOptions,
			}
		}

		dockerdContainer.VolumeMounts = append(dockerdContainer.VolumeMounts, dockerVolumeMounts...)

		if mtu := runnerSpec.DockerMTU; mtu != nil {
			dockerdContainer.Env = append(dockerdContainer.Env, []corev1.EnvVar{
				// See https://docs.docker.com/engine/security/rootless/
				{
					Name:  "DOCKERD_ROOTLESS_ROOTLESSKIT_MTU",
					Value: fmt.Sprintf("%d", *runnerSpec.DockerMTU),
				},
			}...)

			dockerdContainer.Args = append(dockerdContainer.Args,
				"--mtu",
				fmt.Sprintf("%d", *runnerSpec.DockerMTU),
			)
		}

		if dockerRegistryMirror != "" {
			dockerdContainer.Args = append(dockerdContainer.Args,
				fmt.Sprintf("--registry-mirror=%s", dockerRegistryMirror),
			)
		}
	}

	if runnerContainerIndex == -1 {
		pod.Spec.Containers = append([]corev1.Container{*runnerContainer}, pod.Spec.Containers...)

		if dockerdContainerIndex != -1 {
			dockerdContainerIndex++
		}
	} else {
		pod.Spec.Containers[runnerContainerIndex] = *runnerContainer
	}

	if !dockerdInRunner && dockerEnabled {
		if dockerdContainerIndex == -1 {
			pod.Spec.Containers = append(pod.Spec.Containers, *dockerdContainer)
		} else {
			pod.Spec.Containers[dockerdContainerIndex] = *dockerdContainer
		}
	}

	return *pod, nil
}

func (r *RunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	name := "runner-controller"
	if r.Name != "" {
		name = r.Name
	}

	r.Recorder = mgr.GetEventRecorderFor(name)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Runner{}).
		Owns(&corev1.Pod{}).
		Named(name).
		Complete(r)
}

func addFinalizer(finalizers []string, finalizerName string) ([]string, bool) {
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

func removeFinalizer(finalizers []string, finalizerName string) ([]string, bool) {
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

func workVolumePresent(items []corev1.Volume) (bool, int) {
	for index, item := range items {
		if item.Name == "work" {
			return true, index
		}
	}
	return false, 0
}

func workVolumeMountPresent(items []corev1.VolumeMount) (bool, int) {
	for index, item := range items {
		if item.Name == "work" {
			return true, index
		}
	}
	return false, 0
}
