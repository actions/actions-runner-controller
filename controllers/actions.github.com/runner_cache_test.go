package actionsgithubcom

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newRunnerCacheFixture(t *testing.T, replicas int) (*EphemeralRunnerSetReconciler, client.WithWatch, *v1alpha1.EphemeralRunnerSet) {
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
			Replicas: replicas,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL: "https://github.com/org/repo",
				PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "runner", Image: "runner:latest"}},
				}},
			},
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{Phase: v1alpha1.EphemeralRunnerSetPhaseRunning},
	}
	api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}, &v1alpha1.EphemeralRunner{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*v1alpha1.EphemeralRunner); ok {
					obj.SetUID(uuid.NewUUID())
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	return &EphemeralRunnerSetReconciler{
		Client: api, APIReader: api, Log: logr.Discard(), Scheme: scheme,
		ResourceBuilder: ResourceBuilder{
			Scheme: scheme, ResourceCache: newTestResourceCache(),
			SecretResolver: &stubSecretResolver{client: scalefake.NewClient()},
		},
	}, api, set
}

func cachedRunnerList(api client.WithWatch, snapshot *v1alpha1.EphemeralRunnerList) client.WithWatch {
	return interceptor.NewClient(api, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if runners, ok := list.(*v1alpha1.EphemeralRunnerList); ok {
				snapshot.DeepCopyInto(runners)
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
}

func listCachedRunners(t *testing.T, api client.Client) *v1alpha1.EphemeralRunnerList {
	t.Helper()
	var runners v1alpha1.EphemeralRunnerList
	require.NoError(t, api.List(t.Context(), &runners))
	return &runners
}

func reconcileCachedSet(t *testing.T, r *EphemeralRunnerSetReconciler, set *v1alpha1.EphemeralRunnerSet) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	return result
}

func TestRunnerCacheEmptyInventorySkipsSnapshot(t *testing.T) {
	for _, emptyShard := range []bool{false, true} {
		t.Run(fmt.Sprintf("emptyShard=%v", emptyShard), func(t *testing.T) {
			r, _, set := newRunnerCacheFixture(t, 0)
			key := client.ObjectKeyFromObject(set)
			if emptyShard {
				r.runnerCreationCache.sets.Store(key, &runnerSetCache{uid: set.UID})
			}
			// A nil snapshot verifies that empty inventories never inspect it.
			unseen, expired := r.runnerCreationCache.observe(set, nil, time.Now())
			require.Zero(t, unseen)
			require.Zero(t, expired)
			_, found := r.runnerCreationCache.sets.Load(key)
			require.False(t, found)
		})
	}
}

func BenchmarkRunnerCacheEmptyInventory(b *testing.B) {
	var r EphemeralRunnerSetReconciler
	set := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "ns", UID: "set"},
	}
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		r.runnerCreationCache.observe(set, nil, now)
	}
}

func TestRunnerCacheClear(t *testing.T) {
	r, _, set := newRunnerCacheFixture(t, 0)
	runner, err := r.newEphemeralRunner(set)
	require.NoError(t, err)
	runner.Name = "runner"
	runner.UID = "runner"
	r.runnerCreationCache.record(set, runner.UID, time.Now())
	key := client.ObjectKeyFromObject(set)
	_, found := r.runnerCreationCache.sets.Load(key)
	require.True(t, found)

	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				r.runnerCreationCache.record(set, runner.UID, time.Now())
			}
		})
	}
	for range 100 {
		r.ClearRunnerCreationCache()
	}
	workers.Wait()
	r.ClearRunnerCreationCache()
	_, found = r.runnerCreationCache.sets.Load(key)
	require.False(t, found)
	r.runnerCreationCache.record(set, runner.UID, time.Now())
	_, found = r.runnerCreationCache.sets.Load(key)
	require.True(t, found, "clearing does not disable recording")
}

