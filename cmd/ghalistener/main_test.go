package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func withFastBackoff(t *testing.T) {
	t.Helper()
	oldMin, oldMax := superviseMinBackoff, superviseMaxBackoff
	superviseMinBackoff = time.Millisecond
	superviseMaxBackoff = 2 * time.Millisecond
	t.Cleanup(func() {
		superviseMinBackoff, superviseMaxBackoff = oldMin, oldMax
	})
}

// supervise must restart runOnce on error and stop cleanly (nil) when ctx is
// cancelled.
func TestSuperviseRestartsUntilCancel(t *testing.T) {
	withFastBackoff(t)

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan error, 1)

	go func() {
		done <- supervise(ctx, slog.New(slog.DiscardHandler), func() error {
			if calls.Add(1) >= 5 {
				cancel()
			}
			return errors.New("boom")
		})
	}()

	select {
	case err := <-done:
		assert.NoError(t, err, "supervise must return nil on ctx cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("supervise did not return after ctx cancel")
	}
	assert.GreaterOrEqual(t, calls.Load(), int32(5), "runOnce should have been retried")
}

// A failing scale set supervised inside an errgroup must not cancel a sibling
// scale set that is running happily. This is the failure-isolation guarantee.
func TestSuperviseFailureIsolation(t *testing.T) {
	withFastBackoff(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var badCalls atomic.Int32
	var goodRunning atomic.Bool
	var goodStopped atomic.Bool

	g, gctx := errgroup.WithContext(ctx)

	// Bad scale set: fails on every attempt.
	g.Go(func() error {
		return supervise(gctx, slog.New(slog.DiscardHandler), func() error {
			badCalls.Add(1)
			return errors.New("always fails")
		})
	})

	// Good scale set: blocks until its own ctx is cancelled, recording whether it
	// was ever forced to stop.
	var wg sync.WaitGroup
	wg.Add(1)
	g.Go(func() error {
		return supervise(gctx, slog.New(slog.DiscardHandler), func() error {
			goodRunning.Store(true)
			wg.Done()
			<-gctx.Done()
			goodStopped.Store(true)
			return gctx.Err()
		})
	})

	// Wait for the good session to be up, then let the bad one thrash a while.
	wg.Wait()
	require.Eventually(t, func() bool { return badCalls.Load() >= 3 }, time.Second, time.Millisecond)

	assert.True(t, goodRunning.Load(), "good scale set should be running")
	assert.False(t, goodStopped.Load(), "good scale set must not be stopped by the bad one's failures")

	// Shutdown ends both cleanly.
	cancel()
	assert.NoError(t, g.Wait())
	assert.True(t, goodStopped.Load(), "good scale set should stop on shutdown")
}
