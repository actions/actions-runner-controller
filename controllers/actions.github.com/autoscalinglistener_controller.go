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

package actionsgithubcom

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/actions/actions-runner-controller/github/actions"
	hash "github.com/actions/actions-runner-controller/hash"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	autoscalingListenerContainerName = "listener"
	autoscalingListenerFinalizerName = "autoscalinglistener.actions.github.com/finalizer"
)

// AutoscalingListenerReconciler reconciles a AutoscalingListener object
type AutoscalingListenerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	// ListenerMetricsAddr is address that the metrics endpoint binds to.
	// If it is set to "0", the metrics server is not started.
	ListenerMetricsAddr     string
	ListenerMetricsEndpoint string

	resourceBuilder resourceBuilder
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=create;delete;get;list;watch;update
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create;delete;get;list;watch
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners/finalizers,verbs=update

// Reconcile a AutoscalingListener resource to meet its desired spec.
func (r *AutoscalingListenerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("autoscalinglistener", req.NamespacedName)

	autoscalingListener := new(v1alpha1.AutoscalingListener)
	if err := r.Get(ctx, req.NamespacedName, autoscalingListener); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !autoscalingListener.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(autoscalingListener, autoscalingListenerFinalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("Deleting resources")
		done, err := r.cleanupResources(ctx, autoscalingListener, log)
		if err != nil {
			log.Error(err, "Failed to cleanup resources after deletion")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for resources to be deleted before removing finalizer")
			return ctrl.Result{Requeue: true}, nil
		}

		log.Info("Removing finalizer")
		err = patch(ctx, r.Client, autoscalingListener, func(obj *v1alpha1.AutoscalingListener) {
			controllerutil.RemoveFinalizer(obj, autoscalingListenerFinalizerName)
		})
		if err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully removed finalizer after cleanup")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(autoscalingListener, autoscalingListenerFinalizerName) {
		log.Info("Adding finalizer")
		if err := patch(ctx, r.Client, autoscalingListener, func(obj *v1alpha1.AutoscalingListener) {
			controllerutil.AddFinalizer(obj, autoscalingListenerFinalizerName)
		}); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	// Check if the AutoscalingRunnerSet exists
	var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: autoscalingListener.Spec.AutoscalingRunnerSetName}, &autoscalingRunnerSet); err != nil {
		log.Error(err, "Failed to find AutoscalingRunnerSet.",
			"namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			"name", autoscalingListener.Spec.AutoscalingRunnerSetName)
		return ctrl.Result{}, err
	}

	// Check if the GitHub config secret exists
	secret := new(corev1.Secret)
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: autoscalingListener.Spec.GitHubConfigSecret}, secret); err != nil {
		log.Error(err, "Failed to find GitHub config secret.",
			"namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			"name", autoscalingListener.Spec.GitHubConfigSecret)
		return ctrl.Result{}, err
	}

	// Create a mirror secret in the same namespace as the AutoscalingListener
	mirrorSecret := new(corev1.Secret)
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: scaleSetListenerSecretMirrorName(autoscalingListener)}, mirrorSecret); err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Unable to get listener secret mirror", "namespace", autoscalingListener.Namespace, "name", scaleSetListenerSecretMirrorName(autoscalingListener))
			return ctrl.Result{}, err
		}

		// Create a mirror secret for the listener pod in the Controller namespace for listener pod to use
		log.Info("Creating a mirror listener secret for the listener pod")
		return r.createSecretsForListener(ctx, autoscalingListener, secret, log)
	}

	// make sure the mirror secret is up to date
	mirrorSecretDataHash := mirrorSecret.Labels["secret-data-hash"]
	secretDataHash := hash.ComputeTemplateHash(secret.Data)
	if mirrorSecretDataHash != secretDataHash {
		log.Info("Updating mirror listener secret for the listener pod", "mirrorSecretDataHash", mirrorSecretDataHash, "secretDataHash", secretDataHash)
		return r.updateSecretsForListener(ctx, secret, mirrorSecret, log)
	}

	// Make sure the runner scale set listener service account is created for the listener pod in the controller namespace
	serviceAccount := new(corev1.ServiceAccount)
	if !ShouldSkipListenerSaCreation() {
		log.Info("Creating a service account for the listener pod")
		if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: scaleSetListenerServiceAccountName(autoscalingListener)}, serviceAccount); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Error(err, "Unable to get listener service accounts", "namespace", autoscalingListener.Namespace, "name", scaleSetListenerServiceAccountName(autoscalingListener))
				return ctrl.Result{}, err
			}

			// Create a service account for the listener pod in the controller namespace
			log.Info("Creating a service account for the listener pod")
			return r.createServiceAccountForListener(ctx, autoscalingListener, log)
		}
	} else {
		log.Info("Skipping listener service account creation, checking if provided one exists")
		serviceAccountName := autoscalingListener.Spec.Template.Spec.ServiceAccountName
		if serviceAccountName == "" {
			err := errors.New("Service Account name required for listener, but was not provided in the runnerset")
			log.Error(err, "Service Account name required for listener, but was not provided in the runnerset", "namespace", autoscalingListener.Namespace)
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: serviceAccountName}, serviceAccount); err != nil {
			log.Error(err, "Service Account not found in the namespace", "namespace", autoscalingListener.Namespace, "name", serviceAccountName)
			return ctrl.Result{}, errors.New("Listener's service account not found")
		}
	}

	// TODO: make sure the service account is up to date

	if !ShouldSkipListenerRbacSetup() {
		// Make sure the runner scale set listener role is created in the AutoscalingRunnerSet namespace
		log.Info("Creating role and role bindings for the listener pod")
		listenerRole := new(rbacv1.Role)
		if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: scaleSetListenerRoleName(autoscalingListener)}, listenerRole); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Error(err, "Unable to get listener role", "namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace, "name", scaleSetListenerRoleName(autoscalingListener))
				return ctrl.Result{}, err
			}

			// Create a role for the listener pod in the AutoScalingRunnerSet namespace
			log.Info("Creating a role for the listener pod")
			return r.createRoleForListener(ctx, autoscalingListener, log)
		}

		// Make sure the listener role has the up-to-date rules
		existingRuleHash := listenerRole.Labels["role-policy-rules-hash"]
		desiredRules := rulesForListenerRole([]string{autoscalingListener.Spec.EphemeralRunnerSetName})
		desiredRulesHash := hash.ComputeTemplateHash(&desiredRules)
		if existingRuleHash != desiredRulesHash {
			log.Info("Updating the listener role with the up-to-date rules")
			return r.updateRoleForListener(ctx, listenerRole, desiredRules, desiredRulesHash, log)
		}

		// Make sure the runner scale set listener role binding is created
		listenerRoleBinding := new(rbacv1.RoleBinding)
		if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: scaleSetListenerRoleName(autoscalingListener)}, listenerRoleBinding); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Error(err, "Unable to get listener role binding", "namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace, "name", scaleSetListenerRoleName(autoscalingListener))
				return ctrl.Result{}, err
			}

			// Create a role binding for the listener pod in the AutoScalingRunnerSet namespace
			log.Info("Creating a role binding for the service account and role")
			return r.createRoleBindingForListener(ctx, autoscalingListener, listenerRole, serviceAccount, log)
		}
	} else {
		log.Info("Skipping listener role and role binding creation")
	}

	// Create a secret containing proxy config if specified
	if autoscalingListener.Spec.Proxy != nil {
		proxySecret := new(corev1.Secret)
		if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: proxyListenerSecretName(autoscalingListener)}, proxySecret); err != nil {
			if !kerrors.IsNotFound(err) {
				log.Error(err, "Unable to get listener proxy secret", "namespace", autoscalingListener.Namespace, "name", proxyListenerSecretName(autoscalingListener))
				return ctrl.Result{}, err
			}

			// Create a mirror secret for the listener pod in the Controller namespace for listener pod to use
			log.Info("Creating a listener proxy secret for the listener pod")
			return r.createProxySecret(ctx, autoscalingListener, log)
		}
	}

	// TODO: make sure the role binding has the up-to-date role and service account

	listenerPod := new(corev1.Pod)
	if err := r.Get(ctx, client.ObjectKey{Namespace: autoscalingListener.Namespace, Name: autoscalingListener.Name}, listenerPod); err != nil {
		if !kerrors.IsNotFound(err) {
			log.Error(err, "Unable to get listener pod", "namespace", autoscalingListener.Namespace, "name", autoscalingListener.Name)
			return ctrl.Result{}, err
		}

		if err := r.publishRunningListener(autoscalingListener, false); err != nil {
			// If publish fails, URL is incorrect which means the listener pod would never be able to start
			return ctrl.Result{}, nil
		}

		// Create a listener pod in the controller namespace
		log.Info("Creating a listener pod")
		return r.createListenerPod(ctx, &autoscalingRunnerSet, autoscalingListener, serviceAccount, mirrorSecret, log)
	}

	// The listener pod failed might mean the mirror secret is out of date
	// Delete the listener pod and re-create it to make sure the mirror secret is up to date
	if listenerPod.Status.Phase == corev1.PodFailed && listenerPod.DeletionTimestamp.IsZero() {
		log.Info("Listener pod failed, deleting it and re-creating it", "namespace", listenerPod.Namespace, "name", listenerPod.Name, "reason", listenerPod.Status.Reason, "message", listenerPod.Status.Message)
		if err := r.Delete(ctx, listenerPod); err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Unable to delete the listener pod", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
			return ctrl.Result{}, err
		}
	}

	if listenerPod.Status.Phase == corev1.PodRunning {
		if err := r.publishRunningListener(autoscalingListener, true); err != nil {
			log.Error(err, "Unable to publish running listener", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
			// stop reconciling. We should never get to this point but if we do,
			// listener won't be able to start up, and the crash from the pod should
			// notify the reconciler again.
			return ctrl.Result{}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *AutoscalingListenerReconciler) cleanupResources(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (done bool, err error) {
	logger.Info("Cleaning up the listener pod")
	listenerPod := new(corev1.Pod)
	err = r.Get(ctx, types.NamespacedName{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, listenerPod)
	switch {
	case err == nil:
		if listenerPod.ObjectMeta.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener pod")
			if err := r.Delete(ctx, listenerPod); err != nil {
				return false, fmt.Errorf("failed to delete listener pod: %v", err)
			}
		}
		return false, nil
	case err != nil && !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener pods: %v", err)

	default: // NOT FOUND
		_ = r.publishRunningListener(autoscalingListener, false) // If error is returned, we never published metrics so it is safe to ignore
	}
	logger.Info("Listener pod is deleted")

	var secret corev1.Secret
	err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: scaleSetListenerConfigName(autoscalingListener)}, &secret)
	switch {
	case err == nil:
		if secret.ObjectMeta.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener config secret")
			if err := r.Delete(ctx, &secret); err != nil {
				return false, fmt.Errorf("failed to delete listener config secret: %v", err)
			}
		}
		return false, nil
	case err != nil && !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener config secret: %v", err)
	}

	if autoscalingListener.Spec.Proxy != nil {
		logger.Info("Cleaning up the listener proxy secret")
		proxySecret := new(corev1.Secret)
		err = r.Get(ctx, types.NamespacedName{Name: proxyListenerSecretName(autoscalingListener), Namespace: autoscalingListener.Namespace}, proxySecret)
		switch {
		case err == nil:
			if proxySecret.ObjectMeta.DeletionTimestamp.IsZero() {
				logger.Info("Deleting the listener proxy secret")
				if err := r.Delete(ctx, proxySecret); err != nil {
					return false, fmt.Errorf("failed to delete listener proxy secret: %v", err)
				}
			}
			return false, nil
		case err != nil && !kerrors.IsNotFound(err):
			return false, fmt.Errorf("failed to get listener proxy secret: %v", err)
		}
		logger.Info("Listener proxy secret is deleted")
	}

	if !ShouldSkipListenerRbacSetup() {
		logger.Info("Cleaning up the listener role and role binding")
		listenerRoleBinding := new(rbacv1.RoleBinding)
		err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: scaleSetListenerRoleName(autoscalingListener)}, listenerRoleBinding)
		switch {
		case err == nil:
			if listenerRoleBinding.ObjectMeta.DeletionTimestamp.IsZero() {
				logger.Info("Deleting the listener role binding")
				if err := r.Delete(ctx, listenerRoleBinding); err != nil {
					return false, fmt.Errorf("failed to delete listener role binding: %v", err)
				}
			}
			return false, nil
		case err != nil && !kerrors.IsNotFound(err):
			return false, fmt.Errorf("failed to get listener role binding: %v", err)
		}
		logger.Info("Listener role binding is deleted")

		listenerRole := new(rbacv1.Role)
		err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: scaleSetListenerRoleName(autoscalingListener)}, listenerRole)
		switch {
		case err == nil:
			if listenerRole.ObjectMeta.DeletionTimestamp.IsZero() {
				logger.Info("Deleting the listener role")
				if err := r.Delete(ctx, listenerRole); err != nil {
					return false, fmt.Errorf("failed to delete listener role: %v", err)
				}
			}
			return false, nil
		case err != nil && !kerrors.IsNotFound(err):
			return false, fmt.Errorf("failed to get listener role: %v", err)
		}
		logger.Info("Listener role is deleted")
	} else {
		logger.Info("Skipping listener role and role binding deletion")
	}

	if !ShouldSkipListenerSaCreation() {
		logger.Info("Cleaning up the listener service account")
		listenerSa := new(corev1.ServiceAccount)
		err = r.Get(ctx, types.NamespacedName{Name: scaleSetListenerServiceAccountName(autoscalingListener), Namespace: autoscalingListener.Namespace}, listenerSa)
		switch {
		case err == nil:
			if listenerSa.ObjectMeta.DeletionTimestamp.IsZero() {
				logger.Info("Deleting the listener service account")
				if err := r.Delete(ctx, listenerSa); err != nil {
					return false, fmt.Errorf("failed to delete listener service account: %v", err)
				}
			}
			return false, nil
		case err != nil && !kerrors.IsNotFound(err):
			return false, fmt.Errorf("failed to get listener service account: %v", err)
		}
		logger.Info("Listener service account is deleted")
	} else {
		logger.Info("Skipping listener service account deletion")
	}

	return true, nil
}

