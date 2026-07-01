package actionsgithubcom

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestEphemeralRunnerSetCreateEphemeralRunnersCreatesAllRunnersWithResourceCache(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	runnerSet := newParallelTestEphemeralRunnerSet(10)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runnerSet).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}, &v1alpha1.EphemeralRunner{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		Build()
	resourceCache := NewResourceCache()
	reconciler := &EphemeralRunnerSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Log:    logr.Discard(),
		ResourceBuilder: &ResourceBuilder{
			Scheme:        scheme,
			ResourceCache: &resourceCache,
		},
	}

	err := reconciler.createEphemeralRunners(ctx, runnerSet, 10, logr.Discard())
	require.NoError(t, err)

	runnerList := &v1alpha1.EphemeralRunnerList{}
	require.NoError(t, fakeClient.List(ctx, runnerList, client.InNamespace(runnerSet.Namespace), client.MatchingFields{resourceOwnerKey: runnerSet.Name}))
	require.Len(t, runnerList.Items, 10)
	names := make(map[string]struct{}, len(runnerList.Items))
	for i := range runnerList.Items {
		name := runnerList.Items[i].Name
		require.NotEmpty(t, name)
		_, exists := names[name]
		require.False(t, exists, "runner name %q should be unique", name)
		names[name] = struct{}{}
	}
}

func TestEphemeralRunnerSetDeleteIdleRunnersContinuesAfterJobStillRunning(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	runnerSet := newParallelTestEphemeralRunnerSet(0)
	objects := newBenchmarkIdleEphemeralRunnerObjects(runnerSet, 3)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}, &v1alpha1.EphemeralRunner{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		Build()
	var calls int64
	scaleClient := scalefake.NewClient(scalefake.WithRemoveRunnerFunc(func(ctx context.Context, runnerID int64) error {
		if atomic.AddInt64(&calls, 1) == 1 {
			return scaleset.JobStillRunningError
		}
		return nil
	}))
	reconciler := &EphemeralRunnerSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Log:    logr.Discard(),
		ResourceBuilder: &ResourceBuilder{
			SecretResolver: secretresolver.New(fakeClient, scalefake.NewMultiClient(scalefake.WithClient(scaleClient))),
		},
	}

	runnerList := &v1alpha1.EphemeralRunnerList{}
	require.NoError(t, fakeClient.List(ctx, runnerList, client.InNamespace(runnerSet.Namespace), client.MatchingFields{resourceOwnerKey: runnerSet.Name}))
	state := newEphemeralRunnersByStates(runnerList, true, true)
	err := reconciler.deleteIdleEphemeralRunners(ctx, runnerSet, state.pending, state.running, 2, logr.Discard())
	require.NoError(t, err)

	runnerList = &v1alpha1.EphemeralRunnerList{}
	require.NoError(t, fakeClient.List(ctx, runnerList, client.InNamespace(runnerSet.Namespace), client.MatchingFields{resourceOwnerKey: runnerSet.Name}))
	require.Len(t, runnerList.Items, 1)
	require.GreaterOrEqual(t, atomic.LoadInt64(&calls), int64(3))
}

func newParallelTestEphemeralRunnerSet(replicas int) *v1alpha1.EphemeralRunnerSet {
	runnerSet := NewMinimalEphemeralRunnerSet("default", "test-ers")
	runnerSet.APIVersion = v1alpha1.GroupVersion.String()
	runnerSet.Kind = "EphemeralRunnerSet"
	runnerSet.UID = "test-ers-uid"
	runnerSet.Spec.PatchID = 0
	runnerSet.Spec.Replicas = replicas
	controllerutil.AddFinalizer(runnerSet, EphemeralRunnerSetFinalizerName)
	if runnerSet.Annotations == nil {
		runnerSet.Annotations = map[string]string{}
	}
	runnerSet.Annotations[AnnotationKeyIntegrityHash] = ephemeralRunnerSetIntegrityHash(runnerSet)
	return runnerSet
}