func TestRunnerCacheAccountsForUnseenCreatesAcrossNewBatches(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 4)
	var snapshot v1alpha1.EphemeralRunnerList
	r.Client = cachedRunnerList(api, &snapshot)
	result := reconcileCachedSet(t, r, set)
	require.Zero(t, result.RequeueAfter, "unseen creates must not schedule reconciliation")
	require.Len(t, listCachedRunners(t, api).Items, 4)
	for range 3 {
		reconcileCachedSet(t, r, set)
	}
	require.Len(t, listCachedRunners(t, api).Items, 4)

	// A new listener batch must act immediately against the adjusted count.
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Spec.Replicas, set.Spec.PatchID = 7, 2
	require.NoError(t, api.Update(t.Context(), set))
	reconcileCachedSet(t, r, set)
	require.Len(t, listCachedRunners(t, api).Items, 7)

	snapshot.Items = listCachedRunners(t, api).Items[:3]
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Spec.Replicas, set.Spec.PatchID = 9, 3
	require.NoError(t, api.Update(t.Context(), set))
	reconcileCachedSet(t, r, set)
	require.Len(t, listCachedRunners(t, api).Items, 9, "partial observations must not double count")
	snapshot = *listCachedRunners(t, api)
	require.Zero(t, reconcileCachedSet(t, r, set).RequeueAfter)
	_, found := r.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
	require.False(t, found)
}

func TestRunnerCacheHasIndependentSixtySecondExpiry(t *testing.T) {
	r, _, set := newRunnerCacheFixture(t, 0)
	runner, err := r.newEphemeralRunner(set)
	require.NoError(t, err)
	now := time.Now()
	runner.Name = "first"
	runner.UID = "first"
	r.runnerCreationCache.record(set, runner.UID, now)
	runner.Name = "second"
	runner.UID = "second"
	r.runnerCreationCache.record(set, runner.UID, now.Add(30*time.Second))
	var snapshot v1alpha1.EphemeralRunnerList
	unseen, expired := r.runnerCreationCache.observe(set, &snapshot, now.Add(59*time.Second))
	require.Equal(t, 2, unseen)
	require.Zero(t, expired)
	unseen, expired = r.runnerCreationCache.observe(set, &snapshot, now.Add(60*time.Second))
	require.Equal(t, 1, unseen)
	require.Equal(t, 1, expired)
	unseen, expired = r.runnerCreationCache.observe(set, &snapshot, now.Add(90*time.Second))
	require.Zero(t, unseen)
	require.Equal(t, 1, expired)
	_, found := r.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
	require.False(t, found)
}

func TestRunnerCacheDoesNotAddAPIRequests(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 3)
	var snapshot v1alpha1.EphemeralRunnerList
	gets, lists, creates := 0, 0, 0
	var mu sync.Mutex
	r.Client = interceptor.NewClient(cachedRunnerList(api, &snapshot), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			gets++
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			lists++
			return c.List(ctx, list, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			mu.Lock()
			creates++
			mu.Unlock()
			require.Empty(t, obj.GetName(), "retain the existing API-generated name behavior")
			return c.Create(ctx, obj, opts...)
		},
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			t.Error("unexpected extra metadata patch")
			return errors.New("unexpected patch")
		},
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			t.Error("unexpected extra status patch")
			return errors.New("unexpected status patch")
		},
	})
	for range 4 {
		reconcileCachedSet(t, r, set)
	}
	require.Equal(t, 4, gets)
	require.Equal(t, 4, lists)
	require.Equal(t, 3, creates)
}

func TestRunnerCacheKeepsStatusReconciliationActive(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 2)
	var snapshot v1alpha1.EphemeralRunnerList
	r.Client = cachedRunnerList(api, &snapshot)
	reconcileCachedSet(t, r, set)
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Status.Phase = ""
	require.NoError(t, api.Status().Update(t.Context(), set))
	reconcileCachedSet(t, r, set)
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	require.Equal(t, v1alpha1.EphemeralRunnerSetPhaseRunning, set.Status.Phase)
}

func TestRunnerCacheCleanupProcessesVisibleRunnersWithoutLosingUnseenCreates(t *testing.T) {
	for _, deletingSet := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleting=%v", deletingSet), func(t *testing.T) {
			r, api, set := newRunnerCacheFixture(t, 2)
			reconcileCachedSet(t, r, set)
			runners := listCachedRunners(t, api)
			snapshot := v1alpha1.EphemeralRunnerList{Items: runners.Items[:1]}
			snapshot.Items[0].Status.RunnerID = 7
			r.Client = cachedRunnerList(api, &snapshot)
			require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
			if deletingSet {
				require.NoError(t, api.Delete(t.Context(), set))
			} else {
				set.Spec.ActionableRevision = 1
				require.NoError(t, api.Update(t.Context(), set))
			}
			reconcileCachedSet(t, r, set)
			var visible v1alpha1.EphemeralRunner
			require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(&snapshot.Items[0]), &visible))
			require.False(t, visible.DeletionTimestamp.IsZero(), "visible cleanup must not wait for unseen creates")
			snapshot.Items = nil
			reconcileCachedSet(t, r, set)
			require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
			require.Contains(t, set.Finalizers, EphemeralRunnerSetFinalizerName)
			require.Zero(t, set.Status.AppliedActionableRevision, "unseen old-spec creates must still be revisited")
		})
	}
}

