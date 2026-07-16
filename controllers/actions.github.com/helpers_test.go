package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
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

func createNamespace(t ginkgo.GinkgoTInterface, client client.Client) (*corev1.Namespace, manager.Manager) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling" + RandStringRunes(5)},
	}

	err := client.Create(context.Background(), ns)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := client.Delete(context.Background(), ns)
		require.NoError(t, err)
	})

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Controller: config.Controller{
			SkipNameValidation: ptr.To(true),
		},
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				ns.Name: {},
			},
		},
	})
	require.NoError(t, err)

	return ns, mgr
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

func TestEphemeralRunnerSetActionableSpecChanged(t *testing.T) {
	base := func() *v1alpha1.EphemeralRunnerSet {
		return &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      map[string]string{"app": "arc"},
				Annotations: map[string]string{"note": "keep"},
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				PatchID:  10,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					PodTemplateSpec: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner:old"}},
						},
					},
				},
				EphemeralRunnerMetadata: &v1alpha1.ResourceMeta{
					Labels:      map[string]string{"meta-label": "v1"},
					Annotations: map[string]string{"meta-annotation": "v1"},
				},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(current, desired *v1alpha1.EphemeralRunnerSet)
		want   bool
	}{
		{
			name: "ephemeral runner image change is actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image = "ghcr.io/actions/runner:new"
			},
			want: true,
		},
		{
			name: "ephemeral runner template change is actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.NodeSelector = map[string]string{"kubernetes.io/os": "linux"}
			},
			want: true,
		},
		{
			name: "replicas change is non-actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Spec.Replicas = 3
			},
			want: false,
		},
		{
			name: "patch id change is non-actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Spec.PatchID = 11
			},
			want: false,
		},
		{
			name: "set labels change is non-actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Labels["app"] = "changed"
			},
			want: false,
		},
		{
			name: "set annotations change is non-actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Annotations["note"] = "changed"
			},
			want: false,
		},
		{
			name: "ephemeral runner metadata change is non-actionable",
			mutate: func(_ *v1alpha1.EphemeralRunnerSet, desired *v1alpha1.EphemeralRunnerSet) {
				desired.Spec.EphemeralRunnerMetadata.Annotations["meta-annotation"] = "v2"
			},
			want: false,
		},
		{
			name: "nil metadata transition is non-actionable",
			mutate: func(current, desired *v1alpha1.EphemeralRunnerSet) {
				current.Spec.EphemeralRunnerMetadata = nil
				desired.Spec.EphemeralRunnerMetadata = &v1alpha1.ResourceMeta{Labels: map[string]string{"meta-label": "new"}}
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := base()
			desired := current.DeepCopy()
			tt.mutate(current, desired)

			assert.Equal(t, tt.want, ephemeralRunnerSetActionableSpecChanged(current, desired))
		})
	}
}

func TestNextActionableRevision(t *testing.T) {
	tests := []struct {
		name    string
		current *v1alpha1.EphemeralRunnerSet
		want    int64
	}{
		{name: "nil current starts at one", current: nil, want: 1},
		{
			name:    "spec revision ahead",
			current: &v1alpha1.EphemeralRunnerSet{Spec: v1alpha1.EphemeralRunnerSetSpec{ActionableRevision: 3}, Status: v1alpha1.EphemeralRunnerSetStatus{AppliedActionableRevision: 2}},
			want:    4,
		},
		{
			name:    "applied revision ahead",
			current: &v1alpha1.EphemeralRunnerSet{Spec: v1alpha1.EphemeralRunnerSetSpec{ActionableRevision: 2}, Status: v1alpha1.EphemeralRunnerSetStatus{AppliedActionableRevision: 7}},
			want:    8,
		},
		{
			name:    "equal revisions",
			current: &v1alpha1.EphemeralRunnerSet{Spec: v1alpha1.EphemeralRunnerSetSpec{ActionableRevision: 5}, Status: v1alpha1.EphemeralRunnerSetStatus{AppliedActionableRevision: 5}},
			want:    6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextActionableRevision(tt.current))
		})
	}
}

func TestListenerPodCanonicalEqual(t *testing.T) {
	base := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "listener",
			Namespace:       "controller-ns",
			UID:             "uid-1",
			ResourceVersion: "100",
			Annotations:     map[string]string{"keep": "v"},
			Labels:          map[string]string{"app": "listener"},
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "kube-controller-manager"}},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "listener-sa",
			Containers:         []corev1.Container{{Name: "listener", Image: "ghcr.io/actions/listener:v1"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	tests := []struct {
		name   string
		mutate func(current, desired *corev1.Pod)
		want   bool
	}{
		{
			name: "ignores runtime fields",
			mutate: func(current, desired *corev1.Pod) {
				current.UID = "uid-current"
				desired.UID = "uid-desired"
				current.ResourceVersion = "101"
				desired.ResourceVersion = "202"
				current.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "a"}}
				desired.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "b"}}
				current.Status.Phase = corev1.PodPending
				desired.Status.Phase = corev1.PodFailed
			},
			want: true,
		},
		{
			name: "spec change is not equal",
			mutate: func(_ *corev1.Pod, desired *corev1.Pod) {
				desired.Spec.Containers[0].Image = "ghcr.io/actions/listener:v2"
			},
			want: false,
		},
		{
			name: "non-legacy annotation change is not equal",
			mutate: func(_ *corev1.Pod, desired *corev1.Pod) {
				desired.Annotations["keep"] = "different"
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := base.DeepCopy()
			desired := base.DeepCopy()
			tt.mutate(current, desired)
			assert.Equal(t, tt.want, listenerPodCanonicalEqual(current, desired))
		})
	}
}
