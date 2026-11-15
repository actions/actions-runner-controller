package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsGracefulShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		signaled bool
		err      error
		want     bool
	}{
		{
			name:     "listener stopped by the signal",
			signaled: true,
			err:      context.Canceled,
			want:     true,
		},
		{
			name:     "in-flight request aborted by the signal",
			signaled: true,
			err:      fmt.Errorf("failed to get message: %w", context.Canceled),
			want:     true,
		},
		{
			name:     "metrics server shut down by the signal",
			signaled: true,
			err:      http.ErrServerClosed,
			want:     true,
		},
		{
			name:     "unrelated failure while shutting down",
			signaled: true,
			err:      errors.New("failed to scale"),
			want:     false,
		},
		{
			name:     "listener canceled without a signal",
			signaled: false,
			err:      context.Canceled,
			want:     false,
		},
		{
			name:     "metrics server closed without a signal",
			signaled: false,
			err:      http.ErrServerClosed,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.signaled {
				cancel()
			}

			assert.Equal(t, tt.want, isGracefulShutdown(ctx, tt.err))
		})
	}
}
