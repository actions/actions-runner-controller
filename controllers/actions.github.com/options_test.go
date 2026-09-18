package actionsgithubcom

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptionsResolve(t *testing.T) {
	t.Parallel()

	t.Run("unset per-controller values fall back to the default", func(t *testing.T) {
		got := OptionsWithDefault().Resolve()
		assert.Equal(t, 2, got.DefaultMaxConcurrentReconciles)
		assert.Equal(t, 2, got.AutoscalingRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 2, got.AutoscalingListenerMaxConcurrentReconciles)
		assert.Equal(t, 2, got.EphemeralRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 4, got.EphemeralRunnerMaxConcurrentReconciles)
	})

	t.Run("the shipped defaults survive a second resolve", func(t *testing.T) {
		once := OptionsWithDefault().Resolve()
		assert.Equal(t, once, once.Resolve())
	})

	t.Run("a default of zero or less is replaced by 1", func(t *testing.T) {
		for _, n := range []int{0, -1} {
			opts := Options{DefaultMaxConcurrentReconciles: n}
			got := opts.Resolve()
			assert.Equal(t, 1, got.DefaultMaxConcurrentReconciles)
			assert.Equal(t, 1, got.AutoscalingRunnerSetMaxConcurrentReconciles)
			assert.Equal(t, 1, got.AutoscalingListenerMaxConcurrentReconciles)
			assert.Equal(t, 1, got.EphemeralRunnerSetMaxConcurrentReconciles)
			assert.Equal(t, 1, got.EphemeralRunnerMaxConcurrentReconciles)
		}
	})

	t.Run("a changed default applies to every unset controller", func(t *testing.T) {
		opts := OptionsWithDefault()
		opts.DefaultMaxConcurrentReconciles = 4
		opts.EphemeralRunnerMaxConcurrentReconciles = 20
		got := opts.Resolve()
		assert.Equal(t, 4, got.AutoscalingRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 4, got.AutoscalingListenerMaxConcurrentReconciles)
		assert.Equal(t, 4, got.EphemeralRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 20, got.EphemeralRunnerMaxConcurrentReconciles)
	})

	t.Run("an explicit zero opts a controller back into the default", func(t *testing.T) {
		opts := OptionsWithDefault()
		opts.EphemeralRunnerMaxConcurrentReconciles = 0
		got := opts.Resolve()
		assert.Equal(t, got.DefaultMaxConcurrentReconciles, got.EphemeralRunnerMaxConcurrentReconciles)
	})
}
