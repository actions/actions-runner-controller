package actionsgithubcom

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestOptionsResolve(t *testing.T) {
	t.Parallel()

	t.Run("the shipped defaults are used as-is", func(t *testing.T) {
		got := OptionsWithDefault().Resolve()
		assert.Equal(t, 2, got.AutoscalingRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 2, got.AutoscalingListenerMaxConcurrentReconciles)
		assert.Equal(t, 2, got.EphemeralRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 4, got.EphemeralRunnerMaxConcurrentReconciles)
	})

	t.Run("the shipped defaults survive a second resolve", func(t *testing.T) {
		once := OptionsWithDefault().Resolve()
		assert.Equal(t, once, once.Resolve())
	})

	t.Run("explicit values win over the defaults", func(t *testing.T) {
		opts := Options{
			AutoscalingRunnerSetMaxConcurrentReconciles: 3,
			AutoscalingListenerMaxConcurrentReconciles:  5,
			EphemeralRunnerSetMaxConcurrentReconciles:   6,
			EphemeralRunnerMaxConcurrentReconciles:      20,
		}
		got := opts.Resolve()
		assert.Equal(t, 3, got.AutoscalingRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 5, got.AutoscalingListenerMaxConcurrentReconciles)
		assert.Equal(t, 6, got.EphemeralRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 20, got.EphemeralRunnerMaxConcurrentReconciles)
	})

	t.Run("a value of zero or less is replaced by that controller's default", func(t *testing.T) {
		for _, n := range []int{0, -1} {
			opts := Options{
				AutoscalingRunnerSetMaxConcurrentReconciles: n,
				AutoscalingListenerMaxConcurrentReconciles:  n,
				EphemeralRunnerSetMaxConcurrentReconciles:   n,
				EphemeralRunnerMaxConcurrentReconciles:      n,
			}
			got := opts.Resolve()
			assert.Equal(t, OptionsWithDefault(), got, "a non-positive value must never reach controller-runtime")
		}
	})

	t.Run("a single non-positive value does not disturb the others", func(t *testing.T) {
		opts := OptionsWithDefault()
		opts.AutoscalingListenerMaxConcurrentReconciles = 7
		opts.EphemeralRunnerMaxConcurrentReconciles = 0
		got := opts.Resolve()
		assert.Equal(t, 7, got.AutoscalingListenerMaxConcurrentReconciles)
		assert.Equal(t, 4, got.EphemeralRunnerMaxConcurrentReconciles)
		assert.Equal(t, 2, got.AutoscalingRunnerSetMaxConcurrentReconciles)
		assert.Equal(t, 2, got.EphemeralRunnerSetMaxConcurrentReconciles)
	})
}

func TestControllerErrorBackoffIsCappedAtOneMinute(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []Option
		initial time.Duration
	}{
		{name: "default", initial: 5 * time.Millisecond},
		{
			name: "typed rate limiter", initial: time.Millisecond,
			options: []Option{WithTypedRateLimiter(workqueue.DefaultTypedItemBasedRateLimiter[reconcile.Request]())},
		},
		{
			name: "non-priority queue", initial: 5 * time.Millisecond,
			options: []Option{func(opts *controller.Options) {
				disabled := false
				opts.UsePriorityQueue = &disabled
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := newControllerOptions(tc.options)
			require.NotNil(t, opts.RateLimiter)
			var request reconcile.Request
			for failure := range 32 {
				want := min(tc.initial*time.Duration(1<<failure), time.Minute)
				require.Equal(t, want, opts.RateLimiter.When(request), "failure %d", failure+1)
			}
			require.Equal(t, 32, opts.RateLimiter.NumRequeues(request))
			opts.RateLimiter.Forget(request)
			require.Zero(t, opts.RateLimiter.NumRequeues(request))
			require.Equal(t, tc.initial, opts.RateLimiter.When(request))
		})
	}
}
