package actionsgithubcom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestEphemeralRunnerSetExpectationsGateCleanup(t *testing.T) {
	for _, branch := range []string{"revision", "outdated", "deletion", "finished", "scale down"} {
		t.Run(branch, func(t *testing.T) {
			r, api, set := newCacheLagReconciler(t)
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
			result, err := r.Reconcile(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, time.Second, result.RequeueAfter, "successful creates schedule observation even if no watch event arrives")
			require.NoError(t, api.Get(t.Context(), req.NamespacedName, set))
			switch branch {
			case "revision":
				set.Spec.ActionableRevision++
				require.NoError(t, api.Update(t.Context(), set))
			case "outdated":
				set.Status.Phase = v1alpha1.EphemeralRunnerSetPhaseOutdated
				require.NoError(t, api.Status().Update(t.Context(), set))
			case "deletion":
				require.NoError(t, api.Delete(t.Context(), set))
			case "scale down":
				set.Spec.Replicas = 0
				require.NoError(t, api.Update(t.Context(), set))
			}
			var snapshot v1alpha1.EphemeralRunnerList
			if branch == "finished" || branch == "scale down" {
				require.NoError(t, api.List(t.Context(), &snapshot))
				snapshot.Items = snapshot.Items[:1] // The other create is still missing.
				if branch == "finished" {
					snapshot.Items[0].Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
				}
			}
			deletes := 0
			r.Client = interceptor.NewClient(api, interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
					snapshot.DeepCopyInto(list.(*v1alpha1.EphemeralRunnerList))
					return nil
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					deletes++
					return c.Delete(ctx, obj, opts...)
				},
			})
			result, err = r.Reconcile(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, time.Second, result.RequeueAfter)
			require.Zero(t, deletes)
			require.NoError(t, api.Get(t.Context(), req.NamespacedName, set))
			require.Zero(t, set.Status.AppliedActionableRevision)
			require.Contains(t, set.Finalizers, EphemeralRunnerSetFinalizerName)
		})
	}
}

func TestEphemeralRunnerSetExpectationsNeedNoAuthoritativeList(t *testing.T) {
	r, api, set := newCacheLagReconciler(t)
	r.APIReader = interceptor.NewClient(api, interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			t.Error("normal scaling must not list through APIReader")
			return errors.New("unexpected API list")
		},
	})
	// Patch ID zero needs no authoritative status read either.
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
	_, err := r.Reconcile(t.Context(), req)
	require.NoError(t, err)
	r.APIReader = nil
	_, err = r.Reconcile(t.Context(), req)
	require.NoError(t, err)
	var status v1alpha1.EphemeralRunnerSet
	require.NoError(t, api.Get(t.Context(), req.NamespacedName, &status))
	require.Equal(t, v1alpha1.EphemeralRunnerSetPhaseRunning, status.Status.Phase)
}

func TestEphemeralRunnerSetClearsExpectationsAfterDeletion(t *testing.T) {
	r, api, set := newCacheLagReconciler(t)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}
	r.expectations.created(set, "lost", time.Now())
	set.Finalizers = nil
	require.NoError(t, api.Update(t.Context(), set))
	require.NoError(t, api.Delete(t.Context(), set))
	_, err := r.Reconcile(t.Context(), req)
	require.NoError(t, err)
	require.False(t, r.expectations.hasPending(req.NamespacedName))
}
