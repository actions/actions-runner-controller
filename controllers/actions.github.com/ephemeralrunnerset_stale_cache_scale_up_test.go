package actionsgithubcom

import (
	"context"
	"sync"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
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

// TestScaleUpDoesNotRecreateRunnersMissingFromTheCache models the reconcile
// that the watch event of the first created runner queues: it runs while the
// informer cache holds that runner but not the others created in the same
// batch, after the listener has published the same count under a new patch
// ID. Counting from the cache alone, it creates the missing runners again, so
// the set ends with more runners than Spec.Replicas.
func TestScaleUpDoesNotRecreateRunnersMissingFromTheCache(t *testing.T) {
	ctx := t.Context()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	set := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "stale-cache",
			Namespace:  "default",
			UID:        "stale-cache-set",
			Finalizers: []string{EphemeralRunnerSetFinalizerName},
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: 2,
			PatchID:  1,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    "https://github.com/owner/repo",
				GitHubConfigSecret: "github-config",
				RunnerScaleSetID:   1,
				PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  v1alpha1.EphemeralRunnerContainerName,
						Image: "ghcr.io/actions/actions-runner:latest",
					}},
				}},
			},
		},
	}
	configSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-config", Namespace: set.Namespace},
		Data:       map[string][]byte{"github_token": []byte("token")},
	}

	// The API server: holds every runner that was created.
	apiServer := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(set, configSecret).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}, &v1alpha1.EphemeralRunner{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		Build()

	// The cache: lists runners only once they are marked observed.
	var mu sync.Mutex
	observed := map[string]bool{}
	cached := interceptor.NewClient(apiServer, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if err := c.List(ctx, list, opts...); err != nil {
				return err
			}
			runners, ok := list.(*v1alpha1.EphemeralRunnerList)
			if !ok {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			visible := runners.Items[:0]
			for _, r := range runners.Items {
				if observed[r.Name] {
					visible = append(visible, r)
				}
			}
			runners.Items = visible
			return nil
		},
	})

	resolver := secretresolver.New(apiServer, scalefake.NewMultiClient(scalefake.WithClient(scalefake.NewClient())))
	cache := NewResourceCache()
	reconciler := &EphemeralRunnerSetReconciler{
		Client:          cached,
		APIReader:       apiServer,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: ResourceBuilder{Scheme: scheme, ResourceCache: &cache, SecretResolver: resolver},
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}

	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	var runners v1alpha1.EphemeralRunnerList
	require.NoError(t, apiServer.List(ctx, &runners, client.InNamespace(set.Namespace)))
	require.Len(t, runners.Items, 2, "the first reconcile creates Spec.Replicas runners")

	// The watch has delivered the first runner only.
	mu.Lock()
	observed[runners.Items[0].Name] = true
	mu.Unlock()

	// The listener publishes the same count again under a new patch ID, as it
	// does for every batch of job messages. Without it the visible runner
	// carries Spec.PatchID and the scaling block is skipped.
	var latest v1alpha1.EphemeralRunnerSet
	require.NoError(t, apiServer.Get(ctx, req.NamespacedName, &latest))
	latest.Spec.PatchID = 2
	require.NoError(t, apiServer.Update(ctx, &latest))

	_, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	require.NoError(t, apiServer.List(ctx, &runners, client.InNamespace(set.Namespace)))
	require.Len(t, runners.Items, 2, "a reconcile on a cache that lags the creates must not create runners again")
}
