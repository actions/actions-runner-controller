package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorker_Add(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := newWorker(ctx, 2, func(st *ScaleTarget) {})
	require.True(t, w.Add(&ScaleTarget{}))
	require.True(t, w.Add(&ScaleTarget{}))
	require.False(t, w.Add(&ScaleTarget{}))
}

func TestWorker_Work(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var count int

	w := newWorker(ctx, 1, func(st *ScaleTarget) {
		count++
		cancel()
	})
	require.True(t, w.Add(&ScaleTarget{}))
	require.False(t, w.Add(&ScaleTarget{}))

	<-w.Done()

	require.Equal(t, count, 1)
}
