package actionsgithubcom

import (
	"context"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const defaultGitHubToken = "gh_token"

func newAutoscalingRunnerSet(namespace, secretName string) *v1alpha1.AutoscalingRunnerSet {
	min := 1
	max := 10
	return &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asrs",
			Namespace: namespace,
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl:    "https://github.com/owner/repo",
			GitHubConfigSecret: secretName,
			MaxRunners:         &max,
			MinRunners:         &min,
			RunnerGroup:        "testgroup",
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: "ghcr.io/actions/runner",
						},
					},
				},
			},
		},
	}
}

func startManagers(t ginkgo.GinkgoTInterface, first manager.Manager, others ...manager.Manager) {
	for _, mgr := range append([]manager.Manager{first}, others...) {
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

func createNamespace(t ginkgo.GinkgoTInterface) (*corev1.Namespace, manager.Manager) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling" + RandStringRunes(5)},
	}

	err := k8sClient.Create(context.Background(), ns)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := k8sClient.Delete(context.Background(), ns)
		require.NoError(t, err)
	})

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Namespace:          ns.Name,
		MetricsBindAddress: "0",
	})
	require.NoError(t, err)

	return ns, mgr
}

func createDefaultSecret(t ginkgo.GinkgoTInterface, namespace string) *corev1.Secret {
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
