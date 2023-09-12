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

package actionssummerwindnet

import (
	"context"
	"errors"
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/hash"
	"github.com/go-logr/logr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
)

const (
	containerName = "runner"
	finalizerName = "runner.actions.summerwind.dev"

	LabelKeyPodTemplateHash = "pod-template-hash"

	retryDelayOnGitHubAPIRateLimitError = 30 * time.Second

	EnvVarOrg        = "RUNNER_ORG"
	EnvVarRepo       = "RUNNER_REPO"
	EnvVarGroup      = "RUNNER_GROUP"
	EnvVarLabels     = "RUNNER_LABELS"
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
	GitHubClient                *MultiGitHubClient
	Name                        string
	RegistrationRecheckInterval time.Duration
	RegistrationRecheckJitter   time.Duration
	UnregistrationRetryDelay    time.Duration

	RunnerPodDefaults RunnerPodDefaults
}

type RunnerPodDefaults struct {
	RunnerImage            string
	RunnerImagePullSecrets []string
	DockerImage            string
	DockerRegistryMirror   string
	// The default Docker group ID to use for the dockerd sidecar container.
	// Ubuntu 20.04 runner images assumes 1001 and the 22.04 variant assumes 121 by default.
	DockerGID string

	UseRunnerStatusUpdateHook bool
}

// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.summerwind.dev,resources=runners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=create;delete;get
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=create;delete;get
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create;delete;get

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

		r.GitHubClient.DeinitForRunner(&runner)

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

	if (runner.Status.Phase != phase || runner.Status.Ready != ready) && !r.RunnerPodDefaults.UseRunnerStatusUpdateHook || runner.Status.Phase == "" && r.RunnerPodDefaults.UseRunnerStatusUpdateHook {
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
					break
				}
			}
		}

		if pod.DeletionTimestamp != nil && !pod.DeletionTimestamp.IsZero() && len(pod.Status.ContainerStatuses) == 0 {
			// This falls into cases where the pod is stuck with pod status like the below:
			//
			//   status:
			//     conditions:
			//     - lastProbeTime: null
			//       lastTransitionTime: "2022-11-20T07:58:05Z"
			//       message: 'binding rejected: running Bind plugin "DefaultBinder": Operation cannot
			//         be fulfilled on pods/binding "org-runnerdeploy-l579v-qx5p2": pod org-runnerdeploy-l579v-qx5p2
			//         is being deleted, cannot be assigned to a host'
			//       reason: SchedulerError
			//       status: "False"
			//       type: PodScheduled
			//     phase: Pending
			//     qosClass: BestEffort
			//
			// ARC usually waits for the registration timeout to elapse when the pod is terminated before getting scheduled onto a node,
			// assuming there can be a race condition between ARC and Kubernetes where Kubernetes schedules the pod while ARC is deleting the pod,
			// which may end up with non-gracefully terminating the runner.
			//
			// However, Kubernetes seems to not schedule the pod after observing status like the above.
			// This if-block is therefore needed to prevent ARC from unnecessarily waiting for the registration timeout to happen.
			stopped = true
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

	needsServiceAccount := runner.Spec.ServiceAccountName == "" && (r.RunnerPodDefaults.UseRunnerStatusUpdateHook || runner.Spec.ContainerMode == "kubernetes")
	if needsServiceAccount {
		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      runner.ObjectMeta.Name,
				Namespace: runner.ObjectMeta.Namespace,
			},
		}
		if res := r.createObject(ctx, serviceAccount, serviceAccount.ObjectMeta, &runner, log); res != nil {
			return *res, nil
		}

		rules := []rbacv1.PolicyRule{}

		if r.RunnerPodDefaults.UseRunnerStatusUpdateHook {
			rules = append(rules, []rbacv1.PolicyRule{
				{
					APIGroups:     []string{"actions.summerwind.dev"},
					Resources:     []string{"runners/status"},
					Verbs:         []string{"get", "update", "patch"},
					ResourceNames: []string{runner.ObjectMeta.Name},
				},
			}...)
		}

		if runner.Spec.ContainerMode == "kubernetes" {
			// Permissions based on https://github.com/actions/runner-container-hooks/blob/main/packages/k8s/README.md
			rules = append(rules, []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"pods"},
					Verbs:     []string{"get", "list", "create", "delete"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"pods/exec"},
					Verbs:     []string{"get", "create"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"pods/log"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{"batch"},
					Resources: []string{"jobs"},
					Verbs:     []string{"get", "list", "create", "delete"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"get", "list", "create", "delete"},
				},
			}...)
		}

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      runner.ObjectMeta.Name,
				Namespace: runner.ObjectMeta.Namespace,
			},
			Rules: rules,
		}
		if res := r.createObject(ctx, role, role.ObjectMeta, &runner, log); res != nil {
			return *res, nil
		}

		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      runner.ObjectMeta.Name,
				Namespace: runner.ObjectMeta.Namespace,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     runner.ObjectMeta.Name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      runner.ObjectMeta.Name,
					Namespace: runner.ObjectMeta.Namespace,
				},
			},
		}
		if res := r.createObject(ctx, roleBinding, roleBinding.ObjectMeta, &runner, log); res != nil {
			return *res, nil
		}
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

		errMsg := fmt.Sprintf("Failed to create pod resource: %v", err)
		r.Recorder.Event(&runner, corev1.EventTypeWarning, "FailedCreatePod", errMsg)

		newRunner := runner.DeepCopy()
		newRunner.Status.Phase = "Failed"
		newRunner.Status.Message = errMsg

		if err := r.Status().Patch(ctx, newRunner, client.MergeFrom(&runner)); err != nil {
			r.Recorder.Event(&runner, corev1.EventTypeWarning, "FailedUpdateRunner", fmt.Sprintf("Failed to update runner resource: %v", err))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	r.Recorder.Event(&runner, corev1.EventTypeNormal, "PodCreated", fmt.Sprintf("Created pod '%s'", newPod.Name))
	log.Info("Created runner pod", "repository", runner.Spec.Repository)

	return ctrl.Result{}, nil
}

