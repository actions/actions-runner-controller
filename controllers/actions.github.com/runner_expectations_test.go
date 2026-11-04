package actionsgithubcom

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestRunnerExpectationsObserve(t *testing.T) {
	var e runnerExpectations
	set := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "ns", UID: "uid"}}
	now := time.Now()
	e.created(set, "a", now)
	e.created(set, "b", now)
	list := &v1alpha1.EphemeralRunnerList{Items: []v1alpha1.EphemeralRunner{{ObjectMeta: metav1.ObjectMeta{Name: "a"}}}}
	satisfied, expired := e.observe(set, list, now)
	require.False(t, satisfied)
	require.False(t, expired)
	// The observed name is pruned permanently, not required in every later list.
	list.Items[0].Name = "b"
	satisfied, expired = e.observe(set, list, now)
	require.True(t, satisfied)
	require.False(t, expired)
	require.False(t, e.hasPending(client.ObjectKeyFromObject(set)))
}

func TestRunnerExpectationsDeleteObservation(t *testing.T) {
	for _, state := range []string{"active", "absent", "terminating"} {
		t.Run(state, func(t *testing.T) {
			var e runnerExpectations
			set := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "ns", UID: "uid"}}
			runner := &v1alpha1.EphemeralRunner{ObjectMeta: metav1.ObjectMeta{
				Name: "runner", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(set, v1alpha1.GroupVersion.WithKind("EphemeralRunnerSet"))},
			}}
			now := time.Now()
			e.deleted(runner, now)
			list := &v1alpha1.EphemeralRunnerList{}
			if state != "absent" {
				if state == "terminating" {
					runner.DeletionTimestamp = &metav1.Time{Time: now}
					runner.Finalizers = []string{"test/hold"}
				}
				list.Items = append(list.Items, *runner)
			}
			satisfied, expired := e.observe(set, list, now)
			require.Equal(t, state != "active", satisfied)
			require.False(t, expired)
		})
	}
}

func TestRunnerExpectationsExpiryAndIdentity(t *testing.T) {
	var e runnerExpectations
	set := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "ns", UID: "uid"}}
	now := time.Now()
	e.created(set, "lost", now)
	list := &v1alpha1.EphemeralRunnerList{}
	satisfied, expired := e.observe(set, list, now.Add(runnerExpectationsTimeout-time.Nanosecond))
	require.False(t, satisfied)
	require.False(t, expired)
	// Adding another name must not keep extending the original deadline.
	e.created(set, "also-lost", now.Add(time.Minute))
	satisfied, expired = e.observe(set, list, now.Add(runnerExpectationsTimeout))
	require.True(t, satisfied)
	require.True(t, expired)
	require.False(t, e.hasPending(client.ObjectKeyFromObject(set)))
	e.created(set, "old-owner", now)
	set.UID = "replacement"
	satisfied, expired = e.observe(set, list, now)
	require.True(t, satisfied)
	require.False(t, expired)
	e.created(set, "replacement-runner", now)
	set.UID = "another-owner"
	e.created(set, "another-runner", now)
	list.Items = []v1alpha1.EphemeralRunner{{ObjectMeta: metav1.ObjectMeta{Name: "another-runner"}}}
	satisfied, expired = e.observe(set, list, now)
	require.True(t, satisfied, "recording a new owner's write must discard the old owner's names")
	require.False(t, expired)
	e.created(set, "cleared", now)
	e.clear(client.ObjectKeyFromObject(set))
	require.False(t, e.hasPending(client.ObjectKeyFromObject(set)))
}

func TestRunnerExpectationsConcurrentOwners(t *testing.T) {
	var e runnerExpectations
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			set := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "ns", UID: "uid"}}
			if i%2 == 0 {
				set.Namespace = "other-ns"
			}
			e.created(set, "runner", time.Now())
		})
	}
	wg.Wait()
	require.Len(t, e.pending, 2)
}

func TestRunnerExpectationsDoNotRecordFailedWrites(t *testing.T) {
	r, api, set := newCacheLagReconciler(t)
	writeErr := errors.New("write rejected")
	r.Client = interceptor.NewClient(api, interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error { return writeErr },
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error { return writeErr },
	})
	require.ErrorIs(t, r.createEphemeralRunners(t.Context(), set, 2, logr.Discard()), writeErr)
	require.False(t, r.expectations.hasPending(client.ObjectKeyFromObject(set)))
	runner, err := r.newEphemeralRunner(set)
	require.NoError(t, err)
	require.NoError(t, api.Create(t.Context(), runner))
	require.ErrorIs(t, r.deleteEphemeralRunner(t.Context(), runner), writeErr)
	require.False(t, r.expectations.hasPending(client.ObjectKeyFromObject(set)))
}
