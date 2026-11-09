package multiclient

import (
	"context"
	"sync"
	"time"

	"github.com/actions/scaleset"
)

const requestTimeout = 10 * time.Second

// The SDK's authentication mutex cannot be canceled. Concurrent calls lease
// separate clients, reusing idle clients and their tokens with a shared transport.
type boundedClient struct {
	*scaleset.Client
	newClient func() (*scaleset.Client, error)
	mu        sync.Mutex
	idle      []*scaleset.Client
}

var _ Client = (*boundedClient)(nil)

func (c *boundedClient) acquire(ctx context.Context) (*scaleset.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	var service *scaleset.Client
	if n := len(c.idle); n > 0 {
		service = c.idle[n-1]
		c.idle[n-1] = nil
		c.idle = c.idle[:n-1]
	}
	c.mu.Unlock()
	if service == nil {
		var err error
		service, err = c.newClient()
		if err != nil {
			return nil, err
		}
	}
	service.SetSystemInfo(c.Client.SystemInfo())
	return service, nil
}

func (c *boundedClient) release(service *scaleset.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.idle = append(c.idle, service)
}

func requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	ctx = scaleset.WithRequestRetry(ctx, func(retry *scaleset.RetryConfig) {
		retry.Max = 0
		retry.MaxElapsed = requestTimeout
	})
	return ctx, cancel
}

func (c *boundedClient) MessageSessionClient(ctx context.Context, runnerScaleSetID int, owner string, options ...scaleset.HTTPOption) (*scaleset.MessageSessionClient, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	session, err := service.MessageSessionClient(ctx, runnerScaleSetID, owner, options...)
	if err != nil {
		c.release(service)
	}
	// A live session retains its client and may refresh authentication later.
	return session, err
}

func (c *boundedClient) GenerateJitRunnerConfig(ctx context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.GenerateJitRunnerConfig(ctx, setting, scaleSetID)
}

func (c *boundedClient) GetRunner(ctx context.Context, runnerID int) (*scaleset.RunnerReference, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.GetRunner(ctx, runnerID)
}

func (c *boundedClient) GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.GetRunnerByName(ctx, runnerName)
}

func (c *boundedClient) RemoveRunner(ctx context.Context, runnerID int64) error {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer c.release(service)
	return service.RemoveRunner(ctx, runnerID)
}

func (c *boundedClient) GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*scaleset.RunnerGroup, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.GetRunnerGroupByName(ctx, runnerGroup)
}

func (c *boundedClient) GetRunnerScaleSet(ctx context.Context, runnerGroupID int, runnerScaleSetName string) (*scaleset.RunnerScaleSet, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.GetRunnerScaleSet(ctx, runnerGroupID, runnerScaleSetName)
}

func (c *boundedClient) GetRunnerScaleSetByID(ctx context.Context, runnerScaleSetID int) (*scaleset.RunnerScaleSet, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.GetRunnerScaleSetByID(ctx, runnerScaleSetID)
}

func (c *boundedClient) CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.CreateRunnerScaleSet(ctx, runnerScaleSet)
}

func (c *boundedClient) UpdateRunnerScaleSet(ctx context.Context, runnerScaleSetID int, runnerScaleSet *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.release(service)
	return service.UpdateRunnerScaleSet(ctx, runnerScaleSetID, runnerScaleSet)
}

func (c *boundedClient) DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetID int) error {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	service, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer c.release(service)
	return service.DeleteRunnerScaleSet(ctx, runnerScaleSetID)
}
