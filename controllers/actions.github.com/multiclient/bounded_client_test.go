package multiclient

import (
	"context"
	"testing"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/require"
)

func TestBoundedClientReusesIdleClients(t *testing.T) {
	base := new(scaleset.Client)
	info := scaleset.SystemInfo{ScaleSetID: 42}
	base.SetSystemInfo(info)
	created := 0
	c := &boundedClient{
		Client: base,
		newClient: func() (*scaleset.Client, error) {
			created++
			return new(scaleset.Client), nil
		},
		idle: []*scaleset.Client{base},
	}
	first, err := c.acquire(t.Context())
	require.NoError(t, err)
	second, err := c.acquire(t.Context())
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, 1, created)
	require.Equal(t, info, second.SystemInfo())
	c.release(first)
	c.release(second)

	info.ScaleSetID++
	c.SetSystemInfo(info)
	for range 5 {
		service, err := c.acquire(t.Context())
		require.NoError(t, err)
		require.Equal(t, info, service.SystemInfo())
		c.release(service)
	}
	require.Equal(t, 1, created, "idle clients must retain their authentication cache")
	require.Len(t, c.idle, 2)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	service, err := c.acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, service)
	require.Len(t, c.idle, 2)
}
