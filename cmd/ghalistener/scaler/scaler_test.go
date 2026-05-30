package scaler

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discardLogger = slog.New(slog.DiscardHandler)

func TestSetDesiredWorkerState_MinMaxDefaults(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 0,
				MaxRunners: math.MaxInt32,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("init calculate with acquired 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
		assert.Equal(t, 0, patchID)
	})

	t.Run("init calculate with acquired 1", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
		assert.Equal(t, 0, patchID)
	})

	t.Run("increment patch when job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("increment patch when called with same parameters", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("calculate desired scale when acquired > 0 and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.dirty = true
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("adjust when acquired == 0 and completed == 1", func(t *testing.T) {
		w := newEmptyWorker()
		w.dirty = true
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.False(t, w.dirty)
		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_MinSet(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 1,
				MaxRunners: math.MaxInt32,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("desired patch is 0 but sequence continues on empty batch and min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 4, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 2, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_MaxSet(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 0,
				MaxRunners: 5,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 5, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(5)
		assert.False(t, w.dirty)
		assert.Equal(t, 5, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count > max and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(6)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 5, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale back to 0 when count was > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 2, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_MinMaxSet(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 1,
				MaxRunners: 3,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to min when count == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(4)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 2, w.patchSeq)
	})
}

// mockResourceChecker implements ResourceChecker for testing.
type mockResourceChecker struct {
	adjusted int
	err      error
}

func (m *mockResourceChecker) AdjustCount(_ context.Context, count int) (int, int, error) {
	if m.err != nil {
		return 0, 0, m.err
	}
	return m.adjusted, m.adjusted, nil
}

func TestHandleDesiredRunnerCount_CheckerReturnsFalse(t *testing.T) {
	w := &Scaler{
		config:          Config{MinRunners: 0, MaxRunners: 10},
		targetRunners:   -1,
		patchSeq:        -1,
		logger:          discardLogger,
		resourceChecker: &mockResourceChecker{adjusted: 0},
	}
	result, err := w.HandleDesiredRunnerCount(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, 0, result)
}

func TestHandleDesiredRunnerCount_CheckerReturnsError(t *testing.T) {
	// fail-open: when checker returns an error, scaling must NOT be blocked.
	// The k8s patch will panic (nil clientset, no real server), but the important
	// assertion is that we did NOT return (0, nil) early — which would mean fail-closed.
	// A panic here proves execution proceeded past the resource check.
	w := &Scaler{
		config:        Config{MinRunners: 0, MaxRunners: 10},
		targetRunners: -1,
		patchSeq:      -1,
		logger:        discardLogger,
		resourceChecker: &mockResourceChecker{
			err: errors.New("api error"),
		},
	}
	// fail-open: checker error must not cause a (0, nil) early return.
	// Execution proceeds to the k8s patch which panics on nil clientset —
	// that panic is proof the early-return was NOT taken.
	assert.Panics(t, func() {
		w.HandleDesiredRunnerCount(context.Background(), 5) //nolint:errcheck
	}, "checker error should not block scale-up (fail-open): execution must reach k8s patch")
}

func TestHandleDesiredRunnerCount_NilChecker(t *testing.T) {
	// nil checker: resource check is skipped entirely, scaling proceeds normally.
	// The k8s patch panics on nil clientset — that panic proves execution
	// reached the patch (i.e., the nil-checker guard did not block scaling).
	w := &Scaler{
		config:          Config{MinRunners: 0, MaxRunners: 10},
		targetRunners:   -1,
		patchSeq:        -1,
		logger:          discardLogger,
		resourceChecker: nil,
	}
	assert.Panics(t, func() {
		w.HandleDesiredRunnerCount(context.Background(), 3) //nolint:errcheck
	}, "nil checker should not block scale-up")
}
