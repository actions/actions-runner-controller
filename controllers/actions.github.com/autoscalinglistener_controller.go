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
	"bytes"
	"context"
	"fmt"
	"maps"
	"reflect"
	"time"

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
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/actions/actions-runner-controller/github/actions"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

	ResourceBuilder
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=create;delete;get;list;watch;update
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create;delete;get;list;watch;update
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=autoscalinglisteners/finalizers,verbs=update

// Reconcile a AutoscalingListener resource to meet its desired spec.
func (r *AutoscalingListenerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("autoscalinglistener", req.NamespacedName)

	var autoscalingListener v1alpha1.AutoscalingListener
	if err := r.Get(ctx, req.NamespacedName, &autoscalingListener); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	original := autoscalingListener.DeepCopy()

	if !autoscalingListener.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&autoscalingListener, autoscalingListenerFinalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("Deleting resources")
		requeue, err := r.cleanupResources(ctx, &autoscalingListener, log)
		if err != nil {
			log.Error(err, "Failed to cleanup resources after deletion")
			return ctrl.Result{}, err
		}
		if requeue {
			log.Info("Waiting for resources to be deleted before removing finalizer")
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second}, nil
		}

		log.Info("Removing finalizer")
		if controllerutil.RemoveFinalizer(&autoscalingListener, autoscalingListenerFinalizerName) {
			if err := r.Patch(ctx, &autoscalingListener, client.MergeFrom(original)); err != nil && !kerrors.IsNotFound(err) {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}

		log.Info("Successfully removed finalizer after cleanup")
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(&autoscalingListener, autoscalingListenerFinalizerName) {
		if err := r.Patch(ctx, &autoscalingListener, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	// Check if the AutoscalingRunnerSet exists
	var autoscalingRunnerSet v1alpha1.AutoscalingRunnerSet
	if err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			Name:      autoscalingListener.Spec.AutoscalingRunnerSetName,
		},
		&autoscalingRunnerSet,
	); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info("AutoscalingRunnerSet is not found, deleting autoscaling listener", "namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace, "name", autoscalingListener.Spec.AutoscalingRunnerSetName)
			if err := r.Delete(ctx, &autoscalingListener); err != nil {
				log.Error(err, "failed to delete autoscaling listener", "namespace", autoscalingListener.Namespace, "name", autoscalingListener.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		log.Error(
			err, "Failed to find AutoscalingRunnerSet.",
			"namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			"name", autoscalingListener.Spec.AutoscalingRunnerSetName,
		)
		return ctrl.Result{}, err
	}

	// Make sure the runner scale set listener service account is created for the listener pod in the controller namespace
	var serviceAccount corev1.ServiceAccount
	err := r.Get(
		ctx,
		types.NamespacedName{
			Namespace: autoscalingListener.Namespace,
			Name:      autoscalingListener.Name,
		},
		&serviceAccount,
	)
	switch {
	case err == nil:
		desiredServiceAccount, err := r.newScaleSetListenerServiceAccount(&autoscalingListener)
		if err != nil {
			log.Error(err, "Failed to build desired listener service account")
			return ctrl.Result{}, err
		}

		desiredLabels := r.filterAndMergeLabels(serviceAccount.Labels, desiredServiceAccount.Labels)
		labelsModified := !maps.Equal(serviceAccount.Labels, desiredLabels)
		desiredAnnotations := r.mergeAnnotations(serviceAccount.Annotations, desiredServiceAccount.Annotations)
		annotationsModified := !maps.Equal(serviceAccount.Annotations, desiredAnnotations)
		if labelsModified || annotationsModified {
			updatedServiceAccount := serviceAccount.DeepCopy()
			if labelsModified {
				updatedServiceAccount.Labels = desiredLabels
			}
			if annotationsModified {
				updatedServiceAccount.Annotations = desiredAnnotations
			}
			log.Info("Updating listener service account")

			if err := r.Patch(ctx, updatedServiceAccount, client.MergeFrom(&serviceAccount)); err != nil {
				log.Error(err, "Failed to update listener service account")
				return ctrl.Result{}, err
			}

			return ctrl.Result{Requeue: true}, nil
		}
	case kerrors.IsNotFound(err):
		// Create a service account for the listener pod in the controller namespace
		log.Info("Creating a service account for the listener pod")
		return r.createServiceAccountForListener(ctx, &autoscalingListener, log)
	default:
		log.Error(err, "Unable to get listener service accounts", "namespace", autoscalingListener.Namespace, "name", autoscalingListener.Name)
		return ctrl.Result{}, err
	}

	// Make sure the runner scale set listener role is created in the AutoscalingRunnerSet namespace
	var listenerRole rbacv1.Role
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace,
			Name:      autoscalingListener.Name,
		},
		&listenerRole,
	)
	switch {
	case err == nil:
		desiredRole := r.newScaleSetListenerRole(&autoscalingListener)
		desiredLabels := r.filterAndMergeLabels(listenerRole.Labels, desiredRole.Labels)
		labelsModified := !maps.Equal(listenerRole.Labels, desiredLabels)
		desiredAnnotations := r.mergeAnnotations(listenerRole.Annotations, desiredRole.Annotations)
		annotationsModified := !maps.Equal(listenerRole.Annotations, desiredAnnotations)
		rulesModified := !reflect.DeepEqual(listenerRole.Rules, desiredRole.Rules)
		if labelsModified || annotationsModified || rulesModified {
			updatedRole := listenerRole.DeepCopy()
			if labelsModified {
				updatedRole.Labels = desiredLabels
			}
			if annotationsModified {
				updatedRole.Annotations = desiredAnnotations
			}
			if rulesModified {
				updatedRole.Rules = desiredRole.Rules
			}
			log.Info("Updating listener role")
			if err := r.Patch(ctx, updatedRole, client.MergeFrom(&listenerRole)); err != nil {
				log.Error(err, "Failed to update listener role")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	case kerrors.IsNotFound(err):
		// Create a role for the listener pod in the AutoScalingRunnerSet namespace
		log.Info("Creating a role for the listener pod")
		return r.createRoleForListener(ctx, &autoscalingListener, log)
	default: // error
		log.Error(err, "Unable to get listener role", "namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace, "name", autoscalingListener.Name)
		return ctrl.Result{}, err
	}

	// Make sure the runner scale set listener role binding is created
	var listenerRoleBinding rbacv1.RoleBinding
	err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: autoscalingListener.Name}, &listenerRoleBinding)
	switch {
	case err == nil:
		desiredRoleBinding := r.newScaleSetListenerRoleBinding(
			&autoscalingListener,
			&listenerRole,
			&serviceAccount,
		)
		desiredLabels := r.filterAndMergeLabels(listenerRoleBinding.Labels, desiredRoleBinding.Labels)
		labelsModified := !maps.Equal(listenerRoleBinding.Labels, desiredLabels)
		desiredAnnotations := r.mergeAnnotations(listenerRoleBinding.Annotations, desiredRoleBinding.Annotations)
		annotationsModified := !maps.Equal(listenerRoleBinding.Annotations, desiredAnnotations)
		if labelsModified || annotationsModified {
			updatedRoleBinding := listenerRoleBinding.DeepCopy()
			if labelsModified {
				updatedRoleBinding.Labels = desiredLabels
			}
			if annotationsModified {
				updatedRoleBinding.Annotations = desiredAnnotations
			}
			log.Info("Updating listener role binding")
			if err := r.Patch(ctx, updatedRoleBinding, client.MergeFrom(&listenerRoleBinding)); err != nil {
				log.Error(err, "Failed to update listener role binding")
				return ctrl.Result{}, err
			}

			log.Info("Updated listener role binding")
			return ctrl.Result{Requeue: true}, nil
		}

	case kerrors.IsNotFound(err):
		// Create a role binding for the listener pod in the AutoScalingRunnerSet namespace
		log.Info("Creating a role binding for the service account and role")
		return r.createRoleBindingForListener(
			ctx,
			&autoscalingListener,
			&listenerRole,
			&serviceAccount,
			log,
		)
	default: // error
		log.Error(err, "Unable to get listener role binding", "namespace", autoscalingListener.Spec.AutoscalingRunnerSetNamespace, "name", autoscalingListener.Name)
		return ctrl.Result{}, err
	}

	// Create a secret containing proxy config if specified
	if autoscalingListener.Spec.Proxy != nil {
		var proxySecret corev1.Secret
		err := r.Get(
			ctx,
			types.NamespacedName{
				Namespace: autoscalingListener.Namespace,
				Name:      proxyListenerSecretName(&autoscalingListener),
			},
			&proxySecret,
		)
		switch {
		case err == nil:
			proxySecretData, err := autoscalingListener.Spec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
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
			desiredListenerProxy, err := r.newAutoscalingListenerProxySecret(&autoscalingListener, proxySecretData)
			if err != nil {
				log.Error(err, "Failed to build desired listener proxy secret")
				return ctrl.Result{}, err
			}
			dataModified := !maps.EqualFunc(proxySecret.Data, desiredListenerProxy.Data, bytes.Equal)
			desiredLabels := r.filterAndMergeLabels(proxySecret.Labels, desiredListenerProxy.Labels)
			labelsModified := !maps.Equal(proxySecret.Labels, desiredLabels)
			desiredAnnotations := r.mergeAnnotations(proxySecret.Annotations, desiredListenerProxy.Annotations)
			annotationsModified := !maps.Equal(proxySecret.Annotations, desiredAnnotations)
			if dataModified || labelsModified || annotationsModified {
				updatedProxySecret := proxySecret.DeepCopy()
				if dataModified {
					updatedProxySecret.Data = desiredListenerProxy.Data
				}
				if labelsModified {
					updatedProxySecret.Labels = desiredLabels
				}
				if annotationsModified {
					updatedProxySecret.Annotations = desiredAnnotations
				}
				log.Info("Updating listener proxy secret")
				if err := r.Patch(ctx, updatedProxySecret, client.MergeFrom(&proxySecret)); err != nil {
					log.Error(err, "Failed to update listener proxy secret")
					return ctrl.Result{}, err
				}
				return ctrl.Result{Requeue: true}, nil
			}
		case kerrors.IsNotFound(err):
			// Create a mirror secret for the listener pod in the Controller namespace for listener pod to use
			log.Info("Creating a listener proxy secret for the listener pod")
			return r.createProxySecret(ctx, &autoscalingListener, log)
		default: // error
			log.Error(err, "Unable to get listener proxy secret", "namespace", autoscalingListener.Namespace, "name", proxyListenerSecretName(&autoscalingListener))
			return ctrl.Result{}, err
		}
	}

	var appConfig *appconfig.AppConfig
	getAppConfig := func() (*appconfig.AppConfig, error) {
		if appConfig != nil {
			return appConfig, nil
		}

		cfg, err := r.GetAppConfig(ctx, &autoscalingRunnerSet)
		if err != nil {
			log.Error(
				err,
				"Failed to get app config for AutoscalingRunnerSet.",
				"namespace",
				autoscalingRunnerSet.Namespace,
				"name",
				autoscalingRunnerSet.GitHubConfigSecret,
			)
			return nil, err
		}

		appConfig = cfg
		return appConfig, nil
	}

	var metricsConfig *listenerMetricsServerConfig
	if r.ListenerMetricsAddr != "0" {
		metricsConfig = &listenerMetricsServerConfig{
			addr:     r.ListenerMetricsAddr,
			endpoint: r.ListenerMetricsEndpoint,
		}
	}

	var listenerConfigSecret corev1.Secret
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: autoscalingListener.Namespace,
			Name:      scaleSetListenerConfigName(&autoscalingListener),
		},
		&listenerConfigSecret,
	)
	switch {
	case err == nil:
		cfg, err := r.GetAppConfig(ctx, &autoscalingRunnerSet)
		if err != nil {
			return ctrl.Result{}, err
		}
		cert := ""
		if autoscalingListener.Spec.GitHubServerTLS != nil {
			cert, err = r.certificate(ctx, &autoscalingRunnerSet, &autoscalingListener)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to build GitHub server TLS certificate value for listener config: %w", err)
			}
		}
		desiredSecret, err := r.newScaleSetListenerConfig(&autoscalingListener, cfg, metricsConfig, cert)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to build listener config secret: %w", err)
		}
		dataModified := !maps.EqualFunc(listenerConfigSecret.Data, desiredSecret.Data, bytes.Equal)
		desiredLabels := r.filterAndMergeLabels(listenerConfigSecret.Labels, desiredSecret.Labels)
		labelsModified := !maps.Equal(listenerConfigSecret.Labels, desiredLabels)
		desiredAnnotations := r.mergeAnnotations(listenerConfigSecret.Annotations, desiredSecret.Annotations)
		annotationsModified := !maps.Equal(listenerConfigSecret.Annotations, desiredAnnotations)

		if dataModified || labelsModified || annotationsModified {
			updatedSecret := listenerConfigSecret.DeepCopy()
			if dataModified {
				updatedSecret.Data = desiredSecret.Data
			}
			if labelsModified {
				updatedSecret.Labels = desiredLabels
			}
			if annotationsModified {
				updatedSecret.Annotations = desiredAnnotations
			}
			log.Info("Updating listener config secret", "namespace", updatedSecret.Namespace, "name", updatedSecret.Name)
			if err := r.Patch(ctx, updatedSecret, client.MergeFrom(&listenerConfigSecret)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update listener config secret: %w", err)
			}
			return ctrl.Result{Requeue: true}, nil
		}
	case kerrors.IsNotFound(err):
		cfg, err := getAppConfig()
		if err != nil {
			return ctrl.Result{}, err
		}

		cert := ""
		if autoscalingListener.Spec.GitHubServerTLS != nil {
			cert, err = r.certificate(ctx, &autoscalingRunnerSet, &autoscalingListener)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to build GitHub server TLS certificate value for listener config: %w", err)
			}
		}
		desiredSecret, err := r.newScaleSetListenerConfig(&autoscalingListener, cfg, metricsConfig, cert)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to build listener config secret: %w", err)
		}

		log.Info("Creating listener config secret", "namespace", desiredSecret.Namespace, "name", desiredSecret.Name)
		if err := r.Create(ctx, desiredSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create listener config secret: %w", err)
		}

		// Requeue to create listener pod with the config secret
		return ctrl.Result{Requeue: true}, nil
	default:
		log.Error(err, "Unable to get listener config secret", "namespace", autoscalingListener.Namespace, "name", scaleSetListenerConfigName(&autoscalingListener))
		return ctrl.Result{}, err
	}

	var listenerPod corev1.Pod
	err = r.Get(
		ctx,
		client.ObjectKey{
			Namespace: autoscalingListener.Namespace,
			Name:      autoscalingListener.Name,
		},
		&listenerPod,
	)
	switch {
	case err == nil:
		desiredPod, err := r.newScaleSetListenerPod(
			&autoscalingListener,
			&listenerConfigSecret,
			&serviceAccount,
			&listenerRole,
			&listenerRoleBinding,
			metricsConfig,
		)
		if err != nil {
			log.Error(err, "Failed to build listener pod")
			return ctrl.Result{}, err
		}

		shouldReCreate := desiredPod.Annotations[annotationKeyIntegrityHash] != listenerPod.Annotations[annotationKeyIntegrityHash]
		if shouldReCreate {
			log.Info("Listener pod dependency changed, recreating listener pod")
			if err := r.deleteListenerPod(ctx, &autoscalingListener, &listenerPod, log); err != nil {
				return ctrl.Result{}, err
			}

			log.Info("Listener pod is deleted, will recreate with new dependencies")
			return ctrl.Result{}, nil
		}

		desiredLabels := r.filterAndMergeLabels(listenerPod.Labels, desiredPod.Labels)
		labelsModified := !maps.Equal(listenerPod.Labels, desiredLabels)
		desiredAnnotations := r.mergeAnnotations(listenerPod.Annotations, desiredPod.Annotations)
		annotationsModified := !maps.Equal(listenerPod.Annotations, desiredAnnotations)

		if labelsModified || annotationsModified {
			updatedPod := listenerPod.DeepCopy()
			if labelsModified {
				updatedPod.Labels = desiredLabels
			}
			if annotationsModified {
				updatedPod.Annotations = desiredAnnotations
			}
			log.Info("Updating listener pod", "namespace", updatedPod.Namespace, "name", updatedPod.Name)
			if err := r.Patch(ctx, updatedPod, client.MergeFrom(&listenerPod)); err != nil {
				log.Error(err, "Unable to update listener pod", "namespace", updatedPod.Namespace, "name", updatedPod.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

	case kerrors.IsNotFound(err):
		if err := r.publishRunningListener(&autoscalingListener, false); err != nil {
			// If publish fails, URL is incorrect which means the listener pod would never be able to start
			return ctrl.Result{}, nil
		}

		desiredPod, err := r.newScaleSetListenerPod(
			&autoscalingListener,
			&listenerConfigSecret,
			&serviceAccount,
			&listenerRole,
			&listenerRoleBinding,
			metricsConfig,
		)
		if err != nil {
			log.Error(err, "Failed to build listener pod")
			return ctrl.Result{}, err
		}

		log.Info("Creating listener pod", "namespace", desiredPod.Namespace, "name", desiredPod.Name)
		if err := r.Create(ctx, desiredPod); err != nil {
			log.Error(err, "Unable to create listener pod", "namespace", desiredPod.Namespace, "name", desiredPod.Name)
			return ctrl.Result{}, err
		}
	default: // error
		log.Error(err, "Unable to get listener pod", "namespace", autoscalingListener.Namespace, "name", autoscalingListener.Name)
		return ctrl.Result{}, err
	}

	cs := listenerContainerStatus(&listenerPod)
	switch {
	case listenerPod.Status.Reason == "Evicted":
		log.Info(
			"Listener pod is evicted",
			"phase", listenerPod.Status.Phase,
			"reason", listenerPod.Status.Reason,
			"message", listenerPod.Status.Message,
		)

		return ctrl.Result{}, r.deleteListenerPod(ctx, &autoscalingListener, &listenerPod, log)

	case cs == nil:
		log.Info("Listener pod is not ready", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
		return ctrl.Result{}, nil
	case cs.State.Terminated != nil:
		log.Info(
			"Listener pod is terminated",
			"namespace", listenerPod.Namespace,
			"name", listenerPod.Name,
			"reason", cs.State.Terminated.Reason,
			"message", cs.State.Terminated.Message,
		)

		return ctrl.Result{}, r.deleteListenerPod(ctx, &autoscalingListener, &listenerPod, log)

	case cs.State.Running != nil:
		if err := r.publishRunningListener(&autoscalingListener, true); err != nil {
			log.Error(err, "Unable to publish running listener", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
			// stop reconciling. We should never get to this point but if we do,
			// listener won't be able to start up, and the crash from the pod should
			// notify the reconciler again.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil

	}
	return ctrl.Result{}, nil
}

func (r *AutoscalingListenerReconciler) deleteListenerPod(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, listenerPod *corev1.Pod, log logr.Logger) error {
	if err := r.publishRunningListener(autoscalingListener, false); err != nil {
		log.Error(err, "Unable to publish runner listener down metric", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
	}

	if listenerPod.DeletionTimestamp.IsZero() {
		log.Info("Deleting the listener pod", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
		if err := r.Delete(ctx, listenerPod); err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Unable to delete the listener pod", "namespace", listenerPod.Namespace, "name", listenerPod.Name)
			return err
		}
	}
	return nil
}

func (r *AutoscalingListenerReconciler) cleanupResources(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (requeue bool, err error) {
	logger.Info("Cleaning up the listener pod")
	listenerPod := new(corev1.Pod)
	err = r.Get(ctx, types.NamespacedName{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, listenerPod)
	switch {
	case err == nil:
		if listenerPod.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener pod")
			if err := r.Delete(ctx, listenerPod); err != nil {
				return false, fmt.Errorf("failed to delete listener pod: %w", err)
			}
		}
		requeue = true
	case kerrors.IsNotFound(err):
		_ = r.publishRunningListener(autoscalingListener, false) // If error is returned, we never published metrics so it is safe to ignore
	default:
		return false, fmt.Errorf("failed to get listener pods: %w", err)
	}
	logger.Info("Listener pod is deleted")

	var secret corev1.Secret
	err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Namespace, Name: scaleSetListenerConfigName(autoscalingListener)}, &secret)
	switch {
	case err == nil:
		if secret.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener config secret")
			if err := r.Delete(ctx, &secret); err != nil {
				return false, fmt.Errorf("failed to delete listener config secret: %w", err)
			}
		}
		requeue = true
	case !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener config secret: %w", err)
	}

	if autoscalingListener.Spec.Proxy != nil {
		logger.Info("Cleaning up the listener proxy secret")
		proxySecret := new(corev1.Secret)
		err = r.Get(ctx, types.NamespacedName{Name: proxyListenerSecretName(autoscalingListener), Namespace: autoscalingListener.Namespace}, proxySecret)
		switch {
		case err == nil:
			if proxySecret.DeletionTimestamp.IsZero() {
				logger.Info("Deleting the listener proxy secret")
				if err := r.Delete(ctx, proxySecret); err != nil {
					return false, fmt.Errorf("failed to delete listener proxy secret: %w", err)
				}
			}
			requeue = true
		case !kerrors.IsNotFound(err):
			return false, fmt.Errorf("failed to get listener proxy secret: %w", err)
		}
		logger.Info("Listener proxy secret is deleted")
	}

	listenerRoleBinding := new(rbacv1.RoleBinding)
	err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: autoscalingListener.Name}, listenerRoleBinding)
	switch {
	case err == nil:
		if listenerRoleBinding.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener role binding")
			if err := r.Delete(ctx, listenerRoleBinding); err != nil {
				return false, fmt.Errorf("failed to delete listener role binding: %w", err)
			}
		}
		requeue = true
	case !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener role binding: %w", err)
	}
	logger.Info("Listener role binding is deleted")

	listenerRole := new(rbacv1.Role)
	err = r.Get(ctx, types.NamespacedName{Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace, Name: autoscalingListener.Name}, listenerRole)
	switch {
	case err == nil:
		if listenerRole.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener role")
			if err := r.Delete(ctx, listenerRole); err != nil {
				return false, fmt.Errorf("failed to delete listener role: %w", err)
			}
		}
		requeue = true
	case !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener role: %w", err)
	}
	logger.Info("Listener role is deleted")

	logger.Info("Cleaning up the listener service account")
	listenerSa := new(corev1.ServiceAccount)
	err = r.Get(ctx, types.NamespacedName{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, listenerSa)
	switch {
	case err == nil:
		if listenerSa.DeletionTimestamp.IsZero() {
			logger.Info("Deleting the listener service account")
			if err := r.Delete(ctx, listenerSa); err != nil {
				return false, fmt.Errorf("failed to delete listener service account: %w", err)
			}
		}
		requeue = true
	case !kerrors.IsNotFound(err):
		return false, fmt.Errorf("failed to get listener service account: %w", err)
	}
	logger.Info("Listener service account is deleted")

	return requeue, nil
}

func (r *AutoscalingListenerReconciler) createServiceAccountForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (ctrl.Result, error) {
	newServiceAccount, err := r.newScaleSetListenerServiceAccount(autoscalingListener)
	if err != nil {
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

	newProxySecret, err := r.newAutoscalingListenerProxySecret(autoscalingListener, data)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build listener proxy secret: %w", err)
	}

	logger.Info("Creating listener proxy secret", "namespace", newProxySecret.Namespace, "name", newProxySecret.Name)
	if err := r.Create(ctx, newProxySecret); err != nil {
		logger.Error(err, "Unable to create listener secret", "namespace", newProxySecret.Namespace, "name", newProxySecret.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener proxy secret", "namespace", newProxySecret.Namespace, "name", newProxySecret.Name)

	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) createRoleForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, logger logr.Logger) (ctrl.Result, error) {
	newRole := r.newScaleSetListenerRole(autoscalingListener)

	logger.Info("Creating listener role", "namespace", newRole.Namespace, "name", newRole.Name, "rules", newRole.Rules)
	if err := r.Create(ctx, newRole); err != nil {
		logger.Error(err, "Unable to create listener role", "namespace", newRole.Namespace, "name", newRole.Name, "rules", newRole.Rules)
		return ctrl.Result{}, err
	}

	logger.Info("Created listener role", "namespace", newRole.Namespace, "name", newRole.Name, "rules", newRole.Rules)
	return ctrl.Result{Requeue: true}, nil
}

func (r *AutoscalingListenerReconciler) createRoleBindingForListener(ctx context.Context, autoscalingListener *v1alpha1.AutoscalingListener, listenerRole *rbacv1.Role, serviceAccount *corev1.ServiceAccount, logger logr.Logger) (ctrl.Result, error) {
	newRoleBinding := r.newScaleSetListenerRoleBinding(autoscalingListener, listenerRole, serviceAccount)

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
	githubConfigURL := autoscalingListener.Spec.GitHubConfigURL
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
func (r *AutoscalingListenerReconciler) SetupWithManager(mgr ctrl.Manager, opts ...Option) error {
	r.setSchemeIfUnset(r.Scheme)

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
		requests = append(
			requests,
			reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      name,
					Namespace: namespace,
				},
			},
		)
		return requests
	}

	return builderWithOptions(
		ctrl.NewControllerManagedBy(mgr).
			For(&v1alpha1.AutoscalingListener{}).
			Owns(&corev1.Pod{}).
			Owns(&corev1.ServiceAccount{}).
			Watches(&rbacv1.Role{}, handler.EnqueueRequestsFromMapFunc(labelBasedWatchFunc)).
			Watches(&rbacv1.RoleBinding{}, handler.EnqueueRequestsFromMapFunc(labelBasedWatchFunc)).
			WithEventFilter(predicate.ResourceVersionChangedPredicate{}),
		opts,
	).Complete(r)
}

func listenerContainerStatus(pod *corev1.Pod) *corev1.ContainerStatus {
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == autoscalingListenerContainerName {
			return cs
		}
	}
	return nil
}
