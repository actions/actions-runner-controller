package actionsgithubcom

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const defaultGitHubToken = "gh_token"

func startManagers(t ginkgo.GinkgoTInterface, first manager.Manager, others ...manager.Manager) {
	for _, mgr := range append([]manager.Manager{first}, others...) {
		if err := SetupIndexers(mgr); err != nil {
			t.Fatalf("failed to setup indexers: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())

		g, ctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			return mgr.Start(ctx)
		})

		t.Cleanup(func() {
			cancel()
			require.NoError(t, g.Wait())
		})
	}
}

func createNamespaces(t ginkgo.GinkgoTInterface, numNamespaces int) ([]*corev1.Namespace, manager.Manager) {
	namespaces := make([]*corev1.Namespace, 0)
	defaultNamespaces := make(map[string]cache.Config)
	for range numNamespaces {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling" + RandStringRunes(5)},
		}
		namespaces = append(namespaces, ns)

		err := k8sClient.Create(context.Background(), ns)
		require.NoError(t, err)

		t.Cleanup(func() {
			err := k8sClient.Delete(context.Background(), ns)
			require.NoError(t, err)
		})

		defaultNamespaces[ns.Name] = cache.Config{}
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Cache: cache.Options{DefaultNamespaces: defaultNamespaces},
	})
	require.NoError(t, err)

	return namespaces, mgr
}

func createNamespace(t ginkgo.GinkgoTInterface, client client.Client) (*corev1.Namespace, manager.Manager) {
	namespaces, mgr := createNamespaces(t, 1)
	return namespaces[0], mgr
}

func createDefaultSecret(t ginkgo.GinkgoTInterface, client client.Client, namespace string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github-config-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"github_token": []byte(defaultGitHubToken),
		},
	}

	err := k8sClient.Create(context.Background(), secret)
	require.NoError(t, err)

	return secret
}
