package secretresolver

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/vault"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type fakeVault struct{ err error }

func (v fakeVault) GetSecret(context.Context, string) (string, error) { return "", v.err }

func TestWrapKubernetesError(t *testing.T) {
	notFound := kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "github-config")
	if err := wrapKubernetesError(notFound); !errors.Is(err, ErrNotFound) || !kerrors.IsNotFound(err) {
		t.Fatalf("NotFound lost its identity: %v", err)
	}
	forbidden := kerrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "github-config", errors.New("denied"))
	if err := wrapKubernetesError(forbidden); errors.Is(err, ErrNotFound) {
		t.Fatalf("Forbidden reported as not found: %v", err)
	}
}

func TestVaultResolverTagsOnlySecretNotFound(t *testing.T) {
	tests := map[string]struct {
		err      error
		notFound bool
	}{
		"404":           {&azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "SecretNotFound"}, true},
		"404 wrapped":   {errors.Join(errors.New("failed to get secret"), &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "SecretNotFound"}), true},
		"404 other":     {&azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "VaultNotFound"}, false},
		"403":           {&azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "Forbidden"}, false},
		"network error": {errors.New("dial tcp: i/o timeout"), false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := &vaultResolver{vault: fakeVault{err: tt.err}}
			_, appErr := r.appConfig(context.Background(), "github-config")
			_, proxyErr := r.proxyCredentials(context.Background(), "proxy")
			for _, err := range []error{appErr, proxyErr} {
				if err == nil {
					t.Fatal("expected an error")
				}
				if got := errors.Is(err, ErrNotFound); got != tt.notFound {
					t.Fatalf("errors.Is(err, ErrNotFound) = %v, want %v: %v", got, tt.notFound, err)
				}
			}
		})
	}
}

func TestGetActionsServiceTagsEveryMissingDependency(t *testing.T) {
	const ns = "arc-runners"
	configSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-config", Namespace: ns},
		Data:       map[string][]byte{"github_token": []byte("token")},
	}
	proxyWithCredentials := &v1alpha1.ProxyConfig{
		HTTP: &v1alpha1.ProxyServerConfig{Url: "http://proxy.example.com:3128", CredentialSecretRef: "proxy-credentials"},
	}
	forbidden := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return kerrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, key.Name, errors.New("denied"))
		},
	}

	tests := map[string]struct {
		spec        v1alpha1.AutoscalingRunnerSetSpec
		objects     []client.Object
		interceptor *interceptor.Funcs
		notFound    bool
	}{
		"github config secret missing": {
			spec:     v1alpha1.AutoscalingRunnerSetSpec{GitHubConfigSecret: "github-config"},
			notFound: true,
		},
		"proxy credential secret missing": {
			spec:     v1alpha1.AutoscalingRunnerSetSpec{GitHubConfigSecret: "github-config", Proxy: proxyWithCredentials},
			objects:  []client.Object{configSecret},
			notFound: true,
		},
		"tls config map missing": {
			spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigSecret: "github-config",
				GitHubServerTLS: &v1alpha1.TLSConfig{CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "ca"}, Key: "ca.crt"},
				}},
			},
			objects:  []client.Object{configSecret},
			notFound: true,
		},
		"vault proxy credential secret missing": {
			spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigSecret: "github-config",
				VaultConfig:        &v1alpha1.VaultConfig{Type: vault.VaultTypeAzureKeyVault, Proxy: proxyWithCredentials},
			},
			notFound: true,
		},
		"forbidden is not missing": {
			spec:        v1alpha1.AutoscalingRunnerSetSpec{GitHubConfigSecret: "github-config"},
			interceptor: &forbidden,
			notFound:    false,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(tt.objects...)
			if tt.interceptor != nil {
				builder = builder.WithInterceptorFuncs(*tt.interceptor)
			}
			ars := &v1alpha1.AutoscalingRunnerSet{ObjectMeta: metav1.ObjectMeta{Name: "ars", Namespace: ns}, Spec: tt.spec}

			_, err := New(builder.Build(), nil).GetActionsService(context.Background(), ars)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrNotFound); got != tt.notFound {
				t.Fatalf("errors.Is(err, ErrNotFound) = %v, want %v: %v", got, tt.notFound, err)
			}
		})
	}
}
