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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileForbiddenPodCreation(t *testing.T) {
	const (
		throttled   = "rpc error: code = ResourceExhausted desc = stream terminated by RST_STREAM with error code: ENHANCE_YOUR_CALM"
		unavailable = "rpc error: code = Unavailable desc = connection error: desc = \"transport: Error while dialing\""
		quota       = "exceeded quota: runner-quota, requested: pods=1, used: pods=10, limited: pods=10"
		noPriority  = "no PriorityClass with name notexist was found"
		permission  = "rpc error: code = PermissionDenied desc = the caller does not have permission"
	)

	tests := []struct {
		name string
		// message is the reason the API server gives for rejecting the pod.
		message string
		// age is how long ago the ephemeral runner was created.
		age time.Duration

		wantRequeueAfter time.Duration
		wantDeleted      bool
		wantFailed       bool
	}{
		{
			name:             "throttled backend retries while the runner is young",
			message:          throttled,
			age:              time.Minute,
			wantRequeueAfter: 30 * time.Second,
		},
		{
			name:             "unavailable backend retries while the runner is young",
			message:          unavailable,
			age:              time.Minute,
			wantRequeueAfter: 30 * time.Second,
		},
		{
			name:        "throttled backend re-creates the runner once it is about to expire",
			message:     throttled,
			age:         11 * time.Minute,
			wantDeleted: true,
		},
		{
			name:             "resource quota retries while the runner is young",
			message:          quota,
			age:              time.Minute,
			wantRequeueAfter: 30 * time.Second,
		},
		{
			name:        "resource quota re-creates the runner once it is about to expire",
			message:     quota,
			age:         11 * time.Minute,
			wantDeleted: true,
		},
		{
			name:       "rejection of the pod spec fails the runner",
			message:    noPriority,
			age:        time.Minute,
			wantFailed: true,
		},
		{
			name:       "rejection of the pod spec fails the runner when it is about to expire",
			message:    noPriority,
			age:        11 * time.Minute,
			wantFailed: true,
		},
		{
			name:       "gRPC status that is not transient fails the runner",
			message:    permission,
			age:        time.Minute,
			wantFailed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			key := types.NamespacedName{Namespace: "default", Name: "test-runner"}

			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))

			runner := newExampleRunner(key.Name, key.Namespace, "github-config-secret")
			runner.Finalizers = []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName}
			runner.CreationTimestamp = metav1.NewTime(time.Now().Add(-tt.age))
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Data: map[string][]byte{
					jitTokenKey:  []byte("jit-token"),
					"runnerId":   []byte("7"),
					"runnerName": []byte(key.Name),
				},
			}

			podCreateAttempts := 0
			c := ctrlfake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(runner, secret).
				WithStatusSubresource(&v1alpha1.EphemeralRunner{}).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, clt client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if _, ok := obj.(*corev1.Pod); ok {
							podCreateAttempts++
							return kerrors.NewForbidden(schema.GroupResource{Resource: "pods"}, obj.GetName(), errors.New(tt.message))
						}
						return clt.Create(ctx, obj, opts...)
					},
				}).
				Build()

			reconciler := &EphemeralRunnerReconciler{
				Client:    c,
				APIReader: c,
				Log:       logr.Discard(),
				Scheme:    scheme,
				ResourceBuilder: ResourceBuilder{
					Scheme: scheme,
				},
			}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			require.NoError(t, err)
			assert.Equal(t, 1, podCreateAttempts)
			assert.Equal(t, tt.wantRequeueAfter, result.RequeueAfter)

			got := new(v1alpha1.EphemeralRunner)
			require.NoError(t, c.Get(ctx, key, got))
			assert.Equal(t, tt.wantDeleted, !got.DeletionTimestamp.IsZero(), "ephemeral runner deletion")
			if tt.wantFailed {
				assert.Equal(t, v1alpha1.EphemeralRunnerPhaseFailed, got.Status.Phase)
				assert.Equal(t, ReasonInvalidPodFailure, got.Status.Reason)
				assert.Contains(t, got.Status.Message, tt.message)
			} else {
				assert.NotEqual(t, v1alpha1.EphemeralRunnerPhaseFailed, got.Status.Phase, "ephemeral runner must not be marked as failed")
				assert.Empty(t, got.Status.Reason)
			}
		})
	}
}
