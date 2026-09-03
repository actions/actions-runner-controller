package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type otlpCapture struct {
	mu    sync.Mutex
	paths []string
}

func (c *otlpCapture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	})
}

func (c *otlpCapture) Paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.paths))
	copy(out, c.paths)
	return out
}

func exportOneSpan(t *testing.T, ctx context.Context, endpoint string) {
	t.Helper()
	exp, err := newOTLPTraceExporter(ctx, endpoint)
	require.NoError(t, err)
	stubs := tracetest.SpanStubs{{Name: "test"}}
	require.NoError(t, exp.ExportSpans(ctx, stubs.Snapshots()))
	require.NoError(t, exp.Shutdown(ctx))
}

// The listener config carries a full base URL (the OTLP spec form of
// OTEL_EXPORTER_OTLP_ENDPOINT, e.g. http://collector:4318). The
// exporter must parse it and append the /v1/traces signal path — not
// treat it as a bare host:port.
func TestNewOTLPTraceExporter_ConfigEndpointFullURL(t *testing.T) {
	capture := &otlpCapture{}
	srv := httptest.NewServer(capture.handler())
	defer srv.Close()

	exportOneSpan(t, context.Background(), srv.URL)

	require.NotEmpty(t, capture.Paths(), "collector received no export")
	assert.Equal(t, "/v1/traces", capture.Paths()[0])
}

func TestNewOTLPTraceExporter_ConfigEndpointExplicitPath(t *testing.T) {
	capture := &otlpCapture{}
	srv := httptest.NewServer(capture.handler())
	defer srv.Close()

	exportOneSpan(t, context.Background(), srv.URL+"/custom/v1/traces")

	require.NotEmpty(t, capture.Paths(), "collector received no export")
	assert.Equal(t, "/custom/v1/traces", capture.Paths()[0])
}

// With no config endpoint, the SDK's spec-compliant OTEL_EXPORTER_OTLP_*
// env handling applies (endpoint, headers, TLS, ...).
func TestNewOTLPTraceExporter_StandardEnvVars(t *testing.T) {
	capture := &otlpCapture{}
	srv := httptest.NewServer(capture.handler())
	defer srv.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)

	exportOneSpan(t, context.Background(), "")

	require.NotEmpty(t, capture.Paths(), "collector received no export")
	assert.Equal(t, "/v1/traces", capture.Paths()[0])
}

func TestNewOTLPTraceExporter_RejectsInvalidEndpoint(t *testing.T) {
	_, err := newOTLPTraceExporter(context.Background(), "not a url")
	assert.Error(t, err)
}
