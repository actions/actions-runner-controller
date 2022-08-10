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
	"errors"
	"fmt"
	"strings"

	actionsv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/go-logr/logr"
	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// TODO: Replace with shared image.
	image                        = "ghcr.io/cory-miller/autoscaler-prototype"
	name                         = "autoscaler-prototype"
	autoscalingRunnerSetOwnerKey = ".metadata.controller"
)

var (
	labels = client.MatchingLabels{
		"app": "autoscaler",
	}
)

// AutoscalingRunnerSetReconciler reconciles a AutoscalingRunnerSet object
type AutoscalingRunnerSetReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func getAutoscalerApplicationPodRef(namespace, autoscalerImage, org, repo, scaleSet, token string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v-%v", name, scaleSet),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  name,
					Image: autoscalerImage,
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

// runnerjobs is added to give implicit permission to the role+rolebinding.
// It would be probably be better to do this another way if possible.

//+kubebuilder:rbac:groups=core,resources=namespaces;pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=namespaces/status;pods/status,verbs=get
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=autoscalingrunnersets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=autoscalingrunnersets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=autoscalingrunnersets/finalizers,verbs=update
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerjobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=actions.summerwind.dev,resources=runnerjobs/status,verbs=get
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=role;rolebinding,verbs=get;list;watch;create;update;patch;delete;escalate
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=role/status;rolebinding/status,verbs=get

// Reconcile a AutoscalingRunnerSet resource to meet its desired spec.
func (r *AutoscalingRunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	kvlog := r.Log.WithValues("autoscalingrunnerset", req.NamespacedName)

	var runnerSet actionsv1alpha1.AutoscalingRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &runnerSet); err != nil {
		kvlog.Error(err, "unable to fetch AutoscalingRunnerSet")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !runnerSet.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	namespaceForManagement := runnerSet.Namespace

	// Start of reconciliation for Autoscaler pod.
	var childPods corev1.PodList
	if err := r.List(ctx, &childPods, client.InNamespace(req.Namespace), client.MatchingFields{autoscalingRunnerSetOwnerKey: req.Name}, labels); err != nil {
		kvlog.Error(err, "unable to list child pods")
		return ctrl.Result{}, err
	}

	switch len(childPods.Items) {
	case 0:
		// Create Autoscaler pod
		var c github.Config
		if err := envconfig.Process("github", &c); err != nil {
			return ctrl.Result{}, err
		}

		autoscalerImage := runnerSet.Spec.AutoscalerImage
		if strings.TrimSpace(autoscalerImage) == "" {
			autoscalerImage = image
		}

		pod := getAutoscalerApplicationPodRef(namespaceForManagement, autoscalerImage, runnerSet.Spec.RunnerOrg, runnerSet.Spec.RunnerRepo, runnerSet.Spec.RunnerScaleSet, c.Token)

		if err := ctrl.SetControllerReference(&runnerSet, pod, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, pod); err != nil {
			kvlog.Error(err, "unable to create Autoscaler for AutoscalingRunnerSet", "pod", pod)
			return ctrl.Result{}, err
		}

		kvlog.Info("Created pod", "podName", pod.ObjectMeta.Name)
	case 1:
		childPod := childPods.Items[0]

		// Give the pod more time to start.
		if childPod.Status.Phase == corev1.PodPending {
			return ctrl.Result{Requeue: true}, nil
		}

		// Set active ref to this pod.
		if childPod.Status.Phase == corev1.PodRunning {
			podRef, err := reference.GetReference(r.Scheme, &childPod)
			if err != nil {
				kvlog.Error(err, "unable to make reference to active job", "job", childPod)
				return ctrl.Result{}, err
			}

			runnerSet.Status.ActiveAutoscaler = *podRef

			break
		}

		// Clean up old pods.
		if childPod.Status.Phase == corev1.PodSucceeded || childPod.Status.Phase == corev1.PodFailed {
			if err := r.Delete(ctx, &runnerSet); err != nil {
				kvlog.Info("failed to delete runner set", "AutoscalingRunnerSet", runnerSet.Name)
				return ctrl.Result{Requeue: true}, nil
			}

			break
		}

		if childPod.Status.Phase == corev1.PodUnknown {
			kvlog.Info("Pod is in unknown status", "AutoscalingRunnerSet", runnerSet.Name)
			return ctrl.Result{Requeue: true}, nil
		}

		kvlog.Info("Pod is in unexpected status", "AutoscalingRunnerSet", runnerSet.Name, "status", childPod.Status.Phase)
	default:
		// Tell the user to intervene
		kvlog.Error(errors.New("AutoscalingRunnerSet cannot reconcile"), "multiple Autoscaler applications are detected. manual clean-up required")
	}
	// End of reconciliation for Autoscaler pod.

	childNamespaceType := types.NamespacedName{
		Namespace: runnerSet.Spec.RunnerScaleSet,
		Name:      runnerSet.Spec.RunnerScaleSet,
	}

	// Start of reconciliation for RunnerJob namespace.
	if err := r.Get(ctx, childNamespaceType, &corev1.Namespace{}); err != nil {
		childNamespace := &corev1.Namespace{}
		childNamespace.Name = childNamespaceType.Name
		childNamespace.Namespace = childNamespaceType.Namespace
		if err := r.Create(ctx, childNamespace); err != nil {
			kvlog.Error(err, "could not create namespace for runners")
			return ctrl.Result{}, nil
		}
	}
	// End of reconciliation for RunnerJob namespace.

	roleName := fmt.Sprintf("%v-runner-creator", runnerSet.Spec.RunnerScaleSet)

	// Start of reconciliation for RunnerJob RBAC.
	if err := r.Get(ctx, childNamespaceType, &rbacv1.Role{}); err != nil {
		role := &rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Role",
				APIVersion: "rbac.authorization.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      roleName,
				Namespace: namespaceForManagement,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:           []string{"*"},
					APIGroups:       []string{"batch"},
					Resources:       []string{"*"},
					ResourceNames:   []string{},
					NonResourceURLs: []string{},
				},
			},
		}
		if err := r.Create(ctx, role); err != nil {
			kvlog.Error(err, "could not create role for runner creation")
			return ctrl.Result{}, nil
		}
	}

	if err := r.Get(ctx, childNamespaceType, &rbacv1.RoleBinding{}); err != nil {
		rolebinding := &rbacv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				Kind:       "RoleBinding",
				APIVersion: "rbac.authorization.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%v-rolebinding", roleName),
				Namespace: namespaceForManagement,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      "default",
					Namespace: namespaceForManagement,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io/v1",
				Name:     roleName,
				Kind:     "Role",
			},
		}
		if err := r.Create(ctx, rolebinding); err != nil {
			kvlog.Error(err, "could not create rolebinding for runner creation")
			return ctrl.Result{}, nil
		}
	}
	// End of reconciliation for RunnerJob namespace and RBAC.

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutoscalingRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, autoscalingRunnerSetOwnerKey, func(rawObj client.Object) []string {

		groupVersion := actionsv1alpha1.GroupVersion.String()

		// grab the job object, extract the owner...
		pod, ok := rawObj.(*corev1.Pod)
		if ok {
			owner := metav1.GetControllerOf(pod)
			if owner == nil {
				return nil
			}

			// ...make sure it's a Pod...
			if owner.APIVersion != groupVersion || owner.Kind != "AutoscalingRunnerSet" {
				return nil
			}

			// ...and if so, return it
			return []string{owner.Name}
		}

		namespace, ok := rawObj.(*corev1.Namespace)
		if ok {
			owner := metav1.GetControllerOf(namespace)
			if owner == nil {
				return nil
			}

			// ...make sure it's a Namespace...
			if owner.APIVersion != groupVersion || owner.Kind != "AutoscalingRunnerSet" {
				return nil
			}

			// ...and if so, return it
			return []string{owner.Name}
		}

		return []string{}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&actionsv1alpha1.AutoscalingRunnerSet{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Namespace{}).
		// Below does not work unless this controller has cluster scoped role.list and rolebinding.list.
		// Don't think we want to force that, but it does mean we won't reconcile changes to these resource types.
		// Owns(&rbacv1.Role{}).
		// Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}
