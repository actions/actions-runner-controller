package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestListenerRestoresMissingAnnotations(t *testing.T) {
	for _, tt := range []struct {
		name   string
		object client.Object
	}{
		{name: "service account", object: &corev1.ServiceAccount{}},
		{name: "role", object: &rbacv1.Role{}},
		{name: "role binding", object: &rbacv1.RoleBinding{}},
		{name: "config secret", object: &corev1.Secret{}},
		{name: "pod", object: &corev1.Pod{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, rbacv1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			metadata := &v1alpha1.ResourceMeta{Annotations: map[string]string{"example.com/required": "value"}}
			listener := &v1alpha1.AutoscalingListener{
				ObjectMeta: metav1.ObjectMeta{Name: "test-listener", Namespace: "test-ns"},
				Spec: v1alpha1.AutoscalingListenerSpec{
					GitHubConfigURL:               "https://github.com/org/repo",
					GitHubConfigSecret:            "auth",
					RunnerScaleSetID:              1,
					AutoscalingRunnerSetName:      "test-set",
					AutoscalingRunnerSetNamespace: "test-ns",
					EphemeralRunnerSetName:        "test-set",
					Image:                         "listener:latest",
					ServiceAccountMetadata:        metadata.DeepCopy(),
					RoleMetadata:                  metadata.DeepCopy(),
					RoleBindingMetadata:           metadata.DeepCopy(),
					ConfigSecretMetadata:          metadata.DeepCopy(),
					Template: &corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Annotations: metadata.DeepCopy().Annotations},
					},
				},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				listener,
				&v1alpha1.AutoscalingRunnerSet{
					ObjectMeta: metav1.ObjectMeta{Name: "test-set", Namespace: listener.Namespace},
					Spec: v1alpha1.AutoscalingRunnerSetSpec{
						GitHubConfigUrl: listener.Spec.GitHubConfigURL, GitHubConfigSecret: "auth",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: listener.Namespace},
					Data:       map[string][]byte{"github_token": []byte("test-token")},
				},
			).Build()
			r := &AutoscalingListenerReconciler{
				Client: c, Scheme: scheme, Log: logr.Discard(), ListenerMetricsAddr: "0",
				ResourceBuilder: ResourceBuilder{
					Scheme: scheme, ResourceCache: newTestResourceCache(),
					SecretResolver: secretresolver.New(c, scalefake.NewMultiClient()),
				},
			}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(listener)}
			for range 6 {
				_, err := r.Reconcile(t.Context(), req)
				require.NoError(t, err)
			}
			key := req.NamespacedName
			if _, ok := tt.object.(*corev1.Secret); ok {
				key.Name = scaleSetListenerConfigName(listener)
			}
			require.NoError(t, c.Get(t.Context(), key, tt.object))
			require.Equal(t, "value", tt.object.GetAnnotations()["example.com/required"])
			tt.object.SetAnnotations(nil)
			require.NoError(t, c.Update(t.Context(), tt.object))

			// Removing the pod's config-version annotation requires replacement;
			// the other resources restore their annotations with a patch.
			for range 6 {
				require.NotPanics(t, func() {
					_, err := r.Reconcile(t.Context(), req)
					require.NoError(t, err)
				})
			}
			require.NoError(t, c.Get(t.Context(), key, tt.object))
			assert.Equal(t, "value", tt.object.GetAnnotations()["example.com/required"])
		})
	}
}
