package actionsgithubcom

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Writes reach the API server immediately, but the informer list stays empty
// across reconciles, including the one triggered by the controller's own status
// patch. A second scale decision must account for the first pass's creates.
func TestEphemeralRunnerSetScaleUpWithLaggingCache(t *testing.T) {
	for _, partialFailure := range []bool{false, true} {
		name := "successful batch"
		if partialFailure {
			name = "partially failed batch"
		}
		t.Run(name, func(t *testing.T) {
			r, api, set := newCacheLagReconciler(t)
			var attempts atomic.Int32
			createErr := errors.New("create failed")
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*v1alpha1.EphemeralRunner); ok && attempts.Add(1) == 2 && partialFailure {
						return createErr
					}
					return c.Create(ctx, obj, opts...)
				},
			})
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
			_, err := r.Reconcile(t.Context(), req)
			if partialFailure {
				require.ErrorIs(t, err, createErr)
			} else {
				require.NoError(t, err)
			}
			for range 3 {
				_, err = r.Reconcile(t.Context(), req)
				require.NoError(t, err)
				var runners v1alpha1.EphemeralRunnerList
				require.NoError(t, api.List(t.Context(), &runners))
				want := set.Spec.Replicas
				if partialFailure {
					want-- // Wait for the successful create before filling the failed slot.
				}
				require.Len(t, runners.Items, want, "reconciling before the informer observes creates must not over-create")
			}
			r.Client = api // The informer catches up to the successful writes.
			_, err = r.Reconcile(t.Context(), req)
			require.NoError(t, err)
			var runners v1alpha1.EphemeralRunnerList
			require.NoError(t, api.List(t.Context(), &runners))
			require.Len(t, runners.Items, set.Spec.Replicas)
		})
	}
}

func TestEphemeralRunnerSetScaleUpWaitsForDeletion(t *testing.T) {
	r, api, set := newCacheLagReconciler(t)
	r.Client = api
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
	_, err := r.Reconcile(t.Context(), req)
	require.NoError(t, err)
	_, err = r.Reconcile(t.Context(), req) // Observe the initial creates.
	require.NoError(t, err)
	var stale v1alpha1.EphemeralRunnerList
	require.NoError(t, api.List(t.Context(), &stale))
	runner := &stale.Items[0]
	runner.Finalizers = []string{"test/hold-deletion"}
	require.NoError(t, api.Update(t.Context(), runner))
	_, err = r.deleteEphemeralRunnerWithActionsClient(t.Context(), runner, nil, logr.Discard())
	require.NoError(t, err)
	r.Client = interceptor.NewClient(api, interceptor.Funcs{
		List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
			stale.DeepCopyInto(list.(*v1alpha1.EphemeralRunnerList))
			return nil
		},
	})
	result, err := r.Reconcile(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, time.Second, result.RequeueAfter)

	// Seeing the deletion timestamp satisfies the delete expectation even
	// though the finalizer keeps the object around and occupying a scale-up slot.
	r.Client = api
	for range 3 {
		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)
		var runners v1alpha1.EphemeralRunnerList
		require.NoError(t, api.List(t.Context(), &runners))
		require.Len(t, runners.Items, set.Spec.Replicas)
	}
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(runner), runner))
	runner.Finalizers = nil
	require.NoError(t, api.Update(t.Context(), runner))
	_, err = r.Reconcile(t.Context(), req)
	require.NoError(t, err)
	var runners v1alpha1.EphemeralRunnerList
	require.NoError(t, api.List(t.Context(), &runners))
	require.Len(t, runners.Items, set.Spec.Replicas)
	for _, runner := range runners.Items {
		require.True(t, runner.DeletionTimestamp.IsZero())
	}
}

func TestEphemeralRunnerSetScaleDownWithLaggingCache(t *testing.T) {
	r, api, set := newCacheLagReconciler(t)
	// There were three runners when this informer snapshot was taken. One
	// deletion already completed, leaving exactly the desired two at the API.
	var stale v1alpha1.EphemeralRunnerList
	for range 3 {
		runner, err := r.newEphemeralRunner(set)
		require.NoError(t, err)
		runner.Finalizers = nil
		require.NoError(t, api.Create(t.Context(), runner))
		stale.Items = append(stale.Items, *runner)
	}
	// The previous reconcile issued the deletion, but the informer still
	// returns the three-runner snapshot.
	_, err := r.deleteEphemeralRunnerWithActionsClient(t.Context(), &stale.Items[2], nil, logr.Discard())
	require.NoError(t, err)
	r.Client = interceptor.NewClient(api, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if runners, ok := list.(*v1alpha1.EphemeralRunnerList); ok {
				stale.DeepCopyInto(runners)
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
	r.SecretResolver = &stubSecretResolver{client: scalefake.NewClient()}
	for range 3 {
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
		var runners v1alpha1.EphemeralRunnerList
		require.NoError(t, api.List(t.Context(), &runners))
		require.Len(t, runners.Items, set.Spec.Replicas, "a stale pre-delete count must not delete another runner")
	}
}

func TestEphemeralRunnerSetScaleUpFiltersOwnership(t *testing.T) {
	r, api, set := newCacheLagReconciler(t)
	r.Client = api
	for _, owner := range []*v1alpha1.EphemeralRunnerSet{
		{ObjectMeta: metav1.ObjectMeta{Name: "other-set", Namespace: set.Namespace, UID: "other-set"}},
		{ObjectMeta: metav1.ObjectMeta{Name: set.Name, Namespace: "other-ns", UID: "other-namespace"}},
	} {
		runner, err := r.newEphemeralRunner(owner)
		require.NoError(t, err)
		require.NoError(t, api.Create(t.Context(), runner))
	}
	for range 3 {
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
	}
	var runners v1alpha1.EphemeralRunnerList
	require.NoError(t, api.List(t.Context(), &runners))
	owned := 0
	for _, runner := range runners.Items {
		if metav1.IsControlledBy(&runner, set) {
			owned++
		}
	}
	require.Equal(t, set.Spec.Replicas, owned)
	require.Len(t, runners.Items, set.Spec.Replicas+2, "other owners' runners must remain untouched")
}

func newCacheLagReconciler(t *testing.T) (*EphemeralRunnerSetReconciler, client.WithWatch, *v1alpha1.EphemeralRunnerSet) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	set := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-set", Namespace: "test-ns", UID: "test-set",
			Finalizers: []string{EphemeralRunnerSetFinalizerName},
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: 2,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL: "https://github.com/org/repo",
				PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "runner", Image: "runner:latest"}},
				}},
			},
		},
	}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).Build()
	cached := interceptor.NewClient(api, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if runners, ok := list.(*v1alpha1.EphemeralRunnerList); ok {
				runners.Items = nil
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
	r := &EphemeralRunnerSetReconciler{
		Client: cached, APIReader: api, Log: logr.Discard(), Scheme: scheme,
		ResourceBuilder: ResourceBuilder{Scheme: scheme, ResourceCache: newTestResourceCache()},
	}
	return r, api, set
}
