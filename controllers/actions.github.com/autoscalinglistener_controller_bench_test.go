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
	"testing"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_FinalizerAdd benchmarks adding finalizer to AutoscalingListener
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_FinalizerAdd(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet (required dependency)
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener without finalizer
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = nil

	// Create secret for listener configuration
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logf.Log.WithName("benchmark"),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	for b.Loop() {
		// Reset listener state (remove finalizer for each iteration)
		updatedListener := &actionsv1alpha1.AutoscalingListener{}
		_ = fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: listenerName}, updatedListener)
		updatedListener.Finalizers = nil
		_ = fakeClient.Update(ctx, updatedListener)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_FinalizerRemove benchmarks removing finalizer during deletion
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_FinalizerRemove(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer and deletion timestamp
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}
	now := metav1.Now()
	listener.DeletionTimestamp = &now

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logf.Log.WithName("benchmark"),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	for b.Loop() {
		// Reset listener with finalizer
		updatedListener := &actionsv1alpha1.AutoscalingListener{}
		if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: listenerName}, updatedListener); err == nil {
			updatedListener.Finalizers = []string{autoscalingListenerFinalizerName}
			_ = fakeClient.Update(ctx, updatedListener)
		}

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_MissingARSDeletion benchmarks deleting listener when AutoscalingRunnerSet is missing
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_MissingARSDeletion(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create listener with finalizer but NO AutoscalingRunnerSet
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listener, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logf.Log.WithName("benchmark"),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	for b.Loop() {
		// Recreate listener for each iteration
		_ = fakeClient.Delete(ctx, listener)
		newListener := NewMinimalAutoscalingListener(namespace, listenerName)
		newListener.Spec.AutoscalingRunnerSetNamespace = namespace
		newListener.Spec.AutoscalingRunnerSetName = arsName
		newListener.Spec.GitHubConfigSecret = secret.Name
		newListener.Finalizers = []string{autoscalingListenerFinalizerName}
		_ = fakeClient.Create(ctx, newListener)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_CreateServiceAccount benchmarks creating ServiceAccount
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_CreateServiceAccount(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer (already added)
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logf.Log.WithName("benchmark"),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	b.ResetTimer()
	for b.Loop() {
		// Delete ServiceAccount to trigger creation path
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      listenerName,
				Namespace: namespace,
			},
		}
		_ = fakeClient.Delete(ctx, sa)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_UpdateServiceAccount benchmarks updating ServiceAccount
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_UpdateServiceAccount(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create ServiceAccount with outdated labels
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"outdated-label": "old-value",
			},
		},
	}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, sa, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	for b.Loop() {
		// Reset ServiceAccount labels to trigger update
		updatedSA := &corev1.ServiceAccount{}
		_ = fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: listenerName}, updatedSA)
		updatedSA.Labels = map[string]string{"outdated-label": "old-value"}
		_ = fakeClient.Update(ctx, updatedSA)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_CreateRole benchmarks creating Role
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_CreateRole(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create ServiceAccount (prerequisite)
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
		},
	}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, sa, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Delete Role to trigger creation path
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      listenerName,
				Namespace: namespace,
			},
		}
		_ = fakeClient.Delete(ctx, role)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_UpdateRole benchmarks updating Role
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_UpdateRole(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create ServiceAccount
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
		},
	}

	// Create Role with outdated labels
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"outdated-label": "old-value",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get"},
			},
		},
	}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, sa, role, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset Role labels to trigger update
		updatedRole := &rbacv1.Role{}
		_ = fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: listenerName}, updatedRole)
		updatedRole.Labels = map[string]string{"outdated-label": "old-value"}
		_ = fakeClient.Update(ctx, updatedRole)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_CreateRoleBinding benchmarks creating RoleBinding
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_CreateRoleBinding(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create ServiceAccount
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
		},
	}

	// Create Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{},
	}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, sa, role, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Delete RoleBinding to trigger creation path
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      listenerName,
				Namespace: namespace,
			},
		}
		_ = fakeClient.Delete(ctx, rb)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_UpdateRoleBinding benchmarks updating RoleBinding
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_UpdateRoleBinding(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create listener with finalizer
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create ServiceAccount
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
		},
	}

	// Create Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{},
	}

	// Create RoleBinding with outdated labels
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"outdated-label": "old-value",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     listenerName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      listenerName,
				Namespace: namespace,
			},
		},
	}

	// Create secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, sa, role, roleBinding, secret).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset RoleBinding labels to trigger update
		updatedRB := &rbacv1.RoleBinding{}
		_ = fakeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: listenerName}, updatedRB)
		updatedRB.Labels = map[string]string{"outdated-label": "old-value"}
		_ = fakeClient.Update(ctx, updatedRB)

		_, _ = reconciler.Reconcile(ctx, req)
	}
}

