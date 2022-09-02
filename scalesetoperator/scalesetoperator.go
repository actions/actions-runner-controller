package scalesetoperator

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/github/runnermanager"
	"github.com/actions-runner-controller/actions-runner-controller/scalesetlistener"
	"github.com/go-logr/logr"
)

type Operator struct {
	l          *scalesetlistener.Listener
	logger     logr.Logger
	maxRunners int

	mu      sync.Mutex
	started bool
}

func New(l *scalesetlistener.Listener, logger logr.Logger, maxRunners int) *Operator {
	return &Operator{
		l:          l,
		logger:     logger,
		maxRunners: maxRunners,
	}
}

func (op *Operator) RunJobOperator(ctx context.Context) error {
	op.mu.Lock()
	if op.started {
		op.mu.Unlock()
		return errors.New("operator started")
	}
	op.started = true
	op.mu.Unlock()

	listenerErr := make(chan error)
	go func() {
		listenerErr <- op.l.Run(ctx)
	}()

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
		case err := <-listenerErr:
			return err
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

func (op *Operator) RunDeploymentOperator(ctx context.Context, deploymentName string) error {
	op.mu.Lock()
	if op.started {
		op.mu.Unlock()
		return errors.New("operator started")
	}
	op.started = true
	op.mu.Unlock()

	listenerErr := make(chan error)
	go func() {
		listenerErr <- op.l.Run(ctx)
	}()

	deploymentOperator := deploymentOperator{
		// TODO: see from where to get ns
		namespace:      "default",
		deploymentName: deploymentName,
	}

	stream := op.l.MessageStream()
	for {
		select {
		case err := <-listenerErr:
			return err
		case <-ctx.Done():
			return nil
		default:
		}
		message := <-stream
		deploymentOperator.patchDeployment(ctx, message.N)
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

type deploymentOperator struct {
	namespace      string
	deploymentName string
	logger         logr.Logger
}

func (op *deploymentOperator) patchDeployment(ctx context.Context, desiredRunners int) {
	patched, err := runnermanager.PatchRunnerDeployment(ctx, op.namespace, op.deploymentName, &desiredRunners)
	if err != nil {
		op.logger.Error(err, "Error: Patch runner deployment failed.")
		return
	}
	op.logger.Info("Patched runner deployment.", "patched replicas", patched.Spec.Replicas)
}
