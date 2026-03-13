package fake

import (
	"context"

	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/scaleset"
)

// ClientOption is a functional option for configuring a fake Client
type ClientOption func(*Client)

// WithGetRunnerScaleSet configures the result of GetRunnerScaleSet
func WithGetRunnerScaleSet(result *scaleset.RunnerScaleSet, err error) ClientOption {
	return func(c *Client) {
		c.getRunnerScaleSetResult.RunnerScaleSet = result
		c.getRunnerScaleSetResult.err = err
	}
}

// WithGetRunnerScaleSetByID configures the result of GetRunnerScaleSetByID
func WithGetRunnerScaleSetByID(result *scaleset.RunnerScaleSet, err error) ClientOption {
	return func(c *Client) {
		c.getRunnerScaleSetByIDResult.RunnerScaleSet = result
		c.getRunnerScaleSetByIDResult.err = err
	}
}

// WithGetRunnerGroupByName configures the result of GetRunnerGroupByName
func WithGetRunnerGroupByName(result *scaleset.RunnerGroup, err error) ClientOption {
	return func(c *Client) {
		c.getRunnerGroupByNameResult.RunnerGroup = result
		c.getRunnerGroupByNameResult.err = err
	}
}

// WithGetRunnerGroupByNameFunc configures a function to handle GetRunnerGroupByName calls dynamically
func WithGetRunnerGroupByNameFunc(fn func(context.Context, string) (*scaleset.RunnerGroup, error)) ClientOption {
	return func(c *Client) {
		c.getRunnerGroupByNameFunc = fn
	}
}

// WithCreateRunnerScaleSet configures the result of CreateRunnerScaleSet
func WithCreateRunnerScaleSet(result *scaleset.RunnerScaleSet, err error) ClientOption {
	return func(c *Client) {
		c.createRunnerScaleSetResult.RunnerScaleSet = result
		c.createRunnerScaleSetResult.err = err
	}
}

// WithUpdateRunnerScaleSet configures the result of UpdateRunnerScaleSet
func WithUpdateRunnerScaleSet(result *scaleset.RunnerScaleSet, err error) ClientOption {
	return func(c *Client) {
		c.updateRunnerScaleSetResult.RunnerScaleSet = result
		c.updateRunnerScaleSetResult.err = err
	}
}

// WithDeleteRunnerScaleSet configures the result of DeleteRunnerScaleSet
func WithDeleteRunnerScaleSet(err error) ClientOption {
	return func(c *Client) {
		c.deleteRunnerScaleSetResult.err = err
	}
}

// WithRemoveRunner configures the result of RemoveRunner
func WithRemoveRunner(err error) ClientOption {
	return func(c *Client) {
		c.removeRunnerResult.err = err
	}
}

// WithGenerateJitRunnerConfig configures the result of GenerateJitRunnerConfig
func WithGenerateJitRunnerConfig(result *scaleset.RunnerScaleSetJitRunnerConfig, err error) ClientOption {
	return func(c *Client) {
		c.generateJitRunnerConfigResult.RunnerScaleSetJitRunnerConfig = result
		c.generateJitRunnerConfigResult.err = err
	}
}

// WithGetRunnerByName configures the result of GetRunnerByName
func WithGetRunnerByName(result *scaleset.RunnerReference, err error) ClientOption {
	return func(c *Client) {
		c.getRunnerByNameResult.RunnerReference = result
		c.getRunnerByNameResult.err = err
	}
}

// WithGetRunner configures the result of GetRunner
func WithGetRunner(result *scaleset.RunnerReference, err error) ClientOption {
	return func(c *Client) {
		c.getRunnerResult.RunnerReference = result
		c.getRunnerResult.err = err
	}
}

// WithSystemInfo configures the SystemInfo
func WithSystemInfo(info scaleset.SystemInfo) ClientOption {
	return func(c *Client) {
		c.systemInfo = info
	}
}

// WithUpdateRunnerScaleSetFunc configures a function to handle UpdateRunnerScaleSet calls dynamically
func WithUpdateRunnerScaleSetFunc(fn func(context.Context, int, *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error)) ClientOption {
	return func(c *Client) {
		c.updateRunnerScaleSetFunc = fn
	}
}

