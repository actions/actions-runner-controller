package metrics

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type shutdownLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func TestExporterUnexpectedCloseStillReturnsAnError(t *testing.T) {
	e := NewExporter(ExporterConfig{ServerAddr: "127.0.0.1:0", Logger: slog.New(slog.DiscardHandler)}).(*exporter)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, e.srv.Close())
	require.ErrorIs(t, e.ListenAndServe(ctx), http.ErrServerClosed,
		"only context-driven shutdown may suppress the server-closed error")
}

func (b *shutdownLogBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *shutdownLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestExporterIntentionalShutdownPreservesCause(t *testing.T) {
	for _, cancelBeforeBind := range []bool{true, false} {
		name := "after bind"
		if cancelBeforeBind {
			name = "before bind"
		}
		t.Run(name, func(t *testing.T) {
			var logs shutdownLogBuffer
			// NewExporter creates a fresh Prometheus registry for every instance.
			e := NewExporter(ExporterConfig{
				ServerAddr: "127.0.0.1:0",
				Logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
			}).(*exporter)
			bound := make(chan struct{})
			e.srv.BaseContext = func(net.Listener) context.Context {
				close(bound)
				return context.Background()
			}
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("broker poll exhausted")
			t.Cleanup(func() {
				cancel(cause)
				require.NoError(t, e.srv.Close())
			})
			if cancelBeforeBind {
				cancel(cause)
			}
			result := make(chan error, 1)
			go func() { result <- e.ListenAndServe(ctx) }()
			if !cancelBeforeBind {
				select {
				case <-bound:
					cancel(cause)
				case <-time.After(2 * time.Second):
					t.Fatal("metrics server did not bind its loopback listener")
				}
			}
			select {
			case err := <-result:
				assert.NoError(t, err, "intentional metrics shutdown must not compete with the listener error")
			case <-time.After(2 * time.Second):
				t.Fatal("metrics server did not return after cancellation")
			}
			assert.Contains(t, logs.String(), cause.Error(), "shutdown must preserve the underlying cancellation cause")
		})
	}
}