func (r *RunnerReconciler) createObject(ctx context.Context, obj client.Object, meta metav1.ObjectMeta, runner *v1alpha1.Runner, log logr.Logger) *ctrl.Result {
	kind := strings.Split(reflect.TypeOf(obj).String(), ".")[1]
	if err := ctrl.SetControllerReference(runner, obj, r.Scheme); err != nil {
		log.Error(err, fmt.Sprintf("Could not add owner reference to %s %s. %s", kind, meta.Name, err.Error()))
		return &ctrl.Result{Requeue: true}
	}
	if err := r.Create(ctx, obj); err != nil {
		if kerrors.IsAlreadyExists(err) {
			log.Info(fmt.Sprintf("Failed to create %s %s as it already exists. Reusing existing %s", kind, meta.Name, kind))
			r.Recorder.Event(runner, corev1.EventTypeNormal, fmt.Sprintf("%sReused", kind), fmt.Sprintf("Reused %s '%s'", kind, meta.Name))
			return nil
		}

		log.Error(err, fmt.Sprintf("Retrying as failed to create %s %s resource", kind, meta.Name))
		return &ctrl.Result{Requeue: true}
	}
	r.Recorder.Event(runner, corev1.EventTypeNormal, fmt.Sprintf("%sCreated", kind), fmt.Sprintf("Created %s '%s'", kind, meta.Name))
	log.Info(fmt.Sprintf("Created %s", kind), "name", meta.Name)
	return nil
}

