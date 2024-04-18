package worker

import (
	"math"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
)

func TestSetDesiredWorkerState_MinMaxDefaults(t *testing.T) {
	logger := logr.Discard()
	newEmptyWorker := func() *Worker {
		return &Worker{
			config: Config{
				MinRunners: 0,
				MaxRunners: math.MaxInt32,
			},
			lastPatch:   -1,
			lastPatchID: -1,
			logger:      &logger,
		}
	}

	t.Run("init calculate with acquired 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("init calculate with acquired 1", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("increment patch when job done", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(0, 0)
		w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})

	t.Run("increment patch when called with same parameters", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(1, 0)
		w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})

	t.Run("calculate desired scale when acquired > 0 and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(1, 1)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("re-use the last state when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(1, 0)
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("adjust when acquired == 0 and completed == 1", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(1, 1)
		w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})
}

func TestSetDesiredWorkerState_MinSet(t *testing.T) {
	logger := logr.Discard()
	newEmptyWorker := func() *Worker {
		return &Worker{
			config: Config{
				MinRunners: 1,
				MaxRunners: math.MaxInt32,
			},
			lastPatch:   -1,
			lastPatchID: -1,
			logger:      &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("re-use the old state on count == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(2, 0)
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(2, 0)
		w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})
}

func TestSetDesiredWorkerState_MaxSet(t *testing.T) {
	logger := logr.Discard()
	newEmptyWorker := func() *Worker {
		return &Worker{
			config: Config{
				MinRunners: 0,
				MaxRunners: 5,
			},
			lastPatch:   -1,
			lastPatchID: -1,
			logger:      &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("re-use the old state on count == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(2, 0)
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 2, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(2, 0)
		w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(6, 0)
		assert.Equal(t, 5, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(5, 0)
		assert.Equal(t, 5, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("scale to max when count > max and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(1, 0)
		w.setDesiredWorkerState(6, 1)
		assert.Equal(t, 5, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})

	t.Run("scale back to 0 when count was > max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(6, 0)
		w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})
}

func TestSetDesiredWorkerState_MinMaxSet(t *testing.T) {
	logger := logr.Discard()
	newEmptyWorker := func() *Worker {
		return &Worker{
			config: Config{
				MinRunners: 1,
				MaxRunners: 3,
			},
			lastPatch:   -1,
			lastPatchID: -1,
			logger:      &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("re-use the old state on count == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(2, 0)
		w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("scale to min when count == 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(2, 0)
		w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.lastPatchID)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(4, 0)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.lastPatchID)
	})
}