func TestRunnerCacheCountsObservedTerminatingCapacity(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 5)
	reconcileCachedSet(t, r, set)
	snapshot := *listCachedRunners(t, api)
	r.Client = cachedRunnerList(api, &snapshot)
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Spec.Replicas = 3
	require.NoError(t, api.Update(t.Context(), set))
	reconcileCachedSet(t, r, set)
	deleted := 0
	for _, runner := range listCachedRunners(t, api).Items {
		if !runner.DeletionTimestamp.IsZero() {
			deleted++
		}
	}
	require.Equal(t, 2, deleted)
	snapshot = *listCachedRunners(t, api)
	// A higher target still counts terminating runners as occupied capacity.
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Spec.Replicas = 6
	require.NoError(t, api.Update(t.Context(), set))
	reconcileCachedSet(t, r, set)
	require.Len(t, listCachedRunners(t, api).Items, 6)
}

func TestRunnerCachePrunesObservedCreatesIncludingTerminalRunners(t *testing.T) {
	for _, phase := range []v1alpha1.EphemeralRunnerPhase{"", v1alpha1.EphemeralRunnerPhaseSucceeded, v1alpha1.EphemeralRunnerPhaseFailed, v1alpha1.EphemeralRunnerPhaseOutdated} {
		t.Run(string(phase), func(t *testing.T) {
			r, api, set := newRunnerCacheFixture(t, 1)
			reconcileCachedSet(t, r, set)
			runners := listCachedRunners(t, api)
			runners.Items[0].Status.Phase = phase
			unseen, expired := r.runnerCreationCache.observe(set, runners, time.Now())
			require.Zero(t, unseen)
			require.Zero(t, expired)
			_, found := r.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
			require.False(t, found)
		})
	}
}

func TestRunnerCacheObservesRunnerUIDWithoutChangingSnapshot(t *testing.T) {
	for _, observation := range []string{"same-uid", "same-name", "different-owner"} {
		t.Run(observation, func(t *testing.T) {
			r, _, set := newRunnerCacheFixture(t, 0)
			runner, err := r.newEphemeralRunner(set)
			require.NoError(t, err)
			runner.Name, runner.UID = "runner", "original"
			now := time.Now()
			r.runnerCreationCache.record(set, runner.UID, now)
			snapshot := v1alpha1.EphemeralRunnerList{Items: []v1alpha1.EphemeralRunner{*runner.DeepCopy()}}
			switch observation {
			case "same-uid":
				snapshot.Items[0].Name = "different-name"
			case "same-name":
				snapshot.Items[0].UID = "replacement"
			case "different-owner":
				snapshot.Items[0].OwnerReferences[0].UID = "different-set"
			}
			before := snapshot.DeepCopy()
			unseen, expired := r.runnerCreationCache.observe(set, &snapshot, now)
			require.Zero(t, expired)
			require.Equal(t, before, &snapshot)
			if observation == "same-uid" {
				require.Zero(t, unseen)
			} else {
				require.Equal(t, 1, unseen)
			}
		})
	}
}

func TestRunnerCacheExpiredMissingRunnerCanBeReplaced(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 1)
	runner, err := r.newEphemeralRunner(set)
	require.NoError(t, err)
	runner.Name = "short-lived-runner"
	runner.UID = "short-lived-runner"
	r.runnerCreationCache.record(set, runner.UID, time.Now().Add(-runnerCacheTTL))
	reconcileCachedSet(t, r, set)
	require.Len(t, listCachedRunners(t, api).Items, 1)
}

func TestRunnerCacheRecordsOnlySuccessfulWrites(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 2)
	writeErr := errors.New("write rejected")
	r.Client = interceptor.NewClient(api, interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error { return writeErr },
	})
	_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.ErrorIs(t, err, writeErr)
	_, found := r.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
	require.False(t, found)
	r.Client = api
	reconcileCachedSet(t, r, set)
	require.Len(t, listCachedRunners(t, api).Items, 2)
}

