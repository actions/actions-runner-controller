/*
Copyright 2021 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/go-logr/logr"
	"github.com/kelseyhightower/envconfig"
)

type RunnerScaleSetListenerConfig struct {
	ConfigureUrl                string `split_words:"true"`
	AppID                       int64  `split_words:"true"`
	AppInstallationID           int64  `split_words:"true"`
	AppPrivateKey               string `split_words:"true"`
	Token                       string `split_words:"true"`
	EphemeralRunnerSetNamespace string `split_words:"true"`
	EphemeralRunnerSetName      string `split_words:"true"`
	MaxRunners                  int    `split_words:"true"`
	MinRunners                  int    `split_words:"true"`
	RunnerScaleSetId            int    `split_words:"true"`
}

func main() {
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating logger: %v\n", err)
		os.Exit(1)
	}

	var rc RunnerScaleSetListenerConfig
	if err := envconfig.Process("github", &rc); err != nil {
		logger.Error(err, "Error: processing environment variables for RunnerScaleSetListenerConfig")
		os.Exit(1)
	}

	// Validate all inputs
	if err := validateConfig(&rc); err != nil {
		logger.Error(err, "Inputs validation failed")
		os.Exit(1)
	}

	if err := run(rc, logger); err != nil {
		logger.Error(err, "Run error")
		os.Exit(1)
	}
}

func run(rc RunnerScaleSetListenerConfig, logger logr.Logger) error {
	// Create root context and hook with sigint and sigterm
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	creds := &actions.ActionsAuth{}
	if rc.Token != "" {
		creds.Token = rc.Token
	} else {
		creds.AppCreds = &actions.GitHubAppAuth{
			AppID:             rc.AppID,
			AppInstallationID: rc.AppInstallationID,
			AppPrivateKey:     rc.AppPrivateKey,
		}
	}

	actionsServiceClient, err := actions.NewClient(
		ctx,
		rc.ConfigureUrl,
		creds,
		actions.WithUserAgent(fmt.Sprintf("actions-runner-controller/%s", build.Version)),
		actions.WithLogger(logger),
	)
	if err != nil {
		return fmt.Errorf("failed to create an Actions Service client: %w", err)
	}

	// Create message listener
	autoScalerClient, err := NewAutoScalerClient(ctx, actionsServiceClient, &logger, rc.RunnerScaleSetId)
	if err != nil {
		return fmt.Errorf("failed to create a message listener: %w", err)
	}
	defer autoScalerClient.Close()

	// Create kube manager and scale controller
	kubeManager, err := NewKubernetesManager(&logger)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes manager: %w", err)
	}

	scaleSettings := &ScaleSettings{
		Namespace:    rc.EphemeralRunnerSetNamespace,
		ResourceName: rc.EphemeralRunnerSetName,
		MaxRunners:   rc.MaxRunners,
		MinRunners:   rc.MinRunners,
	}

	service := NewService(ctx, autoScalerClient, kubeManager, scaleSettings, func(s *Service) {
		s.logger = logger.WithName("service")
	})

	// Start listening for messages
	if err = service.Start(); err != nil {
		return fmt.Errorf("failed to start message queue listener: %w", err)
	}
	return nil
}

func validateConfig(config *RunnerScaleSetListenerConfig) error {
	if len(config.ConfigureUrl) == 0 {
		return fmt.Errorf("GitHubConfigUrl is not provided")
	}

	if len(config.EphemeralRunnerSetNamespace) == 0 || len(config.EphemeralRunnerSetName) == 0 {
		return fmt.Errorf("EphemeralRunnerSetNamespace '%s' or EphemeralRunnerSetName '%s' is missing", config.EphemeralRunnerSetNamespace, config.EphemeralRunnerSetName)
	}

	if config.RunnerScaleSetId == 0 {
		return fmt.Errorf("RunnerScaleSetId '%d' is missing", config.RunnerScaleSetId)
	}

	if config.MaxRunners < config.MinRunners {
		return fmt.Errorf("MinRunners '%d' cannot be greater than MaxRunners '%d'", config.MinRunners, config.MaxRunners)
	}

	hasToken := len(config.Token) > 0
	hasPrivateKeyConfig := config.AppID > 0 && config.AppPrivateKey != ""

	if !hasToken && !hasPrivateKeyConfig {
		return fmt.Errorf("GitHub auth credential is missing, token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	}

	if hasToken && hasPrivateKeyConfig {
		return fmt.Errorf("only one GitHub auth method supported at a time. Have both PAT and App auth: token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	}

	return nil
}
