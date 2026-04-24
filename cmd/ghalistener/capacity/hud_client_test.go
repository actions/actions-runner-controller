package capacity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHUDClient(t *testing.T, handler http.HandlerFunc) (*HUDClient, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client := &HUDClient{
		token:  "test-token",
		client: &http.Client{Timeout: 5 * time.Second},
	}
	// Override the package-level URL by patching the client to hit our server.
	// Since hudAPIURL is a const, we override via a custom transport.
	origURL := srv.URL
	client.client.Transport = &rewriteTransport{base: http.DefaultTransport, target: origURL}
	return client, srv.Close
}

// rewriteTransport redirects all requests to the test server.
type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.target[len("http://"):]
	return t.base.RoundTrip(req)
}

func TestGetQueuedJobsForLabels_HappyPath(t *testing.T) {
	rows := []QueuedJobsForRunner{
		{RunnerLabel: "linux.2xlarge", NumQueuedJobs: 10},
		{RunnerLabel: "linux.4xlarge", NumQueuedJobs: 5},
		{RunnerLabel: "linux.gpu.a100", NumQueuedJobs: 3},
	}
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-token", r.Header.Get("x-hud-internal-bot"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rows)
	})
	defer cleanup()

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"linux.2xlarge"})
	require.NoError(t, err)
	assert.Equal(t, 10, total)
}

func TestGetQueuedJobsForLabels_NoMatchingLabels(t *testing.T) {
	rows := []QueuedJobsForRunner{
		{RunnerLabel: "linux.2xlarge", NumQueuedJobs: 10},
	}
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rows)
	})
	defer cleanup()

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"windows.large"})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
}

func TestGetQueuedJobsForLabels_MultipleMatchingLabels(t *testing.T) {
	rows := []QueuedJobsForRunner{
		{RunnerLabel: "linux.2xlarge", NumQueuedJobs: 10},
		{RunnerLabel: "linux.4xlarge", NumQueuedJobs: 5},
		{RunnerLabel: "linux.gpu.a100", NumQueuedJobs: 3},
	}
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rows)
	})
	defer cleanup()

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"linux.2xlarge", "linux.gpu.a100"})
	require.NoError(t, err)
	assert.Equal(t, 13, total)
}

func TestGetQueuedJobsForLabels_EmptyLabels(t *testing.T) {
	client := NewHUDClient("token")
	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
}

func TestGetQueuedJobsForLabels_ServerError(t *testing.T) {
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"linux.2xlarge"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
	assert.Equal(t, 0, total)
}

func TestGetQueuedJobsForLabels_Timeout(t *testing.T) {
	// Use a channel so the handler blocks until the test signals it to stop,
	// preventing httptest.Server.Close from waiting for the handler goroutine.
	done := make(chan struct{})
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-done
	})
	defer func() { close(done); cleanup() }()
	client.client.Timeout = 50 * time.Millisecond

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"linux.2xlarge"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HUD API request failed")
	assert.Equal(t, 0, total)
}

func TestGetQueuedJobsForLabels_MalformedJSON(t *testing.T) {
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not json"))
	})
	defer cleanup()

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"linux.2xlarge"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decoding HUD response")
	assert.Equal(t, 0, total)
}

func TestGetQueuedJobsForLabels_EmptyToken(t *testing.T) {
	rows := []QueuedJobsForRunner{
		{RunnerLabel: "linux.2xlarge", NumQueuedJobs: 7},
	}
	client, cleanup := newTestHUDClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Token header should be set even if empty.
		assert.Equal(t, "", r.Header.Get("x-hud-internal-bot"))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rows)
	})
	defer cleanup()
	client.token = ""

	total, err := client.GetQueuedJobsForLabels(context.Background(), []string{"linux.2xlarge"})
	require.NoError(t, err)
	assert.Equal(t, 7, total)
}
