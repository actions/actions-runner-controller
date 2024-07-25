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
			lastPatch: -1,
			patchSeq:  -1,
			logger:    &logger,
		}
	}

	t.Run("init calculate with acquired 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
		assert.Equal(t, 0, patchID)
	})

	t.Run("init calculate with acquired 1", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
		assert.Equal(t, 0, patchID)
	})

	t.Run("increment patch when job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("increment patch when called with same parameters", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("calculate desired scale when acquired > 0 and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 1)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("re-use the last state when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("adjust when acquired == 0 and completed == 1", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 1)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
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
			lastPatch: -1,
			patchSeq:  -1,
			logger:    &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("re-use the old state on count == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("desired patch is 0 but sequence continues on empty batch and min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 4, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)

		patchID = w.setDesiredWorkerState(0, 3)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 2, w.patchSeq)
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
			lastPatch: -1,
			patchSeq:  -1,
			logger:    &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("re-use the old state on count == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 2, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 5, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(5, 0)
		assert.Equal(t, 5, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count > max and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(6, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 5, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale back to 0 when count was > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)

		patchID = w.setDesiredWorkerState(0, 3)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 0, w.lastPatch)
		assert.Equal(t, 2, w.patchSeq)
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
			lastPatch: -1,
			patchSeq:  -1,
			logger:    &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("re-use the old state on count == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale to min when count == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(4, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)

		patchID = w.setDesiredWorkerState(0, 3)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 2, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_ScaleUpFactorSet(t *testing.T) {
	logger := logr.Discard()
	newEmptyWorker := func() *Worker {
		return &Worker{
			config: Config{
				MinRunners:    1,
				MaxRunners:    10,
				ScaleUpFactor: "1.5",
			},
			lastPatch: -1,
			patchSeq:  -1,
			logger:    &logger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("re-use the old state on count == 0 and completed == 1", func(t *testing.T) {
		// Scales up 1.5 times 1+ceil(2*1.5) = 4
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 4, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale to min when count == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2, 0)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(0, 1)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 10, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 6, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3, 0)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 6, w.lastPatch)
		assert.Equal(t, 0, w.patchSeq)

		patchID = w.setDesiredWorkerState(0, 3)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0, 0)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.lastPatch)
		assert.Equal(t, 2, w.patchSeq)
	})

}