// Client implements multiclient.Client interface for testing
type Client struct {
	systemInfo               scaleset.SystemInfo
	updateRunnerScaleSetFunc func(context.Context, int, *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error)

	getRunnerScaleSetResult struct {
		*scaleset.RunnerScaleSet
		err error
	}
	getRunnerScaleSetByIDResult struct {
		*scaleset.RunnerScaleSet
		err error
	}
	getRunnerGroupByNameResult struct {
		*scaleset.RunnerGroup
		err error
	}
	getRunnerGroupByNameFunc   func(context.Context, string) (*scaleset.RunnerGroup, error)
	createRunnerScaleSetResult struct {
		*scaleset.RunnerScaleSet
		err error
	}
	updateRunnerScaleSetResult struct {
		*scaleset.RunnerScaleSet
		err error
	}
	deleteRunnerScaleSetResult struct {
		err error
	}
	removeRunnerResult struct {
		err error
	}
	generateJitRunnerConfigResult struct {
		*scaleset.RunnerScaleSetJitRunnerConfig
		err error
	}
	getRunnerByNameResult struct {
		*scaleset.RunnerReference
		err error
	}
	getRunnerResult struct {
		*scaleset.RunnerReference
		err error
	}
	messageSessionClientResult struct {
		*scaleset.MessageSessionClient
		err error
	}
}

// Compile-time interface check
var _ multiclient.Client = (*Client)(nil)

// NewClient creates a new fake Client with the given options
func NewClient(opts ...ClientOption) *Client {
	c := &Client{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) SetSystemInfo(info scaleset.SystemInfo) {
	c.systemInfo = info
}

func (c *Client) SystemInfo() scaleset.SystemInfo {
	return c.systemInfo
}

func (c *Client) MessageSessionClient(ctx context.Context, runnerScaleSetID int, owner string, options ...scaleset.HTTPOption) (*scaleset.MessageSessionClient, error) {
	return c.messageSessionClientResult.MessageSessionClient, c.messageSessionClientResult.err
}

func (c *Client) GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	return c.generateJitRunnerConfigResult.RunnerScaleSetJitRunnerConfig, c.generateJitRunnerConfigResult.err
}

func (c *Client) GetRunner(ctx context.Context, runnerID int) (*scaleset.RunnerReference, error) {
	return c.getRunnerResult.RunnerReference, c.getRunnerResult.err
}

func (c *Client) GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error) {
	return c.getRunnerByNameResult.RunnerReference, c.getRunnerByNameResult.err
}

func (c *Client) RemoveRunner(ctx context.Context, runnerID int64) error {
	return c.removeRunnerResult.err
}

func (c *Client) GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*scaleset.RunnerGroup, error) {
	if c.getRunnerGroupByNameFunc != nil {
		return c.getRunnerGroupByNameFunc(ctx, runnerGroup)
	}
	return c.getRunnerGroupByNameResult.RunnerGroup, c.getRunnerGroupByNameResult.err
}

func (c *Client) GetRunnerScaleSet(ctx context.Context, runnerGroupID int, runnerScaleSetName string) (*scaleset.RunnerScaleSet, error) {
	return c.getRunnerScaleSetResult.RunnerScaleSet, c.getRunnerScaleSetResult.err
}

func (c *Client) GetRunnerScaleSetByID(ctx context.Context, runnerScaleSetID int) (*scaleset.RunnerScaleSet, error) {
	return c.getRunnerScaleSetByIDResult.RunnerScaleSet, c.getRunnerScaleSetByIDResult.err
}

func (c *Client) CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
	return c.createRunnerScaleSetResult.RunnerScaleSet, c.createRunnerScaleSetResult.err
}

func (c *Client) UpdateRunnerScaleSet(ctx context.Context, runnerScaleSetID int, runnerScaleSet *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
	if c.updateRunnerScaleSetFunc != nil {
		return c.updateRunnerScaleSetFunc(ctx, runnerScaleSetID, runnerScaleSet)
	}
	return c.updateRunnerScaleSetResult.RunnerScaleSet, c.updateRunnerScaleSetResult.err
}

func (c *Client) DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetID int) error {
	return c.deleteRunnerScaleSetResult.err
}
