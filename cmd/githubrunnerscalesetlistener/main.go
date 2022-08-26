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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/logging"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/github/scalesetclient"
	"github.com/go-logr/logr"
	"github.com/kelseyhightower/envconfig"
)

func getRunnerScaleSet(scaleSetName string) *github.RunnerScaleSet {
	return &github.RunnerScaleSet{
		Name:          scaleSetName,
		RunnerGroupId: 1,
		Labels: []github.Label{
			{Name: scaleSetName, Type: "User"},
		},
		RunnerSetting: github.RunnerSetting{
			Ephemeral:     true,
			DisableUpdate: true,
		},
	}
}

type RunnerScaleSetListenerConfig struct {
	RunnerDeploymentNameSpace string `split_words:"true"`
	RunnerDeploymentName      string `split_words:"true"`
	RunnerScaleSetName        string `split_words:"true"`
	RunnerEnterprise          string `split_words:"true"`
	RunnerOrg                 string `split_words:"true"`
	RunnerRepository          string `split_words:"true"`
}

func main() {
	var (
		err error

		ghClient *github.Client
	)

	var c github.Config
	err = envconfig.Process("github", &c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables for github.Config: %v\n", err)
		os.Exit(1)
	}

	var rc RunnerScaleSetListenerConfig
	err = envconfig.Process("github", &rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables for RunnerScaleSetListenerConfig: %v\n", err)
		os.Exit(1)
	}

	if len(rc.RunnerRepository) == 0 || len(rc.RunnerOrg) == 0 || len(c.Token) == 0 || len(rc.RunnerScaleSetName) == 0 {
		fmt.Fprintln(os.Stderr, "GitHub config is not provided:", rc.RunnerRepository, rc.RunnerOrg, c.Token, rc.RunnerScaleSetName)
		os.Exit(1)
	}

	if len(rc.RunnerDeploymentNameSpace) == 0 || len(rc.RunnerDeploymentName) == 0 {
		fmt.Fprintln(os.Stderr, "RunnerDeployment config is not provided:", rc.RunnerDeploymentNameSpace, rc.RunnerDeploymentName)
		os.Exit(1)
	}

	logger := logging.NewLogger(logging.LogLevelDebug)
	hasToken := len(c.Token) > 0
	hasPrivateKeyConfig := c.AppID > 0 && c.AppInstallationID > 0 && c.AppPrivateKey != ""
	hasBasicAuth := len(c.BasicauthUsername) > 0 && len(c.BasicauthPassword) > 0

	if !hasToken && !hasPrivateKeyConfig && !hasBasicAuth {
		fmt.Fprintln(os.Stderr, "GitHub client cannot initialize. Must provide any of token or private key or basic auth creds.")
		os.Exit(1)
	}

	c.Log = &logger

	ghClient, err = c.NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Client creation failed.", err)
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

	actionsAdminConnection, err := ghClient.GetActionsServiceAdminConnection(ctx, rc.RunnerEnterprise, rc.RunnerOrg, rc.RunnerRepository)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Could not create an Actions Service admin connection.", err)
		os.Exit(1)
	}

	logger.Info("Admin connection created.", "actionsServiceUrl", actionsAdminConnection.ActionsServiceUrl, "adminToken", actionsAdminConnection.AdminToken)

	// Create and get a runner scale set
	actionsServiceClient := &github.ActionsClient{
		Client:                   http.DefaultClient,
		ActionsServiceURL:        actionsAdminConnection.ActionsServiceUrl,
		ActionsServiceAdminToken: actionsAdminConnection.AdminToken,
		UserAgent:                "actions-runner-controller-message-queue-listener",
	}

	runnerScaleSet, err := actionsServiceClient.GetRunnerScaleSet(ctx, rc.RunnerScaleSetName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Can not found runner scale set.", err)
		os.Exit(1)
	}

	if runnerScaleSet != nil {
		logger.Info("Get runner scale set.", "id", runnerScaleSet.Id, "name", runnerScaleSet.Name)
		statistics, _ := json.Marshal(runnerScaleSet.Statistics)
		logger.Info("Current runner scale set statistics.", "statistics", string(statistics))
	} else {
		logger.Info("Runner scale set is not found, creating a new one.")

		newRunnerScaleSet := getRunnerScaleSet(rc.RunnerScaleSetName)

		runnerScaleSet, err = actionsServiceClient.CreateRunnerScaleSet(ctx, newRunnerScaleSet)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: Create runner scale set failed.", err)
			os.Exit(1)
		}

		logger.Info("Created runner scale set.", "id", runnerScaleSet.Id)
	}

	hostName, err := os.Hostname()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Get host name failed.", err)
		os.Exit(1)
	}

	var runnerScaleSetSession *github.RunnerScaleSetSession
	for {
		runnerScaleSetSession, err = actionsServiceClient.CreateMessageSession(ctx, runnerScaleSet.Id, hostName)
		if err == nil {
			break
		}

		clientSideError := &github.HttpClientSideError{}
		if errors.As(err, &clientSideError) {
			logger.Info("Unable to create message session. The error indicates something is wrong on the client side, won't make any retry.", "error", err.Error())
			fmt.Fprintln(os.Stderr, "Error: Create message session failed.", err)
			os.Exit(1)
		} else {
			logger.Info("Unable to create message session. Will try again in 30 seconds", "error", err.Error())
			time.Sleep(30 * time.Second)
		}
	}

	logger.Info("Created runner scale set message queue session.", "sessionId", runnerScaleSetSession.SessionId, "url", runnerScaleSetSession.MessageQueueUrl, "token", runnerScaleSetSession.MessageQueueAccessToken)

	defer actionsServiceClient.DeleteMessageSession(ctx, runnerScaleSetSession.RunnerScaleSet.Id, runnerScaleSetSession.SessionId)

	var lastMessageId int64 = 0

	for {
		logger.Info("Waiting for message...")

		select {
		case <-ctx.Done():
			logger.Info("Message queue listener is stopped.")
			return
		default:
			// We will recursively check against this error type in errors.As call below
			expiredError := &github.MessageQueueTokenExpiredError{}

			message, err := getMessage(ctx, actionsServiceClient, logger, runnerScaleSetSession.MessageQueueUrl, runnerScaleSetSession.MessageQueueAccessToken, lastMessageId)
			if errors.As(err, &expiredError) {
				logger.Info("Message queue token is expired, refreshing...")

				actionsAdminConnection, err = ghClient.GetActionsServiceAdminConnection(ctx, rc.RunnerEnterprise, rc.RunnerOrg, rc.RunnerRepository)
				if err != nil {
					logger.Error(err, "Error: Get Actions service admin connection failed during message session refresh.")
					continue
				}

				actionsServiceClient.ActionsServiceAdminToken = actionsAdminConnection.AdminToken

				runnerScaleSetSession, err = actionsServiceClient.RefreshMessageSession(ctx, runnerScaleSetSession.RunnerScaleSet.Id, runnerScaleSetSession.SessionId)
				if err != nil {
					logger.Error(err, "Error: Refresh message session failed.")
					continue
				}
			} else if err != nil {
				logger.Error(err, "Error: Get message failed.")
				continue
			}

			if message == nil {
				continue
			}

			lastMessageId = message.MessageId

			statistics, _ := json.Marshal(message.Statistics)
			logger.Info("Current runner scale set statistics.", "statistics", string(statistics))

			switch message.MessageType {
			case "RunnerScaleSetJobAvailable":
				scalesetclient.MaybeAcquireJob(ctx, logger, actionsServiceClient, runnerScaleSetSession, message)
			case "RunnerScaleSetJobAssigned":
				scalesetclient.HandleJobAssignment(ctx, logger, actionsServiceClient, runnerScaleSet, message)
			case "RunnerScaleSetJobCompleted":
				scalesetclient.NoopHandleJobCompletion(logger, message)
			case "RunnerScaleSetJobMessages":
				scalesetclient.HandleBatchedRunnerScaleSetMessages(ctx, logger, rc.RunnerDeploymentNameSpace, rc.RunnerDeploymentName, actionsServiceClient, runnerScaleSetSession, message)
			default:
				logger.Info("Unknown message type received.", "messageType", message.MessageType)
			}
		}
	}
}

func getMessage(ctx context.Context, c *github.ActionsClient, logger logr.Logger, messageQueueUrl, messageQueueToken string, lastMessageId int64) (*github.RunnerScaleSetMessage, error) {
	message, err := c.GetMessage(ctx, messageQueueUrl, messageQueueToken, lastMessageId)
	if err != nil {
		return nil, err
	}

	if message == nil {
		return nil, nil
	}

	logger.Info("Get message.", "messageId", message.MessageId, "messageType", message.MessageType, "body", message.Body)

	defer deleteMessage(ctx, c, logger, messageQueueUrl, messageQueueToken, message.MessageId)

	return message, nil
}

func deleteMessage(ctx context.Context, c *github.ActionsClient, logger logr.Logger, messageQueueUrl, messageQueueToken string, messageId int64) {
	if err := c.DeleteMessage(ctx, messageQueueUrl, messageQueueToken, messageId); err != nil {
		logger.Error(err, "Error: Delete message failed.")
	}

	logger.Info("Deleted message.", "messageId", messageId)
}
