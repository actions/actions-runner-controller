package scalesetoperator

import (
	"context"
	"fmt"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/github/runnermanager"
	"github.com/actions-runner-controller/actions-runner-controller/scalesetlistener"
	"github.com/go-logr/logr"
)

type Operator struct {
	l          *scalesetlistener.Listener
	logger     logr.Logger
	maxRunners int
}

func New(l *scalesetlistener.Listener, logger logr.Logger, maxRunners int) *Operator {
	return &Operator{
		l:          l,
		logger:     logger,
		maxRunners: maxRunners,
	}
}

func (op *Operator) Run(ctx context.Context) error {
	go op.l.Run(ctx)

	jobOperator := jobOperator{
		actionsClient:  op.l.ActionsServiceClient(),
		runnerScaleSet: op.l.RunnerScaleSet(),
		logger:         op.logger,
		buffer:         make(chan struct{}, op.maxRunners),
	}

	runnerJobList, err := runnermanager.GetScaleSetJobs(ctx, jobOperator.runnerScaleSet, "default")
	if err != nil {
		return fmt.Errorf("failed to fetch the state: %v", err)
	}
	prevState := len(runnerJobList.Items)
	go jobOperator.consume(ctx)

	stream := op.l.MessageStream()
	for {
		select {
		case <-ctx.Done():
			close(jobOperator.buffer)
			return nil
		default:
		}
		message := <-stream
		// at first, it is going to be N - previous state - 0
		// every other time, it is going to be N - number of elements we haven't created yet
		scale := message.N - prevState - len(jobOperator.buffer)
		prevState = 0
		for i := 0; i < scale; i++ {
			jobOperator.buffer <- struct{}{} // empty struct since this is essentially call for work with no memory usage
		}
	}
}

type jobOperator struct {
	max            int
	actionsClient  *github.ActionsClient
	runnerScaleSet *github.RunnerScaleSet
	logger         logr.Logger

	buffer chan struct{}
}

func (op *jobOperator) consume(ctx context.Context) {
	for range op.buffer {
		jitConfig, err := op.actionsClient.GenerateJitRunnerConfig(ctx, &github.RunnerScaleSetJitRunnerSetting{WorkFolder: "__work"}, op.runnerScaleSet.RunnerJitConfigUrl)
		if err != nil {
			// TODO: decide how to approach this
			continue
		}

		_, err = runnermanager.CreateJob(ctx, jitConfig, op.runnerScaleSet.Name)
		if err != nil {
			// TODO: decide how to approach this
			continue
		}
	}
}