func (r *AutoscalingListenerReconciler) createServiceAccountForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (ctrl.Result, error) {
	newServiceAccount := r.resourceBuilder.newScaleSetListenerServiceAccount(autoscalingListener)

	if err := ctrl.SetControllerReference(autoscalingListener, newServiceAccount, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Creating listener service accounts", "namespace", newServiceAccount.Namespace, "name", newServiceAccount.Name)
	if err := r.Create(ctx, newServiceAccount); err != nil {
		logger.Error(err, "Unable to create listener service accounts", "namespace", newServiceAccount.Namespace, "name", newServiceAccount.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener service accounts", "namespace", newServiceAccount.Namespace, "name", newServiceAccount.Name)
	return ctrl.Result{}, nil
}

func (r *AutoscalingListenerReconciler) createListenerPod(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, autoscalingListener *v1alpha1.AutoscalingListener, serviceAccount *corev1.ServiceAccount, secret *corev1.Secret, logger logr.Logger) (ctrl.Result, error) {
	var envs []corev1.EnvVar
	if autoscalingListener.Spec.Proxy != nil {
		httpURL := corev1.EnvVar{
			Name: "http_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: proxyListenerSecretName(autoscalingListener)},
					Key:                  "http_proxy",
				},
			},
		}
		if autoscalingListener.Spec.Proxy.HTTP != nil {
			envs = append(envs, httpURL)
		}

		httpsURL := corev1.EnvVar{
			Name: "https_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: proxyListenerSecretName(autoscalingListener)},
					Key:                  "https_proxy",
				},
			},
		}
		if autoscalingListener.Spec.Proxy.HTTPS != nil {
			envs = append(envs, httpsURL)
		}

		noProxy := corev1.EnvVar{
			Name: "no_proxy",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: proxyListenerSecretName(autoscalingListener)},
					Key:                  "no_proxy",
				},
			},
		}
		if len(autoscalingListener.Spec.Proxy.NoProxy) > 0 {
			envs = append(envs, noProxy)
		}
	}

	cert := ""
	if autoscalingListener.Spec.GitHubServerTLS != nil {
		var err error
		cert, err = r.certificate(ctx, autoscalingRunnerSet, autoscalingListener)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create certificate env var for listener: %v", err)
		}
	}

	var metricsConfig *listenerMetricsServerConfig
	if r.ListenerMetricsAddr != "0" {
		metricsConfig = &listenerMetricsServerConfig{
			addr:     r.ListenerMetricsAddr,
			endpoint: r.ListenerMetricsEndpoint,
		}
	}

	var podConfig corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: scaleSetListenerConfigName(autoscalingListener)}, &podConfig); err != nil {
		if !kerrors.IsNotFound(err) {
			logger.Error(err, "Unable to get listener config secret", "namespace", autoscalingListener.Namespace, "name", scaleSetListenerConfigName(autoscalingListener))
			return ctrl.Result{Requeue: true}, err
		}

		logger.Info("Creating listener config secret")

		podConfig, err := r.resourceBuilder.newScaleSetListenerConfig(autoscalingListener, secret, metricsConfig, cert)
		if err != nil {
			logger.Error(err, "Failed to build listener config secret")
			return ctrl.Result{}, err
		}

		if err := ctrl.SetControllerReference(autoscalingListener, podConfig, r.Scheme); err != nil {
			logger.Error(err, "Failed to set controller reference")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, podConfig); err != nil {
			logger.Error(err, "Unable to create listener config secret", "namespace", podConfig.Namespace, "name", podConfig.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	newPod, err := r.resourceBuilder.newScaleSetListenerPod(autoscalingListener, &podConfig, serviceAccount, secret, metricsConfig, envs...)
	if err != nil {
		logger.Error(err, "Failed to build listener pod")
		return ctrl.Result{}, err
	}

	if err := ctrl.SetControllerReference(autoscalingListener, newPod, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference")
		return ctrl.Result{}, err
	}

	logger.Info("Creating listener pod", "namespace", newPod.Namespace, "name", newPod.Name)
	if err := r.Create(ctx, newPod); err != nil {
		logger.Error(err, "Unable to create listener pod", "namespace", newPod.Namespace, "name", newPod.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener pod", "namespace", newPod.Namespace, "name", newPod.Name)
	return ctrl.Result{}, nil
}

func (r *AutoscalingListenerReconciler) certificate(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet, autoscalingListener *v1alpha1.AutoscalingListener) (string, error) {
	if autoscalingListener.Spec.GitHubServerTLS.CertificateFrom == nil {
		return "", fmt.Errorf("githubServerTLS.certificateFrom is not specified")
	}

	if autoscalingListener.Spec.GitHubServerTLS.CertificateFrom.ConfigMapKeyRef == nil {
		return "", fmt.Errorf("githubServerTLS.certificateFrom.configMapKeyRef is not specified")
	}

	var configmap corev1.ConfigMap
	err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: autoscalingRunnerSet.Namespace,
			Name:      autoscalingListener.Spec.GitHubServerTLS.CertificateFrom.ConfigMapKeyRef.Name,
		},
		&configmap,
	)
	if err != nil {
		return "", fmt.Errorf(
			"failed to get configmap %s: %w",
			autoscalingListener.Spec.GitHubServerTLS.CertificateFrom.ConfigMapKeyRef.Name,
			err,
		)
	}

	certificate, ok := configmap.Data[autoscalingListener.Spec.GitHubServerTLS.CertificateFrom.ConfigMapKeyRef.Key]
	if !ok {
		return "", fmt.Errorf(
			"key %s is not found in configmap %s",
			autoscalingListener.Spec.GitHubServerTLS.CertificateFrom.ConfigMapKeyRef.Key,
			autoscalingListener.Spec.GitHubServerTLS.CertificateFrom.ConfigMapKeyRef.Name,
		)
	}

	return certificate, nil
}

func (r *AutoscalingListenerReconciler) createSecretsForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, secret *corev1.Secret, logger logr.Logger) (ctrl.Result, error) {
	newListenerSecret := r.resourceBuilder.newScaleSetListenerSecretMirror(autoscalingListener, secret)

	if err := ctrl.SetControllerReference(autoscalingListener, newListenerSecret, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Creating listener secret", "namespace", newListenerSecret.Namespace, "name", newListenerSecret.Name)
	if err := r.Create(ctx, newListenerSecret); err != nil {
		logger.Error(err, "Unable to create listener secret", "namespace", newListenerSecret.Namespace, "name", newListenerSecret.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener secret", "namespace", newListenerSecret.Namespace, "name", newListenerSecret.Name)
	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) createProxySecret(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (ctrl.Result, error) {
	data, err := autoscalingListener.Spec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
		var secret corev1.Secret
		err := r.Get(ctx, types.NamespacedName{Name: s, Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace}, &secret)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %s: %w", s, err)
		}
		return &secret, nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert proxy config to secret data: %w", err)
	}

	newProxySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyListenerSecretName(autoscalingListener),
			Namespace: autoscalingListener.Namespace,
			Labels: map[string]string{
				LabelKeyGitHubScaleSetNamespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
				LabelKeyGitHubScaleSetName:      autoscalingListener.Spec.AutoscalingRunnerSetName,
			},
		},
		Data: data,
	}
	if err := ctrl.SetControllerReference(autoscalingListener, newProxySecret, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create listener proxy secret: %w", err)
	}

	logger.Info("Creating listener proxy secret", "namespace", newProxySecret.Namespace, "name", newProxySecret.Name)
	if err := r.Create(ctx, newProxySecret); err != nil {
		logger.Error(err, "Unable to create listener secret", "namespace", newProxySecret.Namespace, "name", newProxySecret.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener proxy secret", "namespace", newProxySecret.Namespace, "name", newProxySecret.Name)

	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) updateSecretsForListener(ctx context.Context, secret *corev1.Secret, mirrorSecret *corev1.Secret, logger logr.Logger) (ctrl.Result, error) {
	dataHash := hash.ComputeTemplateHash(secret.Data)
	updatedMirrorSecret := mirrorSecret.DeepCopy()
	updatedMirrorSecret.Labels["secret-data-hash"] = dataHash
	updatedMirrorSecret.Data = secret.Data

	logger.Info("Updating listener mirror secret", "namespace", updatedMirrorSecret.Namespace, "name", updatedMirrorSecret.Name, "hash", dataHash)
	if err := r.Update(ctx, updatedMirrorSecret); err != nil {
		logger.Error(err, "Unable to update listener mirror secret", "namespace", updatedMirrorSecret.Namespace, "name", updatedMirrorSecret.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Updated listener mirror secret", "namespace", updatedMirrorSecret.Namespace, "name", updatedMirrorSecret.Name, "hash", dataHash)
	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) createRoleForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (ctrl.Result, error) {
	newRole := r.resourceBuilder.newScaleSetListenerRole(autoscalingListener)

	logger.Info("Creating listener role", "namespace", newRole.Namespace, "name", newRole.Name, "rules", newRole.Rules)
	if err := r.Create(ctx, newRole); err != nil {
		logger.Error(err, "Unable to create listener role", "namespace", newRole.Namespace, "name", newRole.Name, "rules", newRole.Rules)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener role", "namespace", newRole.Namespace, "name", newRole.Name, "rules", newRole.Rules)
	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) updateRoleForListener(ctx context.Context, listenerRole *rbacv1.Role, desiredRules []rbacv1.PolicyRule, desiredRulesHash string, logger logr.Logger) (ctrl.Result, error) {
	updatedPatchRole := listenerRole.DeepCopy()
	updatedPatchRole.Labels["role-policy-rules-hash"] = desiredRulesHash
	updatedPatchRole.Rules = desiredRules

	logger.Info("Updating listener role in namespace to have the right permission", "namespace", updatedPatchRole.Namespace, "name", updatedPatchRole.Name, "oldRules", listenerRole.Rules, "newRules", updatedPatchRole.Rules)
	if err := r.Update(ctx, updatedPatchRole); err != nil {
		logger.Error(err, "Unable to update listener role", "namespace", updatedPatchRole.Namespace, "name", updatedPatchRole.Name, "rules", updatedPatchRole.Rules)
		return ctrl.Result{}, err
	}

	logger.Info("Updated listener role in namespace to have the right permission", "namespace", updatedPatchRole.Namespace, "name", updatedPatchRole.Name, "rules", updatedPatchRole.Rules)
	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) createRoleBindingForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, listenerRole *rbacv1.Role, serviceAccount *corev1.ServiceAccount, logger logr.Logger) (ctrl.Result, error) {
	newRoleBinding := r.resourceBuilder.newScaleSetListenerRoleBinding(autoscalingListener, listenerRole, serviceAccount)

	logger.Info("Creating listener role binding",
		"namespace", newRoleBinding.Namespace,
		"name", newRoleBinding.Name,
		"role", listenerRole.Name,
		"serviceAccountNamespace", serviceAccount.Namespace,
		"serviceAccount", serviceAccount.Name)
	if err := r.Create(ctx, newRoleBinding); err != nil {
		logger.Error(err, "Unable to create listener role binding",
			"namespace", newRoleBinding.Namespace,
			"name", newRoleBinding.Name,
			"role", listenerRole.Name,
			"serviceAccountNamespace", serviceAccount.Namespace,
			"serviceAccount", serviceAccount.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener role binding",
		"namespace", newRoleBinding.Namespace,
		"name", newRoleBinding.Name,
		"role", listenerRole.Name,
		"serviceAccountNamespace", serviceAccount.Namespace,
		"serviceAccount", serviceAccount.Name)
	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) publishRunningListener(autoscalingListener *v1alpha1.AutoscalingListener, isUp bool) error {
	githubConfigURL := autoscalingListener.Spec.GitHubConfigUrl
	parsedURL, err := actions.ParseGitHubConfigFromURL(githubConfigURL)
	if err != nil {
		return err
	}

	commonLabels := metrics.CommonLabels{
		Name:         autoscalingListener.Name,
		Namespace:    autoscalingListener.Namespace,
		Repository:   parsedURL.Repository,
		Organization: parsedURL.Organization,
		Enterprise:   parsedURL.Enterprise,
	}

	if isUp {
		metrics.AddRunningListener(commonLabels)
	} else {
		metrics.SubRunningListener(commonLabels)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutoscalingListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	log := r.Log.WithValues("autoscalinglistener")

	groupVersionIndexer := func(rawObj client.Object) []string {
		groupVersion := v1alpha1.GroupVersion.String()
		owner := metav1.GetControllerOf(rawObj)
		if owner == nil {
			return nil
		}

		// ...make sure it is owned by this controller
		if owner.APIVersion != groupVersion || owner.Kind != "AutoscalingListener" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, resourceOwnerKey, groupVersionIndexer); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.ServiceAccount{}, resourceOwnerKey, groupVersionIndexer); err != nil {
		return err
	}

	labelBasedWatchFunc := func(_ context.Context, obj client.Object) []reconcile.Request {
		var requests []reconcile.Request
		labels := obj.GetLabels()
		namespace, ok := labels["auto-scaling-listener-namespace"]
		if !ok {
			return nil
		}

		name, ok := labels["auto-scaling-listener-name"]
		if !ok {
			return nil
		}
		requests = append(requests,
			reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      name,
					Namespace: namespace,
				},
			},
		)
		return requests
	}

	controller := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AutoscalingListener{}).
		Owns(&corev1.Pod{})

	if !ShouldSkipListenerSaCreation() {
		log.Info("Controller will own ServiceAccount for listeners")
		controller = controller.
			Owns(&corev1.ServiceAccount{})
	} else {
		log.Info("Skipping Listener's ServiceAccount ownership")
	}

	if !ShouldSkipListenerRbacSetup() {
		log.Info("Controller will watch RBAC resources for listeners")
		controller = controller.
			Watches(&rbacv1.Role{}, handler.EnqueueRequestsFromMapFunc(labelBasedWatchFunc)).
			Watches(&rbacv1.RoleBinding{}, handler.EnqueueRequestsFromMapFunc(labelBasedWatchFunc))
	} else {
		log.Info("Skipping Listener's RBAC watches")
	}

	return controller.
		WithEventFilter(predicate.ResourceVersionChangedPredicate{}).
		Complete(r)
}
