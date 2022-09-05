package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/logging"
	"github.com/actions-runner-controller/actions-runner-controller/scalesetlistener"
	"github.com/actions-runner-controller/actions-runner-controller/scalesetoperator"
	"github.com/kelseyhightower/envconfig"
)

type RunnerScaleSetListenerConfig struct {
	RunnerDeploymentNameSpace string `split_words:"true"`
	RunnerDeploymentName      string `split_words:"true"`
	RunnerScaleSetName        string `split_words:"true"`
	RunnerEnterprise          string `split_words:"true"`
	RunnerOrg                 string `split_words:"true"`
	RunnerRepository          string `split_words:"true"`
}

func main() {
	var c github.Config
	if err := envconfig.Process("github", &c); err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables for github.Config: %v\n", err)
		os.Exit(1)
	}

	var rc RunnerScaleSetListenerConfig
	if err := envconfig.Process("github", &rc); err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables for RunnerScaleSetListenerConfig: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cSig := make(chan os.Signal, 1)
	signal.Notify(cSig, os.Interrupt)
	defer func() {
		signal.Stop(cSig)
		cancel()
	}()
	go func() {
		select {
		case <-cSig:
			cancel()
		case <-ctx.Done():
		}
	}()

	message := make(chan *scalesetlistener.Message, 1)
	scaleSetListenerConfig := &scalesetlistener.Config{
		RunnerScaleSetName: rc.RunnerScaleSetName,
		RunnerEnterprise:   rc.RunnerEnterprise,
		RunnerOrg:          rc.RunnerOrg,
		RunnerRepository:   rc.RunnerRepository,
	}

	logger := logging.NewLogger(logging.LogLevelDebug)

	listener := scalesetlistener.New(scaleSetListenerConfig, &c, logger, message)
	if err := listener.MakeClients(ctx); err != nil {
		logger.Info("failed to make listener clients", "error", err.Error())
		os.Exit(1)
	}

	// TODO: Configurable...
	maxRunners := 10
	operator := scalesetoperator.New(listener, logger, maxRunners)

	// TODO: figure out which operator to use
	if err := operator.RunJobOperator(ctx); err != nil {
		log.Fatal(err)
	}
}
