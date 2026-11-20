package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/stretchr/testify/require"
)

// Run only inside the disposable network-isolated test container. The actual
// scaler constructor requires the fixed in-cluster service-account paths;
// neither those host paths nor production credentials are used by this test.
func TestRunReturnsAfterBrokerFailureWhenCleanupAuthenticationStalls(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("ARC_CONTAINER_REPRO") != "1" {
		t.Skip("requires the disposable ARC reproduction container")
	}

	const serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
	require.NoError(t, os.MkdirAll(serviceAccountDir, 0700))
	require.NoError(t, os.WriteFile(serviceAccountDir+"/token", []byte("local-test-token"), 0600))
	kube := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || !strings.HasSuffix(r.URL.Path, "/ephemeralrunnersets/default") {
			http.Error(w, "unexpected Kubernetes request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"actions.github.com/v1alpha1","kind":"EphemeralRunnerSet","metadata":{"name":"default","namespace":"runner-ns"},"spec":{"replicas":15,"patchID":0}}`))
	}))
	defer kube.Close()
	require.NoError(t, os.WriteFile(serviceAccountDir+"/ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: kube.Certificate().Raw}), 0600))
	kubeURL, err := url.Parse(kube.URL)
	require.NoError(t, err)
	t.Setenv("KUBERNETES_SERVICE_HOST", kubeURL.Hostname())
	t.Setenv("KUBERNETES_SERVICE_PORT", kubeURL.Port())
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, "")
	}

	// The initial admin JWT expires from the SDK's cache while the broker poll
	// is blocked, reproducing the authentication refresh seen during teardown.
	claims, err := json.Marshal(map[string]int64{"exp": time.Now().Add(63 * time.Second).Unix()})
	require.NoError(t, err)
	jwt := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".c2lnbmF0dXJl"
	var registrationRequests atomic.Int32
	var brokerReached atomic.Bool
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	var refreshOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefresh) }) }
	defer release()
	var actions *httptest.Server
	actions = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/runners/registration-token"):
			if registrationRequests.Add(1) > 1 {
				refreshOnce.Do(func() { close(refreshStarted) })
				select {
				case <-releaseRefresh:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"local-registration-token"}`))
		case strings.HasSuffix(r.URL.Path, "/actions/runner-registration"):
			_ = json.NewEncoder(w).Encode(map[string]string{"url": actions.URL + "/tenant/", "token": jwt})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sessions"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessionId": "8eea36ec-41bc-4d07-8a58-c522674dc9c0", "messageQueueUrl": actions.URL + "/queue",
				"messageQueueAccessToken": "local-queue-token", "statistics": map[string]int{"totalAssignedJobs": 10},
			})
		case r.URL.Path == "/queue":
			brokerReached.Store(true)
			<-r.Context().Done()
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/sessions/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected Actions request", http.StatusNotFound)
		}
	}))
	defer func() {
		release()
		actions.Close()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, &config.Config{
			ConfigureURL: actions.URL + "/test-org", AppConfig: &appconfig.AppConfig{Token: "local-token"},
			EphemeralRunnerSetNamespace: "runner-ns", EphemeralRunnerSetName: "default",
			MinRunners: 5, MaxRunners: 200, RunnerScaleSetID: 1,
			MetricsAddr: "127.0.0.1:0", MetricsEndpoint: "/metrics", LogLevel: "debug", LogFormat: "text",
		})
	}()
	select {
	case <-refreshStarted:
	case err := <-done:
		t.Fatalf("listener stopped before the cleanup refresh: %v", err)
	case <-time.After(8 * time.Second):
		t.Fatal("listener did not reach cleanup authentication")
	}
	require.True(t, brokerReached.Load(), "the actual listener must reach the stalled broker poll")
	select {
	case err := <-done:
		require.True(t, errors.Is(err, context.DeadlineExceeded), "preserve the original broker failure, got %v", err)
	case <-time.After(12 * time.Second):
		release()
		require.Error(t, <-done, "the released listener must retain its polling failure")
		t.Fatal("broker polling stopped, but unbounded cleanup prevented listener exit and replacement")
	}
}
