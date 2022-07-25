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
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/logging"
	"github.com/go-logr/logr"

	"github.com/kelseyhightower/envconfig"
	ctrl "sigs.k8s.io/controller-runtime"
)

type JobAvailable struct {
	AcquireJobUrl string `json:"acquireJobUrl"`
	JobMessageBase
}

type JobAssigned struct {
	JobMessageBase
}

type JobCompleted struct {
	Result string `json:"result"`
	JobMessageBase
}

type JobMessageBase struct {
	MessageType     string   `json:"messageType"`
	RunnerRequestId int64    `json:"runnerRequestId"`
	RepositoryName  string   `json:"repositoryName"`
	OwnerName       string   `json:"ownerName"`
	JobWorkflowRef  string   `json:"jobWorkflowRef"`
	EventName       string   `json:"eventName"`
	RequestLabels   []string `json:"requestLabels"`
}

func main() {
	var (
		err error

		ghClient *github.Client
	)

	var c github.Config
	err = envconfig.Process("github", &c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewLogger(logging.LogLevelDebug)
	ctrl.SetLogger(logger)

	if len(c.RunnerRepository) <= 0 || len(c.RunnerOrg) <= 0 || len(c.Token) <= 0 || len(c.RunnerScaleSetName) <= 0 {
		fmt.Fprintln(os.Stderr, "GitHub config is not provided:", c.RunnerRepository, c.RunnerOrg, c.Token, c.RunnerScaleSetName)
		os.Exit(1)
	}
	if len(c.Token) > 0 || (c.AppID > 0 && c.AppInstallationID > 0 && c.AppPrivateKey != "") || (len(c.BasicauthUsername) > 0 && len(c.BasicauthPassword) > 0) {
		c.Log = &logger

		ghClient, err = c.NewClient()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: Client creation failed.", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "GitHub client is not initialized.")
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

	actionsAdminConnection, err := ghClient.GetActionsServiceAdminConnection(ctx, c.RunnerEnterprise, c.RunnerOrg, c.RunnerRepository)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Get Actions service admin connection failed.", err)
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

	runnerScaleSet, err := actionsServiceClient.GetRunnerScaleSet(ctx, c.RunnerScaleSetName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Can not found runner scale set.", err)
		os.Exit(1)
	}

	if runnerScaleSet != nil {
		logger.Info("Get runner scale set.", "id", runnerScaleSet.Id, "name", runnerScaleSet.Name)

		replaceRunnerScaleSet := &github.RunnerScaleSet{
			Name:          c.RunnerScaleSetName,
			RunnerGroupId: 1,
			Labels: []github.Label{
				{Name: "runner-scale-set", Type: "User"},
			},
			RunnerSetting: github.RunnerSetting{
				Ephemeral:     true,
				DisableUpdate: true,
			},
		}

		runnerScaleSet, err = actionsServiceClient.ReplaceRunnerScaleSet(ctx, runnerScaleSet.Id, replaceRunnerScaleSet)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: Create runner scale set failed.", err)
			os.Exit(1)
		}

	} else {
		logger.Info("Runner scale set is not found, creating a new one.")
		newRunnerScaleSet := &github.RunnerScaleSet{
			Name:          c.RunnerScaleSetName,
			RunnerGroupId: 1,
			Labels: []github.Label{
				{Name: "auto-scale", Type: "User"},
			},
			RunnerSetting: github.RunnerSetting{
				Ephemeral:     true,
				DisableUpdate: true,
			},
		}

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

	runnerScaleSetSession, err := actionsServiceClient.CreateMessageSession(ctx, runnerScaleSet.Id, hostName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Create message session failed.", err)
		os.Exit(1)
	}

	logger.Info("Created runner scale set message queue session.", "sessionId", runnerScaleSetSession.SessionId, "url", runnerScaleSetSession.MessageQueueUrl, "token", runnerScaleSetSession.MessageQueueAccessToken)
	defer actionsServiceClient.DeleteMessageSession(context.TODO(), runnerScaleSetSession.RunnerScaleSet.Id, runnerScaleSetSession.SessionId)

	var lastMessageId int64 = 0
	for {
		logger.Info("Waiting for message...")
		select {
		case <-ctx.Done():
			logger.Info("Message queue listener is stopped.")
			return
		default:
			message, err := GetMessage(ctx, actionsServiceClient, logger, runnerScaleSetSession.MessageQueueUrl, runnerScaleSetSession.MessageQueueAccessToken, lastMessageId)
			if err != nil {
				logger.Error(err, "Error: Get message failed.")
				if _, ok := err.(*github.MessageQueueTokenExpiredError); ok {
					logger.Info("Message queue token is expired, refreshing...")
					actionsAdminConnection, err = ghClient.GetActionsServiceAdminConnection(ctx, c.RunnerEnterprise, c.RunnerOrg, c.RunnerRepository)
					if err != nil {
						fmt.Fprintln(os.Stderr, "Error: Get Actions service admin connection failed during message session refresh.", err)
						os.Exit(1)
					}
					actionsServiceClient.ActionsServiceAdminToken = actionsAdminConnection.AdminToken
					runnerScaleSetSession, err = actionsServiceClient.RefreshMessageSession(ctx, runnerScaleSetSession.RunnerScaleSet.Id, runnerScaleSetSession.SessionId)
					if err != nil {
						fmt.Fprintln(os.Stderr, "Error: Refresh message session failed.", err)
						os.Exit(1)
					}
				}
			}

			if message != nil {
				lastMessageId = message.MessageId
				if message.MessageType == "RunnerScaleSetJobAvailable" {
					var jobAvailable JobAvailable
					err = json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobAvailable)
					if err != nil {
						logger.Error(err, "Error: Decode RunnerScaleSetJobAvailable message body failed.")
						continue
					}

					logger.Info("Runner scale set job available message received.", "messageId", message.MessageId, "RequestId", jobAvailable.RunnerRequestId)
					err = actionsServiceClient.AcquireJob(ctx, jobAvailable.AcquireJobUrl, runnerScaleSetSession.MessageQueueAccessToken)
					if err != nil {
						logger.Error(err, "Error: Acquire job failed.")
						continue
					}

					logger.Info("Tried to acquire job.", "RequestId", jobAvailable.RunnerRequestId)
				} else if message.MessageType == "RunnerScaleSetJobAssigned" {
					var jobAssigned JobAssigned
					err = json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobAssigned)
					if err != nil {
						logger.Error(err, "Error: Decode RunnerScaleSetJobAssigned message body failed.")
						continue
					}

					logger.Info("Runner scale set job assigned message received.", "messageId", message.MessageId, "RequestId", jobAssigned.RunnerRequestId, "JitConfigUrl", runnerScaleSet.RunnerJitConfigUrl)
					jitConfig, err := actionsServiceClient.GenerateJitRunnerConfig(ctx, &github.RunnerScaleSetJitRunnerSetting{WorkFolder: "__work"}, runnerScaleSet.RunnerJitConfigUrl)
					if err != nil {
						logger.Error(err, "Error: Generate JIT runner config failed.")
						continue
					}

					logger.Info("Generated JIT runner config.", "RequestId", jobAssigned.RunnerRequestId, "RunnerId", jitConfig.Runner.Id, "JitConfig", jitConfig.EncodedJITConfig)
				} else if message.MessageType == "RunnerScaleSetJobCompleted" {
					var jobCompleted JobCompleted
					err = json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobCompleted)
					if err != nil {
						logger.Error(err, "Error: Decode RunnerScaleSetJobCompleted message body failed.")
						continue
					}

					logger.Info("Runner scale set job completed message received.", "messageId", message.MessageId, "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result)
				} else {
					logger.Info("Unknown message type received.", "messageType", message.MessageType)
				}
			}
		}
	}
	// err = actionsServiceClient.DeleteRunnerScaleSet(ctx, runnerScaleSet.Id)
	// if err != nil {
	// 	fmt.Fprintln(os.Stderr, "Error: Delete runner scale set failed.", err)
	// 	os.Exit(1)
	// }

	// Create a runner scale set listening session

	// While loop to pull message from the message queue

	// Enqueue the message to background thread for processing
}

func GetMessage(ctx context.Context, c *github.ActionsClient, logger logr.Logger, messageQueueUrl, messageQueueToken string, lastMessageId int64) (*github.RunnerScaleSetMessage, error) {
	message, err := c.GetMessage(ctx, messageQueueUrl, messageQueueToken, lastMessageId)
	if err != nil {
		return nil, err
	}

	if message != nil {
		logger.Info("Get message.", "messageId", message.MessageId, "messageType", message.MessageType, "body", message.Body)
		defer DeleteMessage(ctx, c, logger, messageQueueUrl, messageQueueToken, message.MessageId)

		return message, nil
	}

	return nil, nil
}

func DeleteMessage(ctx context.Context, c *github.ActionsClient, logger logr.Logger, messageQueueUrl, messageQueueToken string, messageId int64) {
	err := c.DeleteMessage(context.TODO(), messageQueueUrl, messageQueueToken, messageId)
	if err != nil {
		logger.Error(err, "Error: Delete message failed.")
	}

	logger.Info("Deleted message.", "messageId", messageId)
}