func TestRunnerCacheParentIdentityAndDisappearance(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 2)
	reconcileCachedSet(t, r, set)
	replacement := set.DeepCopy()
	replacement.UID = "replacement"
	var empty v1alpha1.EphemeralRunnerList
	unseen, expired := r.runnerCreationCache.observe(replacement, &empty, time.Now())
	require.Zero(t, unseen)
	require.Zero(t, expired)
	_, found := r.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
	require.False(t, found)
	runner, err := r.newEphemeralRunner(set)
	require.NoError(t, err)
	runner.Name = "missing"
	runner.UID = "missing"
	r.runnerCreationCache.record(set, runner.UID, time.Now())
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Finalizers = nil
	require.NoError(t, api.Update(t.Context(), set))
	require.NoError(t, api.Delete(t.Context(), set))
	require.Zero(t, reconcileCachedSet(t, r, set).RequeueAfter)
	_, found = r.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
	require.False(t, found)
}

func TestRunnerCacheConcurrentBatchRecordsArePartitionedBySet(t *testing.T) {
	r, _, set := newRunnerCacheFixture(t, 0)
	now := time.Now()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			owner := set.DeepCopy()
			owner.Name = fmt.Sprintf("set-%d", i%4)
			runner, err := r.newEphemeralRunner(owner)
			require.NoError(t, err)
			runner.Name = fmt.Sprintf("runner-%d", i)
			runner.UID = types.UID(runner.Name)
			r.runnerCreationCache.record(owner, runner.UID, now)
		})
	}
	wg.Wait()
	for i := range 4 {
		owner := set.DeepCopy()
		owner.Name = fmt.Sprintf("set-%d", i)
		var empty v1alpha1.EphemeralRunnerList
		unseen, expired := r.runnerCreationCache.observe(owner, &empty, now)
		require.Equal(t, 25, unseen)
		require.Zero(t, expired)
	}
}

func TestRunnerCachePrunesCreatesBeforeScaleDown(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 1)
	reconcileCachedSet(t, r, set)
	key := client.ObjectKeyFromObject(set)
	_, found := r.runnerCreationCache.sets.Load(key)
	require.True(t, found)
	require.NoError(t, api.Get(t.Context(), key, set))
	set.Spec.Replicas = 0
	require.NoError(t, api.Update(t.Context(), set))
	r.Client = interceptor.NewClient(api, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			_, found := r.runnerCreationCache.sets.Load(key)
			require.False(t, found, "the observed create must be pruned before deletion")
			return c.Delete(ctx, obj, opts...)
		},
	})
	reconcileCachedSet(t, r, set)
	runners := listCachedRunners(t, api)
	require.Len(t, runners.Items, 1)
	require.False(t, runners.Items[0].DeletionTimestamp.IsZero())
	_, found = r.runnerCreationCache.sets.Load(key)
	require.False(t, found)
}

func TestRunnerCacheTargetReductionDoesNotTreatUnseenCreatesAsDeletionCandidates(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 5)
	var snapshot v1alpha1.EphemeralRunnerList
	r.Client = cachedRunnerList(api, &snapshot)
	reconcileCachedSet(t, r, set)
	require.NoError(t, api.Get(t.Context(), client.ObjectKeyFromObject(set), set))
	set.Spec.Replicas = 1
	require.NoError(t, api.Update(t.Context(), set))
	reconcileCachedSet(t, r, set)
	for _, runner := range listCachedRunners(t, api).Items {
		require.True(t, runner.DeletionTimestamp.IsZero())
	}
	snapshot = *listCachedRunners(t, api)
	reconcileCachedSet(t, r, set)
	live := 0
	for _, runner := range listCachedRunners(t, api).Items {
		if runner.DeletionTimestamp.IsZero() {
			live++
		}
	}
	require.Equal(t, 1, live)
}

func TestRunnerCachePartialFailedBatchKeepsSuccessfulReferences(t *testing.T) {
	r, api, set := newRunnerCacheFixture(t, 4)
	var mu sync.Mutex
	attempts := 0
	writeErr := errors.New("create rejected")
	r.Client = interceptor.NewClient(api, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			mu.Lock()
			attempts++
			attempt := attempts
			mu.Unlock()
			if attempt <= 2 {
				return c.Create(ctx, obj, opts...)
			}
			return writeErr
		},
	})
	_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.ErrorIs(t, err, writeErr)
	var snapshot v1alpha1.EphemeralRunnerList
	unseen, expired := r.runnerCreationCache.observe(set, &snapshot, time.Now())
	require.Equal(t, 2, unseen)
	require.Zero(t, expired)
	r.Client = cachedRunnerList(api, &snapshot)
	reconcileCachedSet(t, r, set)
	require.Len(t, listCachedRunners(t, api).Items, 4)
}
