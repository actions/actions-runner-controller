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
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileDefersRunnerIdentityUntilPodExists(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "test-runner"}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       key.Name,
			Namespace:  key.Namespace,
			Finalizers: []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName},
		},
		Spec: v1alpha1.EphemeralRunnerSpec{
			PodTemplateSpec: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName}},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Data: map[string][]byte{
			jitTokenKey:  []byte("jit-token"),
			"runnerId":   []byte("7"),
			"runnerName": []byte("test-runner"),
		},
	}

	failStatusPatch := true
	statusPatchAttempts := 0
	c := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runner, secret).
		WithStatusSubresource(&v1alpha1.EphemeralRunner{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, clt client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*v1alpha1.EphemeralRunner); ok {
					statusPatchAttempts++
					if failStatusPatch {
						failStatusPatch = false
						return errors.New("transient status patch failure")
					}
				}
				return clt.Status().Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	newReconciler := func() *EphemeralRunnerReconciler {
		return &EphemeralRunnerReconciler{
			Client: c,
			Log:    logr.Discard(),
			Scheme: scheme,
			ResourceBuilder: ResourceBuilder{
				Scheme: scheme,
			},
		}
	}
	podCount := func() int {
		t.Helper()

		var pods corev1.PodList
		require.NoError(t, c.List(ctx, &pods, client.InNamespace(key.Namespace)))
		return len(pods.Items)
	}
	getRunner := func() *v1alpha1.EphemeralRunner {
		t.Helper()

		got := new(v1alpha1.EphemeralRunner)
		require.NoError(t, c.Get(ctx, key, got))
		return got
	}

	// The first pass creates the Pod but deliberately leaves status unwritten.
	_, err := newReconciler().Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, 1, podCount())
	assert.Zero(t, getRunner().Status.RunnerID)
	assert.Empty(t, getRunner().Status.RunnerName)
	assert.Zero(t, statusPatchAttempts, "the runner identity must not delay Pod creation")

	// This is the same state after a controller crash following Pod creation:
	// the next reconcile finds the Pod and restores the identity from the JIT
	// secret. A transient patch failure returns an error for reconciliation to
	// retry without creating another Pod.
	_, err = newReconciler().Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.ErrorContains(t, err, "failed to update runner status for RunnerId/RunnerName")
	assert.Equal(t, 1, podCount())
	assert.Zero(t, getRunner().Status.RunnerID)
	assert.Empty(t, getRunner().Status.RunnerName)

	_, err = newReconciler().Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, 1, podCount())
	assert.Equal(t, 2, statusPatchAttempts)
	assert.Equal(t, 7, getRunner().Status.RunnerID)
	assert.Equal(t, "test-runner", getRunner().Status.RunnerName)
}

func TestReconcileRejectsMalformedJITSecretBeforeCreatingPod(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "test-runner"}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	runner := newExampleRunner(key.Name, key.Namespace, "github-config-secret")
	runner.Finalizers = []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Data: map[string][]byte{
			jitTokenKey:  []byte("jit-token"),
			"runnerName": []byte(key.Name),
		},
	}
	c := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runner, secret).
		WithStatusSubresource(&v1alpha1.EphemeralRunner{}).
		Build()

	reconciler := &EphemeralRunnerReconciler{
		Client: c,
		Log:    logr.Discard(),
		Scheme: scheme,
		ResourceBuilder: ResourceBuilder{
			Scheme: scheme,
		},
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, 500*time.Millisecond, result.RequeueAfter)

	err = c.Get(ctx, key, new(corev1.Pod))
	assert.True(t, kerrors.IsNotFound(err), "a malformed JIT Secret must never produce a Pod")

	err = c.Get(ctx, key, new(corev1.Secret))
	assert.True(t, kerrors.IsNotFound(err), "the malformed JIT Secret must be deleted")
}