// BenchmarkActionsGithub_Reconcile_AutoscalingListener_SteadyState benchmarks no-op steady-state reconciliation
func BenchmarkActionsGithub_Reconcile_AutoscalingListener_SteadyState(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = actionsv1alpha1.AddToScheme(scheme)

	namespace := "test-ns"
	listenerName := "test-listener"
	arsName := "test-ars"

	// Create AutoscalingRunnerSet
	ars := NewMinimalAutoscalingRunnerSet(namespace, arsName)

	// Create fully configured listener
	listener := NewMinimalAutoscalingListener(namespace, listenerName)
	listener.Spec.AutoscalingRunnerSetNamespace = namespace
	listener.Spec.AutoscalingRunnerSetName = arsName
	listener.Finalizers = []string{autoscalingListenerFinalizerName}

	// Create all required resources (steady state)
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"actions.github.com/autoscaling-listener-name":      listenerName,
				"actions.github.com/autoscaling-listener-namespace": namespace,
			},
		},
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"auto-scaling-listener-name":      listenerName,
				"auto-scaling-listener-namespace": namespace,
			},
		},
		Rules: rulesForListenerRole([]string{listener.Spec.EphemeralRunnerSetName}),
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"auto-scaling-listener-name":      listenerName,
				"auto-scaling-listener-namespace": namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     listenerName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      listenerName,
				Namespace: namespace,
			},
		},
	}

	// Create config secret
	configSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaleSetListenerConfigName(listener),
			Namespace: namespace,
			Labels: map[string]string{
				"auto-scaling-listener-name": listenerName,
			},
		},
		Data: map[string][]byte{
			"listener": []byte(`{"url":"https://github.com/example/repo","token":"fake-token"}`),
		},
	}

	// Create github config secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte("fake-token"),
		},
	}
	listener.Spec.GitHubConfigSecret = secret.Name

	// Create listener pod (running state)
	listenerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      listenerName,
			Namespace: namespace,
			Labels: map[string]string{
				"auto-scaling-listener-name": listenerName,
			},
			Annotations: map[string]string{
				AnnotationKeyIntegrityHash: "deterministic-hash",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: listenerName,
			Containers: []corev1.Container{
				{
					Name:  autoscalingListenerContainerName,
					Image: listener.Spec.Image,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: autoscalingListenerContainerName,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{
							StartedAt: metav1.Now(),
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ars, listener, sa, role, roleBinding, configSecret, secret, listenerPod).
		WithStatusSubresource(&actionsv1alpha1.AutoscalingListener{}).
		Build()

	secretResolver := secretresolver.New(fakeClient, NewFakeMultiClient())
	rb := &ResourceBuilder{SecretResolver: secretResolver}

	reconciler := &AutoscalingListenerReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: rb,
	}

	ctx := context.Background()
	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Namespace: namespace,
			Name:      listenerName,
		},
	}

	// Warmup
	WarmupIteration(b, func() {
		_, _ = reconciler.Reconcile(ctx, req)
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// No-op steady state - everything is in sync
		_, _ = reconciler.Reconcile(ctx, req)
	}
}
