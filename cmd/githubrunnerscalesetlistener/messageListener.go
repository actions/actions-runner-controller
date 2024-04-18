package main

import (
	"context"

	"github.com/actions/actions-runner-controller/github/actions"
)

//go:generate mockery --inpackage --name=RunnerScaleSetClient
type RunnerScaleSetClient interface {
	GetRunnerScaleSetMessage(ctx context.Context, handler func(msg *actions.RunnerScaleSetMessage) error, maxCapacity int) error
	AcquireJobsForRunnerScaleSet(ctx context.Context, requestIds []int64) error
}
