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

	actionsv1 "github.com/actions-runner-controller/actions-runner-controller/api/v1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namespace = "default"
	image     = "ghcr.io/cory-miller/autoscaler-prototype"
	name      = "autoscaler-prototype"
)

var (
	labels = client.MatchingLabels{
		"app": "autoscaler",
	}

	jobOwnerKey = ".metadata.controller"
)

// AutoscalingRunnerSetReconciler reconciles a AutoscalingRunnerSet object
type AutoscalingRunnerSetReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func getAutoscalerApplicationPodRef(org, repo, scaleSet, token string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v-%v", name, uuid.New().String()),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  name,
					Image: image,
					Env: []corev1.EnvVar{
						{
							Name:  "GITHUB_RUNNER_ORG",
							Value: org,
						},
						{
							Name:  "GITHUB_RUNNER_REPOSITORY",
							Value: repo,
						},
						{
							Name:  "GITHUB_RUNNER_SCALE_SET_NAME",
							Value: scaleSet,
						},
						{
							Name:  "GITHUB_TOKEN",
							Value: token,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
}

func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return true
		}
	}

	return false
}

//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=autoscalingrunnersets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=autoscalingrunnersets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=actions,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=actions,resources=pods/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AutoscalingRunnerSet object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.6.4/pkg/reconcile
func (r *AutoscalingRunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	klog := r.Log.WithValues("autoscalingrunnerset", req.NamespacedName)

	// Check for pods with label matching the autoscaler

	// 1: Load the CronJob by name

	var runnerSet actionsv1.AutoscalingRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &runnerSet); err != nil {
		klog.Error(err, "unable to fetch AutoscalingRunnerSet")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2: List all active jobs, and update the status

	var childPods corev1.PodList
	if err := r.List(ctx, &childPods, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name}, labels); err != nil {
		klog.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}

	activePods := []corev1.Pod{}

	// TODO(cory-miller): Track inactive/old Pods and remove them

	for _, pod := range childPods.Items {
		if pod.Name != name {
			klog.Info("%v is not a known autoscaler pod, skipping", pod.Name)
			continue
		}

		if !isPodReady(&pod) {
			klog.Info("%v is not ready, skipping", pod.Name)
			continue
		}

		activePods = append(activePods, pod)
	}

	runnerSet.Status.ActiveAutoscalers = nil
	for _, activePod := range activePods {
		podRef, err := reference.GetReference(r.Scheme, &activePod)
		if err != nil {
			klog.Error(err, "unable to make reference to active job", "job", activePod)
			continue
		}
		runnerSet.Status.ActiveAutoscalers = append(runnerSet.Status.ActiveAutoscalers, *podRef)
	}

	var c github.Config
	if err := envconfig.Process("github", &c); err != nil {
		return ctrl.Result{}, err
	}

	pod := getAutoscalerApplicationPodRef(runnerSet.Spec.RunnerOrg, runnerSet.Spec.RunnerRepo, runnerSet.Spec.RunnerScaleSet, c.Token)
	if err := r.Create(ctx, pod); err != nil {
		klog.Error(err, "unable to create Autoscaler for AutoscalingRunnerSet", "pod", pod)
		return ctrl.Result{}, err
	}

	klog.Info("Created pod", "podName", pod.ObjectMeta.Name)

	return ctrl.Result{}, nil
}

var (
	apiGVStr = actionsv1.GroupVersion.String()
)

// SetupWithManager sets up the controller with the Manager.
func (r *AutoscalingRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, jobOwnerKey, func(rawObj client.Object) []string {

		// grab the job object, extract the owner...
		pod := rawObj.(*corev1.Pod)
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			return nil
		}

		// ...make sure it's a Pod...
		if owner.APIVersion != apiGVStr || owner.Kind != "Pod" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&actionsv1.AutoscalingRunnerSet{}).
		Complete(r)
}
