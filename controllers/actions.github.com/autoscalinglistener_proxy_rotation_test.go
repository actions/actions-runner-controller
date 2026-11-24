package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Secrets are not watched, so the reconcile that patches the rotated
// credentials into the mirror secret is the only one guaranteed to run. It must
// therefore build the listener pod against the patch response, not the secret
// it read before patching, or the pod is only replaced by whichever unrelated
// event comes next.
func TestRotatedProxyCredentialsRecreateTheListenerPodInOneReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	const namespace = "test-ns"
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{Name: "test-listener", Namespace: namespace},
		Spec: v1alpha1.AutoscalingListenerSpec{
			GitHubConfigURL:               "https://github.com/org/repo",
			GitHubConfigSecret:            "auth",
			RunnerScaleSetID:              1,
			AutoscalingRunnerSetName:      "test-set",
			AutoscalingRunnerSetNamespace: namespace,
			EphemeralRunnerSetName:        "test-set",
			Image:                         "listener:latest",
			Proxy: &v1alpha1.ProxyConfig{
				HTTP: &v1alpha1.ProxyServerConfig{
					Url:                 "http://proxy.example.com:8080",
					CredentialSecretRef: "proxy-credentials",
				},
			},
		},
	}
	proxyCredentials := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "proxy-credentials", Namespace: namespace},
		Data: map[string][]byte{
			"username": []byte("user"),
			"password": []byte("password"),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		listener,
		proxyCredentials,
		&v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-set", Namespace: namespace},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl: listener.Spec.GitHubConfigURL, GitHubConfigSecret: "auth",
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: namespace},
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
	podKey := req.NamespacedName
	mirrorKey := types.NamespacedName{Name: proxyListenerSecretName(listener), Namespace: namespace}

	pod := new(corev1.Pod)
	for range 6 {
		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)
		if err := c.Get(t.Context(), podKey, pod); err == nil {
			break
		}
	}
	require.NoError(t, c.Get(t.Context(), podKey, pod), "listener pod should exist before the rotation")
	mirror := new(corev1.Secret)
	require.NoError(t, c.Get(t.Context(), mirrorKey, mirror))
	require.Equal(t, mirror.ResourceVersion, pod.Annotations[AnnotationKeyListenerProxySecretResourceVersion])

	require.NoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(proxyCredentials), proxyCredentials))
	proxyCredentials.Data["password"] = []byte("rotated")
	require.NoError(t, c.Update(t.Context(), proxyCredentials))

	_, err := r.Reconcile(t.Context(), req)
	require.NoError(t, err)

	require.NoError(t, c.Get(t.Context(), mirrorKey, mirror))
	require.Contains(t, string(mirror.Data["http_proxy"]), "rotated", "the mirror secret should carry the rotated password")
	err = c.Get(t.Context(), podKey, pod)
	require.True(t, kerrors.IsNotFound(err), "the reconcile that patched the mirror secret must also delete the listener pod, got %v", err)
}
