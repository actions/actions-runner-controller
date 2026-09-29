package secretresolver

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeVault struct{ err error }

func (v fakeVault) GetSecret(context.Context, string) (string, error) { return "", v.err }

func TestWrapK8sNotFound(t *testing.T) {
	notFound := kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "github-config")
	if err := wrapK8sNotFound(notFound); !errors.Is(err, ErrNotFound) || !kerrors.IsNotFound(err) {
		t.Fatalf("NotFound lost its identity: %v", err)
	}
	forbidden := kerrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "github-config", errors.New("denied"))
	if err := wrapK8sNotFound(forbidden); errors.Is(err, ErrNotFound) {
		t.Fatalf("Forbidden reported as not found: %v", err)
	}
}

func TestVaultResolverTagsOnlyA404AsNotFound(t *testing.T) {
	tests := map[string]struct {
		err      error
		notFound bool
	}{
		"404":           {&azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "SecretNotFound"}, true},
		"404 wrapped":   {errors.Join(errors.New("failed to get secret"), &azcore.ResponseError{StatusCode: http.StatusNotFound}), true},
		"403":           {&azcore.ResponseError{StatusCode: http.StatusForbidden}, false},
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
