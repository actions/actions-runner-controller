package multiclient

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/ratelimit"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetClientForReportsResponses(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(time.Hour).Unix()))
	token := "eyJhbGciOiJSUzI1NiJ9." + claims + ".c2lnbmF0dXJl"

	server := httptest.NewTLSServer(nil)
	t.Cleanup(server.Close)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runners/registration-token"):
			w.Header().Set("X-RateLimit-Remaining", "4990")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"registration-token"}`))
		case strings.HasSuffix(r.URL.Path, "/actions/runner-registration"):
			require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
				"url": server.URL + "/tenant/123/", "token": token,
			}))
		default:
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})

	type observed struct {
		organization string
		namespace    string
		resp         ratelimit.Response
	}
	var (
		mu  sync.Mutex
		got []observed
	)
	m := NewScaleset(WithResponseObserver(func(config *actions.GitHubConfig, namespace string, resp ratelimit.Response) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, observed{config.Organization, namespace, resp})
	}))

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client, err := m.GetClientFor(t.Context(), &ClientForOptions{
		GithubConfigURL: server.URL + "/org",
		Namespace:       "arc-runners",
		AppConfig:       appconfig.AppConfig{Token: "token"},
		RootCAs:         pool,
	})
	require.NoError(t, err)

	ctx := scaleset.WithRequestRetry(t.Context(), func(c *scaleset.RetryConfig) { c.Max = 0 })
	_, err = client.GetRunnerScaleSetByID(ctx, 1)
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []observed{
		{"org", "arc-runners", ratelimit.Response{
			Backend:    ratelimit.BackendREST,
			Operation:  ratelimit.OperationRegistrationToken,
			StatusCode: http.StatusCreated,
		}},
		{"org", "arc-runners", ratelimit.Response{Backend: ratelimit.BackendREST, Operation: ratelimit.OperationRunnerRegistration, StatusCode: http.StatusOK}},
		{"org", "arc-runners", ratelimit.Response{
			Backend:     ratelimit.BackendActions,
			Operation:   ratelimit.OperationScaleSet,
			StatusCode:  http.StatusTooManyRequests,
			RateLimited: ratelimit.Secondary,
		}},
	}, got)
}
