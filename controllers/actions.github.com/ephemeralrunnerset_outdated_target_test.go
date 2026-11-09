package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

// TestEphemeralRunnerSetIgnoresTheTargetCountOnTheReconcileThatFindsOutdatedRunners
// pins that a rejected runner spec beats the count the listener asked for.
//
// Spec.Replicas is what the listener wanted, computed before any runner had
// reported anything. Once a runner rejects the spec it was given, that count is
// a request to create more runners that will reject it in exactly the same way,
// so it must not be acted on.
//
// The phase alone is not enough to enforce that. Status.Phase only turns
// Outdated at the end of the reconcile that discovers the rejection, so the
// reconcile that finds it first still reaches the scaling block below with a
// phase that still says Running, and scales up against a spec already known to
// be bad. Only the reconcile after that takes the early return. The gate has to
// be the runners themselves.
func TestEphemeralRunnerSetIgnoresTheTargetCountOnTheReconcileThatFindsOutdatedRunners(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	const (
		name      = "test-ers"
		namespace = "default"
	)

	ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Finalizers: []string{EphemeralRunnerSetFinalizerName},
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			// The listener has published a fresh target since the rejection.
			// It knows nothing about it: it counts queued jobs, not whether the
			// runners it asks for can run at all.
			Replicas:           5,
			PatchID:            4,
			ActionableRevision: 2,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL: "https://github.com/owner/repo",
			},
		},
		Status: v1alpha1.EphemeralRunnerSetStatus{
			// Still Running: this is the reconcile that discovers the rejection.
			Phase:                     v1alpha1.EphemeralRunnerSetPhaseRunning,
			AppliedActionableRevision: 2,
		},
	}

	controller := true
	rejectedRunner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-that-rejected-the-spec",
			Namespace: namespace,
			Annotations: map[string]string{
				AnnotationKeyActionableRevision: "2",
				AnnotationKeyPatchID:            "3",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "EphemeralRunnerSet",
					Name:       name,
					UID:        "test-uid",
					Controller: &controller,
				},
			},
		},
		Status: v1alpha1.EphemeralRunnerStatus{
			Phase: v1alpha1.EphemeralRunnerPhaseOutdated,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ephemeralRunnerSet, rejectedRunner).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}, &v1alpha1.EphemeralRunner{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		Build()

	resourceCache := NewResourceCache()
	reconciler := &EphemeralRunnerSetReconciler{
		Client:    fakeClient,
		APIReader: fakeClient,
		Log:       logr.Discard(),
		Scheme:    scheme,
		ResourceBuilder: ResourceBuilder{
			ResourceCache: &resourceCache,
			Scheme:        scheme,
		},
	}

	key := types.NamespacedName{Namespace: namespace, Name: name}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	runners := new(v1alpha1.EphemeralRunnerList)
	require.NoError(t, fakeClient.List(context.Background(), runners, client.InNamespace(namespace)))

	for _, runner := range runners.Items {
		require.Equal(
			t,
			rejectedRunner.Name,
			runner.Name,
			"no runner may be created from a spec the runners have already rejected, whatever count the listener asked for",
		)
	}

	got := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(t, fakeClient.Get(context.Background(), key, got))
	require.Equal(
		t,
		v1alpha1.EphemeralRunnerSetPhaseOutdated,
		got.Status.Phase,
		"the rejection must be recorded on the same reconcile that found it",
	)
}
