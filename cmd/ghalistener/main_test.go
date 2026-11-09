package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
)

func TestExitCode(t *testing.T) {
	tests := map[string]struct {
		err  error
		want int
	}{
		"session creation not found": {
			err:  sessionClientError(fmt.Errorf("failed to create message session: %w", scaleset.NotFoundError)),
			want: config.ExitCodeRunnerScaleSetNotFound,
		},
		"session creation failure other than not found": {
			err:  sessionClientError(fmt.Errorf("failed to create message session: %w", scaleset.UnauthorizedError)),
			want: 1,
		},
		"not found outside session creation": {
			err:  fmt.Errorf("handling initial message failed: %w", scaleset.NotFoundError),
			want: 1,
		},
		"generic error": {
			err:  errors.New("boom"),
			want: 1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, exitCode(tt.err))
		})
	}
}
