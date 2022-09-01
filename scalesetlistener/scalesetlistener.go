package scalesetlistener

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/github/scalesetclient"
	"github.com/go-logr/logr"
)

type Listener struct {
	ghConfig *github.Config
	logger   logr.Logger
	message  chan struct{}
}

func New(ghConfig *github.Config, logger logr.Logger, message chan struct{}) *Listener {
	return &Listener{
		ghConfig: ghConfig,
		logger:   logger,
		message:  message,
	}
}

func (l *Listener) Run(ctx context.Context) error {
	ghClient, err := l.ghConfig.NewClient()
	if err != nil {
		return fmt.Errorf("Client creation failed: %v", err)
	}

	actionsAdminConnection, err := ghClient.GetActionsServiceAdminConnection(ctx, l.ghConfig.RunnerEnterprise, l.ghConfig.RunnerOrg, l.ghConfig.RunnerRepository)
	if err != nil {
		return fmt.Errorf("Could not create an Actions Service admin connection: %v", err)
	}

	actionsServiceClient := newActionsClient(actionsAdminConnection)

	runnerScaleSet, err := createRunnerScaleSet(ctx, actionsServiceClient, l.ghConfig.RunnerScaleSetName)
	if err != nil {
		return err
	}

	hostName, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("Get host name failed: %v", err)
	}

	session, err := createRunnerScaleSetSession(ctx, actionsServiceClient, runnerScaleSet.Id, hostName)
	if err != nil {
		return err
	}

	defer actionsServiceClient.DeleteMessageSession(ctx, session.RunnerScaleSet.Id, session.SessionId)

	var lastMessageId int64 = 0
	for {
		l.logger.Info("Waiting for message...")

		select {
		case <-ctx.Done():
			l.logger.Info("Message queue listener is stopped.")
			return nil
		default:
		}

		message, err := getMessage(ctx, actionsServiceClient, l.logger, session.MessageQueueUrl, session.MessageQueueAccessToken, lastMessageId)
		if err != nil {
			var tokenExpiredErr *github.MessageQueueTokenExpiredError
			if !errors.As(err, &tokenExpiredErr) {
				l.logger.Error(err, "Error: Get message failed.")
				continue
			}

			l.logger.Info("Message queue token is expired, refreshing...")
			actionsAdminConnection, err = ghClient.GetActionsServiceAdminConnection(ctx, l.ghConfig.RunnerEnterprise, l.ghConfig.RunnerOrg, l.ghConfig.RunnerRepository)
			if err != nil {
				l.logger.Error(err, "Error: Get Actions service admin connection failed during message session refresh.")
				continue
			}

			actionsServiceClient.ActionsServiceAdminToken = actionsAdminConnection.AdminToken

			session, err = actionsServiceClient.RefreshMessageSession(ctx, session.RunnerScaleSet.Id, session.SessionId)
			if err != nil {
				l.logger.Error(err, "Error: Refresh message session failed.")
				continue
			}
		}

		if message == nil {
			continue
		}

		lastMessageId = message.MessageId

		switch message.MessageType {
		case "RunnerScaleSetJobAvailable":
			scalesetclient.MaybeAcquireJob(ctx, l.logger, actionsServiceClient, session, message)
		case "RunnerScaleSetJobAssigned":
			scalesetclient.HandleJobAssignment(ctx, l.logger, actionsServiceClient, runnerScaleSet, message)
		case "RunnerScaleSetJobCompleted":
			scalesetclient.NoopHandleJobCompletion(l.logger, message)
		default:
			l.logger.Info("Unknown message type received.", "messageType", message.MessageType)
		}
	}
}

func newActionsClient(actionsAdminConnection *github.ActionsServiceAdminConnection) *github.ActionsClient {
	return &github.ActionsClient{
		Client:                   http.DefaultClient,
		ActionsServiceURL:        actionsAdminConnection.ActionsServiceUrl,
		ActionsServiceAdminToken: actionsAdminConnection.AdminToken,
		UserAgent:                "actions-runner-controller-message-queue-listener",
	}
}

func newRunnerScaleSet(scaleSetName string) *github.RunnerScaleSet {
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

func createRunnerScaleSet(ctx context.Context, actionsServiceClient *github.ActionsClient, name string) (*github.RunnerScaleSet, error) {
	runnerScaleSet, err := actionsServiceClient.GetRunnerScaleSet(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("Can not found runner scale set: %v", err)
	}

	if runnerScaleSet != nil {
		// logger.Info("Get runner scale set.", "id", runnerScaleSet.Id, "name", runnerScaleSet.Name)

		replaceRunnerScaleSet := newRunnerScaleSet(name)

		runnerScaleSet, err = actionsServiceClient.ReplaceRunnerScaleSet(ctx, runnerScaleSet.Id, replaceRunnerScaleSet)
		if err != nil {
			return nil, fmt.Errorf("Create runner scale set failed: %v", err)
		}
	} else {
		//logger.Info("Runner scale set is not found, creating a new one.")

		newRunnerScaleSet := newRunnerScaleSet(name)

		runnerScaleSet, err = actionsServiceClient.CreateRunnerScaleSet(ctx, newRunnerScaleSet)
		if err != nil {
			return nil, fmt.Errorf("Create runner scale set failed: %v", err)
		}
	}

	return runnerScaleSet, nil
}

func createRunnerScaleSetSession(ctx context.Context, actionsServiceClient *github.ActionsClient, scaleSetId int, name string) (*github.RunnerScaleSetSession, error) {
	retries := 3
	var (
		runnerScaleSetSession *github.RunnerScaleSetSession
		err                   error
	)
	for i := 0; i < retries; i++ {
		runnerScaleSetSession, err = actionsServiceClient.CreateMessageSession(ctx, scaleSetId, name)
		if err == nil {
			return runnerScaleSetSession, nil
		}
		log.Println("Unable to create message session. Will try again in 30 seconds", "error", err.Error())
		retries--
		time.Sleep(30 * time.Second)
	}

	return nil, fmt.Errorf("Unable to create session in %d attempts", retries)
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

func anyEmpty(first string, others ...string) bool {
	values := append(others, first)
	for _, v := range values {
		if len(v) == 0 {
			return true
		}
	}
	return false
}