func (r *RunnerReconciler) updateRegistrationToken(ctx context.Context, runner v1alpha1.Runner) (bool, error) {
	if runner.IsRegisterable() {
		return false, nil
	}

	log := r.Log.WithValues("runner", runner.Name)

	ghc, err := r.GitHubClient.InitForRunner(ctx, &runner)
	if err != nil {
		return false, err
	}

	rt, err := ghc.GetRegistrationToken(ctx, runner.Spec.Enterprise, runner.Spec.Organization, runner.Spec.Repository, runner.Name)
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

	ghc, err := r.GitHubClient.InitForRunner(context.Background(), &runner)
	if err != nil {
		return corev1.Pod{}, err
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
	//     See https://github.com/actions/actions-runner-controller/issues/143 for more context.
	labels[LabelKeyPodTemplateHash] = hash.FNVHashStringObjects(
		filterLabels(runner.ObjectMeta.Labels, LabelKeyRunnerTemplateHash),
		runner.ObjectMeta.Annotations,
		runner.Spec,
		ghc.GithubBaseURL,
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

	if runner.Spec.ContainerMode == "kubernetes" {
		workDir := runner.Spec.WorkDir
		if workDir == "" {
			workDir = "/runner/_work"
		}
		if err := applyWorkVolumeClaimTemplateToPod(&template, runner.Spec.WorkVolumeClaimTemplate, workDir); err != nil {
			return corev1.Pod{}, err
		}
	}

	pod, err := newRunnerPodWithContainerMode(runner.Spec.ContainerMode, template, runner.Spec.RunnerConfig, ghc.GithubBaseURL, r.RunnerPodDefaults)
	if err != nil {
		return pod, err
	}

	// Customize the pod spec according to the runner spec
	runnerSpec := runner.Spec

	if len(runnerSpec.VolumeMounts) != 0 {
		// if operater provides a work volume mount, use that
		isPresent, _ := workVolumeMountPresent(runnerSpec.VolumeMounts)
		if isPresent {
			if runnerSpec.ContainerMode == "kubernetes" {
				return pod, errors.New("volume mount \"work\" should be specified by workVolumeClaimTemplate in container mode kubernetes")
			}

			podSpecIsPresent, index := workVolumeMountPresent(pod.Spec.Containers[0].VolumeMounts)
			if podSpecIsPresent {
				// remove work volume since it will be provided from runnerSpec.Volumes
				// if we don't remove it here we would get a duplicate key error, i.e. two volumes named work
				pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts[:index], pod.Spec.Containers[0].VolumeMounts[index+1:]...)
			}
		}

		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, runnerSpec.VolumeMounts...)
	}

	if len(runnerSpec.Volumes) != 0 {
		// if operator provides a work volume. use that
		isPresent, _ := workVolumePresent(runnerSpec.Volumes)
		if isPresent {
			if runnerSpec.ContainerMode == "kubernetes" {
				return pod, errors.New("volume \"work\" should be specified by workVolumeClaimTemplate in container mode kubernetes")
			}

			podSpecIsPresent, index := workVolumePresent(pod.Spec.Volumes)
			if podSpecIsPresent {
				// remove work volume since it will be provided from runnerSpec.Volumes
				// if we don't remove it here we would get a duplicate key error, i.e. two volumes named work
				pod.Spec.Volumes = append(pod.Spec.Volumes[:index], pod.Spec.Volumes[index+1:]...)
			}
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
	} else if r.RunnerPodDefaults.UseRunnerStatusUpdateHook || runner.Spec.ContainerMode == "kubernetes" {
		pod.Spec.ServiceAccountName = runner.ObjectMeta.Name
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

	if runnerSpec.DnsPolicy != "" {
		pod.Spec.DNSPolicy = runnerSpec.DnsPolicy
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

func runnerHookEnvs(pod *corev1.Pod) ([]corev1.EnvVar, error) {
	isRequireSameNode, err := isRequireSameNode(pod)
	if err != nil {
		return nil, err
	}

	return []corev1.EnvVar{
		{
			Name:  "ACTIONS_RUNNER_CONTAINER_HOOKS",
			Value: defaultRunnerHookPath,
		},
		{
			Name:  "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER",
			Value: "true",
		},
		{
			Name: "ACTIONS_RUNNER_POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: "ACTIONS_RUNNER_JOB_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name:  "ACTIONS_RUNNER_REQUIRE_SAME_NODE",
			Value: strconv.FormatBool(isRequireSameNode),
		},
	}, nil
}

func newRunnerPodWithContainerMode(containerMode string, template corev1.Pod, runnerSpec v1alpha1.RunnerConfig, githubBaseURL string, d RunnerPodDefaults) (corev1.Pod, error) {
	var (
		privileged                bool = true
		dockerdInRunner           bool = runnerSpec.DockerdWithinRunnerContainer != nil && *runnerSpec.DockerdWithinRunnerContainer
		dockerEnabled             bool = runnerSpec.DockerEnabled == nil || *runnerSpec.DockerEnabled
		ephemeral                 bool = runnerSpec.Ephemeral == nil || *runnerSpec.Ephemeral
		dockerdInRunnerPrivileged bool = dockerdInRunner

		defaultRunnerImage            = d.RunnerImage
		defaultRunnerImagePullSecrets = d.RunnerImagePullSecrets
		defaultDockerImage            = d.DockerImage
		defaultDockerRegistryMirror   = d.DockerRegistryMirror
		useRunnerStatusUpdateHook     = d.UseRunnerStatusUpdateHook
	)

	const (
		varRunVolumeName      = "var-run"
		varRunVolumeMountPath = "/run"
	)

	if containerMode == "kubernetes" {
		dockerdInRunner = false
		dockerEnabled = false
		dockerdInRunnerPrivileged = false
	}

	template = *template.DeepCopy()

	// This label selector is used by default when rd.Spec.Selector is empty.
	template.ObjectMeta.Labels = CloneAndAddLabel(template.ObjectMeta.Labels, LabelKeyRunner, "")
	template.ObjectMeta.Labels = CloneAndAddLabel(template.ObjectMeta.Labels, LabelKeyPodMutation, LabelValuePodMutation)
	if runnerSpec.GitHubAPICredentialsFrom != nil {
		template.ObjectMeta.Annotations = CloneAndAddLabel(template.ObjectMeta.Annotations, annotationKeyGitHubAPICredsSecret, runnerSpec.GitHubAPICredentialsFrom.SecretRef.Name)
	}

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

	if runnerSpec.DockerVarRunVolumeSizeLimit == nil {
		runnerSpec.DockerVarRunVolumeSizeLimit = resource.NewScaledQuantity(1, resource.Mega)

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
			Name:  EnvVarLabels,
			Value: strings.Join(runnerSpec.Labels, ","),
		},
		{
			Name:  EnvVarGroup,
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
		{
			Name:  "RUNNER_STATUS_UPDATE_HOOK",
			Value: fmt.Sprintf("%v", useRunnerStatusUpdateHook),
		},
		{
			Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
			Value: fmt.Sprintf("actions-runner-controller/%s", build.Version),
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

	if containerMode == "kubernetes" {
		if dockerdContainer != nil {
			template.Spec.Containers = append(template.Spec.Containers[:dockerdContainerIndex], template.Spec.Containers[dockerdContainerIndex+1:]...)
		}
		if dockerdContainerIndex < runnerContainerIndex {
			runnerContainerIndex--
		}
		dockerdContainer = nil
		dockerdContainerIndex = -1
	}

	if runnerContainer == nil {
		runnerContainerIndex = -1
		runnerContainer = &corev1.Container{
			Name: containerName,
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
	if containerMode == "kubernetes" {
		hookEnvs, err := runnerHookEnvs(&template)
		if err != nil {
			return corev1.Pod{}, err
		}
		runnerContainer.Env = append(runnerContainer.Env, hookEnvs...)
	}

	if runnerContainer.SecurityContext == nil {
		runnerContainer.SecurityContext = &corev1.SecurityContext{}
	}

	// Runner need to run privileged if it contains DinD.
	// Do not explicitly set SecurityContext.Privileged to false which is default,
	// otherwise Windows pods don't get admitted on GKE.
	if dockerdInRunnerPrivileged {
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

		// explicitly invoke `dockerd` to avoid automatic TLS / TCP binding
		dockerdContainer.Args = append([]string{
			"dockerd",
			"--host=unix:///run/docker.sock",
		}, dockerdContainer.Args...)

		// this must match a GID for the user in the runner image
		// default matches GitHub Actions infra (and default runner images
		// for actions-runner-controller) so typically should not need to be
		// overridden
		if ok, _ := envVarPresent("DOCKER_GROUP_GID", dockerdContainer.Env); !ok {
			gid := d.DockerGID
			// We default to gid 121 for Ubuntu 22.04 images
			// See below for more details
			// - https://github.com/actions/actions-runner-controller/issues/2490#issuecomment-1501561923
			// - https://github.com/actions/actions-runner-controller/blob/8869ad28bb5a1daaedefe0e988571fe1fb36addd/runner/actions-runner.ubuntu-20.04.dockerfile#L14
			// - https://github.com/actions/actions-runner-controller/blob/8869ad28bb5a1daaedefe0e988571fe1fb36addd/runner/actions-runner.ubuntu-22.04.dockerfile#L12
			if strings.Contains(runnerContainer.Image, "22.04") {
				gid = "121"
			} else if strings.Contains(runnerContainer.Image, "20.04") {
				gid = "1001"
			}

			dockerdContainer.Env = append(dockerdContainer.Env,
				corev1.EnvVar{
					Name:  "DOCKER_GROUP_GID",
					Value: gid,
				})
		}
		dockerdContainer.Args = append(dockerdContainer.Args, "--group=$(DOCKER_GROUP_GID)")

		// ideally, we could mount the socket directly at `/var/run/docker.sock`
		// to use the default, but that's not practical since it won't exist
		// when the container starts, so can't use subPath on the volume mount
		runnerContainer.Env = append(runnerContainer.Env,
			corev1.EnvVar{
				Name:  "DOCKER_HOST",
				Value: "unix:///run/docker.sock",
			},
		)

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
				Name: varRunVolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium:    corev1.StorageMediumMemory,
						SizeLimit: runnerSpec.DockerVarRunVolumeSizeLimit,
					},
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

		if ok, _ := volumeMountPresent(varRunVolumeName, runnerContainer.VolumeMounts); !ok {
			runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts,
				corev1.VolumeMount{
					Name:      varRunVolumeName,
					MountPath: varRunVolumeMountPath,
				},
			)
		}

		// Determine the volume mounts assigned to the docker sidecar. In case extra mounts are included in the RunnerSpec, append them to the standard
		// set of mounts. See https://github.com/actions/actions-runner-controller/issues/435 for context.
		dockerVolumeMounts := []corev1.VolumeMount{
			{
				Name:      runnerVolumeName,
				MountPath: runnerVolumeMountPath,
			},
		}

		if p, _ := volumeMountPresent(varRunVolumeName, dockerdContainer.VolumeMounts); !p {
			dockerVolumeMounts = append(dockerVolumeMounts, corev1.VolumeMount{
				Name:      varRunVolumeName,
				MountPath: varRunVolumeMountPath,
			})
		}

		if p, _ := workVolumeMountPresent(dockerdContainer.VolumeMounts); !p {
			dockerVolumeMounts = append(dockerVolumeMounts, corev1.VolumeMount{
				Name:      "work",
				MountPath: workDir,
			})
		}

		if dockerdContainer.Image == "" {
			dockerdContainer.Image = defaultDockerImage
		}

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

			// This let dockerd to create container's network interface to have the specified MTU.
			// In other words, this is for setting com.docker.network.driver.mtu in the docker bridge options.
			// You can see the options by running `docker network inspect bridge`, where you will see something like the below when spec.dockerMTU=1400:
			//
			// "Options": {
			// 	 "com.docker.network.bridge.default_bridge": "true",
			// 	 "com.docker.network.bridge.enable_icc": "true",
			// 	 "com.docker.network.bridge.enable_ip_masquerade": "true",
			// 	 "com.docker.network.bridge.host_binding_ipv4": "0.0.0.0",
			// 	 "com.docker.network.bridge.name": "docker0",
			// 	 "com.docker.network.driver.mtu": "1400"
			// },
			//
			// See e.g. https://forums.docker.com/t/changing-mtu-value/74114 and https://mlohr.com/docker-mtu/ for more details.
			//
			// Note though, this doesn't immediately affect docker0's MTU, and the MTU of the docker network created with docker-create-network:
			// You can verity that by running `ip link` within the containers:
			//
			//   # ip link
			//   1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1000
			//   link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
			//   2: eth0@if1118: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue state UP
			//   link/ether c2:dd:e6:66:8e:8b brd ff:ff:ff:ff:ff:ff
			//   3: docker0: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN
			//   link/ether 02:42:ab:1c:83:69 brd ff:ff:ff:ff:ff:ff
			//   4: br-c5bf6c172bd7: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN
			//   link/ether 02:42:e2:91:13:1e brd ff:ff:ff:ff:ff:ff
			//
			// br-c5bf6c172bd7 is the interface that corresponds to the docker network created with docker-create-network.
			// We have another ARC feature to inherit the host's MTU to the docker networks:
			// https://github.com/actions/actions-runner-controller/pull/1201
			//
			// docker's MTU is updated to the specified MTU once any container is created.
			// You can verity that by running a random container from within the runner or dockerd containers:
			//
			// / # docker run -d busybox sh -c 'sleep 10'
			// e848e6acd6404ca0199e4d9c5ef485d88c974ddfb7aaf2359c66811f68cf5e42
			//
			// You'll now see the veth767f1a5@if7 got created with the MTU inherited by dockerd:
			//
			// / # ip link
			// 1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1000
			//     link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
			// 2: eth0@if1118: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue state UP
			//     link/ether c2:dd:e6:66:8e:8b brd ff:ff:ff:ff:ff:ff
			// 3: docker0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1400 qdisc noqueue state UP
			//     link/ether 02:42:ab:1c:83:69 brd ff:ff:ff:ff:ff:ff
			// 4: br-c5bf6c172bd7: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN
			//     link/ether 02:42:e2:91:13:1e brd ff:ff:ff:ff:ff:ff
			// 8: veth767f1a5@if7: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1400 qdisc noqueue master docker0 state UP
			//     link/ether 82:d5:08:28:d8:98 brd ff:ff:ff:ff:ff:ff
			//
			// # After 10 seconds sleep, you can see the container stops and the veth767f1a5@if7 interface got deleted:
			//
			// / # ip link
			// 1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1000
			//     link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
			// 2: eth0@if1118: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue state UP
			//     link/ether c2:dd:e6:66:8e:8b brd ff:ff:ff:ff:ff:ff
			// 3: docker0: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN
			//     link/ether 02:42:ab:1c:83:69 brd ff:ff:ff:ff:ff:ff
			// 4: br-c5bf6c172bd7: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc noqueue state DOWN
			//     link/ether 02:42:e2:91:13:1e brd ff:ff:ff:ff:ff:ff
			//
			// See https://github.com/moby/moby/issues/26382#issuecomment-246906331 for reference.
			//
			// Probably we'd better infer DockerMTU from the host's primary interface's MTU and docker0's MTU?
			// That's another story- if you want it, please start a thread in GitHub Discussions!
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

		dockerdContainer.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"/bin/sh", "-c",
						// A prestop hook can start before the dockerd start up, for example, when the docker init is still provisioning
						// the TLS key and  the cert to be used by dockerd.
						//
						// The author of this prestop script encountered issues where the prestophung for ten or more minutes on his cluster.
						// He realized that the hang happened when a prestop hook is executed while the docker init is provioning the key and cert.
						// Assuming it's due to that the SIGTERM sent by K8s after the prestop hook was ignored by the docker init at that time,
						// and it needed to wait until terminationGracePeriodSeconds to elapse before finally killing the container,
						// he wrote this script so that it tries to delay SIGTERM until dockerd starts and becomes ready for processing the signal.
						//
						// Also note that we don't need to run `pkill dockerd` at the end of the prehook script, as SIGTERM is sent by K8s after the prestop had completed.
						`timeout "${RUNNER_GRACEFUL_STOP_TIMEOUT:-15}" /bin/sh -c "echo 'Prestop hook started'; while [ -f /runner/.runner ]; do sleep 1; done; echo 'Waiting for dockerd to start'; while ! pgrep -x dockerd; do sleep 1; done; echo 'Prestop hook stopped'" >/proc/1/fd/1 2>&1`,
					},
				},
			},
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

func envVarPresent(name string, items []corev1.EnvVar) (bool, int) {
	for index, item := range items {
		if item.Name == name {
			return true, index
		}
	}
	return false, -1
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
	return volumeMountPresent("work", items)
}

func volumeMountPresent(name string, items []corev1.VolumeMount) (bool, int) {
	for index, item := range items {
		if item.Name == name {
			return true, index
		}
	}
	return false, -1
}

func applyWorkVolumeClaimTemplateToPod(pod *corev1.Pod, workVolumeClaimTemplate *v1alpha1.WorkVolumeClaimTemplate, workDir string) error {
	if workVolumeClaimTemplate == nil {
		return errors.New("work volume claim template must be specified in container mode kubernetes")
	}
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "work" {
			return fmt.Errorf("Work volume should not be specified in container mode kubernetes. workVolumeClaimTemplate field should be used instead.")
		}
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, workVolumeClaimTemplate.V1Volume())

	var runnerContainer *corev1.Container
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == "runner" {
			runnerContainer = &pod.Spec.Containers[i]
			break
		}
	}

	if runnerContainer == nil {
		return fmt.Errorf("runner container is not present when applying work volume claim template")
	}

	if isPresent, _ := workVolumeMountPresent(runnerContainer.VolumeMounts); isPresent {
		return fmt.Errorf("volume mount \"work\" should not be present on the runner container in container mode kubernetes")
	}

	runnerContainer.VolumeMounts = append(runnerContainer.VolumeMounts, workVolumeClaimTemplate.V1VolumeMount(workDir))

	return nil
}

// isRequireSameNode specifies for the runner in kubernetes mode wether it should
// schedule jobs to the same node where the runner is
//
// This function should only be called in containerMode: kubernetes
func isRequireSameNode(pod *corev1.Pod) (bool, error) {
	isPresent, index := workVolumePresent(pod.Spec.Volumes)
	if !isPresent {
		return true, errors.New("internal error: work volume mount must exist in containerMode: kubernetes")
	}

	if pod.Spec.Volumes[index].Ephemeral == nil || pod.Spec.Volumes[index].Ephemeral.VolumeClaimTemplate == nil {
		return true, errors.New("containerMode: kubernetes should have pod.Spec.Volumes[].Ephemeral.VolumeClaimTemplate set")
	}

	for _, accessMode := range pod.Spec.Volumes[index].Ephemeral.VolumeClaimTemplate.Spec.AccessModes {
		switch accessMode {
		case corev1.ReadWriteOnce:
			return true, nil
		case corev1.ReadWriteMany:
		default:
			return true, errors.New("actions-runner-controller supports ReadWriteOnce and ReadWriteMany modes only")
		}
	}
	return false, nil
}
