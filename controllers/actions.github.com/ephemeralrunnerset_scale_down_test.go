package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// A scale down drops the registration finalizer and then deletes the runner.
// The finalizer patch wakes the next reconcile, which can still list the runner
// from a cache that has not seen the deletion.
func TestScaleDownDoesNotRemoveADeletingRunnerAgain(t *testing.T) {
	ctx := t.Context()

	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "runner",
			Namespace:  "default",
			Finalizers: []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName},
		},
		Status: v1alpha1.EphemeralRunnerStatus{RunnerID: 7},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runner).
		WithStatusSubresource(&v1alpha1.EphemeralRunner{}).
		Build()

	var removals []int64
	service := scalefake.NewClient(scalefake.WithRemoveRunnerFunc(func(_ context.Context, id int64) error {
		removals = append(removals, id)
		return nil
	}))
	r := &EphemeralRunnerSetReconciler{Client: c, APIReader: c, Scheme: scheme, Log: logr.Discard()}

	cached := new(v1alpha1.EphemeralRunner)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(runner), cached))
	stale := cached.DeepCopy()

	ok, err := r.deleteEphemeralRunnerWithActionsClient(ctx, cached, service, logr.Discard())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []int64{7}, removals)

	deleting := new(v1alpha1.EphemeralRunner)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(runner), deleting))
	require.False(t, deleting.DeletionTimestamp.IsZero())

	ok, err = r.deleteEphemeralRunnerWithActionsClient(ctx, stale, service, logr.Discard())
	require.NoError(t, err)
	require.True(t, ok, "a runner already being deleted counts toward the scale down")
	require.Equal(t, []int64{7}, removals, "the registration was removed twice")
}
