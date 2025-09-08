package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

const (
	sessionCreationMaxRetryCount = 10
)

type devContextKey bool

var testIgnoreSleep devContextKey = true

type AutoScalerClient struct {
	client actions.SessionService
	logger logr.Logger

	lastMessageId  int64
	initialMessage *actions.RunnerScaleSetMessage
}

func NewAutoScalerClient(
	ctx context.Context,
	client actions.ActionsService,
	logger *logr.Logger,
	runnerScaleSetId int,
	options ...func(*AutoScalerClient),
) (*AutoScalerClient, error) {
	listener := AutoScalerClient{
		logger: logger.WithName("auto_scaler"),
	}

	session, initialMessage, err := createSession(ctx, &listener.logger, client, runnerScaleSetId)
	if err != nil {
		return nil, fmt.Errorf("fail to create session. %w", err)
	}

	listener.lastMessageId = 0
	listener.initialMessage = initialMessage
	listener.client = newSessionClient(client, logger, session)

	for _, option := range options {
		option(&listener)
	}

	return &listener, nil
}

func createSession(ctx context.Context, logger *logr.Logger, client actions.ActionsService, runnerScaleSetId int) (*actions.RunnerScaleSetSession, *actions.RunnerScaleSetMessage, error) {
	hostName, err := os.Hostname()
	if err != nil {
		hostName = uuid.New().String()
		logger.Info("could not get hostname, fail back to a random string.", "fallback", hostName)
	}

	var runnerScaleSetSession *actions.RunnerScaleSetSession
	var retryCount int
	for {
		runnerScaleSetSession, err = client.CreateMessageSession(ctx, runnerScaleSetId, hostName)
		if err == nil {
			break
		}

		clientSideError := &actions.HttpClientSideError{}
		if errors.As(err, &clientSideError) && clientSideError.Code != http.StatusConflict {
			logger.Info("unable to create message session. The error indicates something is wrong on the client side, won't make any retry.")
			return nil, nil, fmt.Errorf("create message session http request failed. %w", err)
		}

		retryCount++
		if retryCount >= sessionCreationMaxRetryCount {
			return nil, nil, fmt.Errorf("create message session failed since it exceed %d retry limit. %w", sessionCreationMaxRetryCount, err)
		}

		logger.Info("unable to create message session. Will try again in 30 seconds", "error", err.Error())
		if ok := ctx.Value(testIgnoreSleep); ok == nil {
			time.Sleep(getRandomDuration(30, 45))
		}
	}

	statistics, _ := json.Marshal(runnerScaleSetSession.Statistics)
	logger.Info("current runner scale set statistics.", "statistics", string(statistics))

	if runnerScaleSetSession.Statistics.TotalAvailableJobs > 0 || runnerScaleSetSession.Statistics.TotalAssignedJobs > 0 {
		acquirableJobs, err := client.GetAcquirableJobs(ctx, runnerScaleSetId)
		if err != nil {
			return nil, nil, fmt.Errorf("get acquirable jobs failed. %w", err)
		}

		acquirableJobsJson, err := json.Marshal(acquirableJobs.Jobs)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal acquirable jobs failed. %w", err)
		}

		initialMessage := &actions.RunnerScaleSetMessage{
			MessageId:   0,
			MessageType: "RunnerScaleSetJobMessages",
			Statistics:  runnerScaleSetSession.Statistics,
			Body:        string(acquirableJobsJson),
		}

		return runnerScaleSetSession, initialMessage, nil
	}

	initialMessage := &actions.RunnerScaleSetMessage{
		MessageId:   0,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics:  runnerScaleSetSession.Statistics,
		Body:        "",
	}

	return runnerScaleSetSession, initialMessage, nil
}

func (m *AutoScalerClient) Close() error {
	m.logger.Info("closing.")
	return m.client.Close()
}

func (m *AutoScalerClient) GetRunnerScaleSetMessage(ctx context.Context, handler func(msg *actions.RunnerScaleSetMessage) error, maxCapacity int) error {
	if m.initialMessage != nil {
		err := handler(m.initialMessage)
		if err != nil {
			return fmt.Errorf("fail to process initial message. %w", err)
		}

		m.initialMessage = nil
		return nil
	}

	for {
		message, err := m.client.GetMessage(ctx, m.lastMessageId, maxCapacity)
		if err != nil {
			return fmt.Errorf("get message failed from refreshing client. %w", err)
		}

		if message == nil {
			continue
		}

		err = handler(message)
		if err != nil {
			return fmt.Errorf("handle message failed. %w", err)
		}

		m.lastMessageId = message.MessageId

		return m.deleteMessage(ctx, message.MessageId)
	}
}

func (m *AutoScalerClient) deleteMessage(ctx context.Context, messageId int64) error {
	err := m.client.DeleteMessage(ctx, messageId)
	if err != nil {
		return fmt.Errorf("delete message failed from refreshing client. %w", err)
	}

	m.logger.Info("deleted message.", "messageId", messageId)
	return nil
}

func (m *AutoScalerClient) AcquireJobsForRunnerScaleSet(ctx context.Context, requestIds []int64) error {
	m.logger.Info("acquiring jobs.", "request count", len(requestIds), "requestIds", fmt.Sprint(requestIds))
	if len(requestIds) == 0 {
		return nil
	}

	ids, err := m.client.AcquireJobs(ctx, requestIds)
	if err != nil {
		return fmt.Errorf("acquire jobs failed from refreshing client. %w", err)
	}

	m.logger.Info("acquired jobs.", "requested", len(requestIds), "acquired", len(ids))
	return nil
}

func getRandomDuration(minSeconds, maxSeconds int) time.Duration {
	return time.Duration(rand.Intn(maxSeconds-minSeconds)+minSeconds) * time.Second
}
