package listener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
)

const (
	sessionCreationMaxRetries = 10
)

// message types
const (
	messageTypeJobAvailable = "JobAvailable"
	messageTypeJobAssigned  = "JobAssigned"
	messageTypeJobStarted   = "JobStarted"
	messageTypeJobCompleted = "JobCompleted"
)

type Client interface {
	GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*actions.AcquirableJobList, error)
	CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*actions.RunnerScaleSetSession, error)
	GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64) (*actions.RunnerScaleSetMessage, error)
	DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error
	AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error)
	RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*actions.RunnerScaleSetSession, error)
}

type Config struct {
	Client     Client
	ScaleSetID int
	MinRunners int
	MaxRunners int
	Logger     logr.Logger
	Metrics    metrics.Publisher
}

type Listener struct {
	// configured fields
	scaleSetID int
	client     Client
	metrics    metrics.Publisher

	// internal fields
	logger   logr.Logger
	hostname string

	// updated fields
	lastMessageID int64
	session       *actions.RunnerScaleSetSession
}

func New(config Config) (*Listener, error) {
	listener := &Listener{
		scaleSetID: config.ScaleSetID,
		client:     config.Client,
		logger:     config.Logger,
		metrics:    metrics.Discard,
	}

	if config.Metrics != nil {
		listener.metrics = config.Metrics
	}

	listener.metrics.PublishStatic(config.MinRunners, config.MaxRunners)

	hostname, err := os.Hostname()
	if err != nil {
		hostname = uuid.NewString()
		listener.logger.Info("Failed to get hostname, fallback to uuid", "uuid", hostname, "error", err)
	}
	listener.hostname = hostname

	return listener, nil
}

type Handler interface {
	HandleJobStarted(ctx context.Context, jobInfo *actions.JobStarted) error
	HandleDesiredRunnerCount(ctx context.Context, desiredRunnerCount int) error
}

func (l *Listener) Listen(ctx context.Context, handler Handler) error {
	if err := l.createSession(ctx); err != nil {
		return fmt.Errorf("createSession failed: %w", err)
	}

	initialMessage := &actions.RunnerScaleSetMessage{
		MessageId:   0,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics:  l.session.Statistics,
		Body:        "",
	}

	if l.session.Statistics.TotalAvailableJobs > 0 || l.session.Statistics.TotalAssignedJobs > 0 {
		acquirableJobs, err := l.client.GetAcquirableJobs(ctx, l.scaleSetID)
		if err != nil {
			return fmt.Errorf("failed to call GetAcquirableJobs: %w", err)
		}

		acquirableJobsJson, err := json.Marshal(acquirableJobs)
		if err != nil {
			return fmt.Errorf("failed to marshal acquirable jobs: %w", err)
		}

		initialMessage.Body = string(acquirableJobsJson)
	}

	if err := handler.HandleDesiredRunnerCount(ctx, initialMessage.Statistics.TotalAssignedJobs); err != nil {
		return fmt.Errorf("handling initial message failed: %w", err)
	}

	for {
		msg, err := l.getMessage(ctx)
		if err != nil {
			return fmt.Errorf("getMessage failed: %w", err)
		}

		if msg == nil {
			continue
		}

		statistics, jobsStarted, err := l.parseMessage(ctx, msg)
		if err != nil {
			return fmt.Errorf("failed to parse message: %w", err)
		}

		l.lastMessageID = msg.MessageId

		if err := l.deleteLastMessage(ctx); err != nil {
			return fmt.Errorf("failed to delete message: %w", err)
		}

		for _, jobStarted := range jobsStarted {
			if err := handler.HandleJobStarted(ctx, jobStarted); err != nil {
				return fmt.Errorf("failed to handle job started: %w", err)
			}
		}

		if err := handler.HandleDesiredRunnerCount(ctx, statistics.TotalAssignedJobs); err != nil {
			return fmt.Errorf("failed to handle desired runner count: %w", err)
		}
	}
}

func (l *Listener) createSession(ctx context.Context) error {
	var session *actions.RunnerScaleSetSession
	var retries int

	for {
		var err error
		session, err = l.client.CreateMessageSession(ctx, l.scaleSetID, l.hostname)
		if err == nil {
			break
		}

		clientErr := &actions.HttpClientSideError{}
		if !errors.As(err, &clientErr) {
			return fmt.Errorf("failed to create session: %w", err)
		}

		if clientErr.Code != http.StatusConflict {
			return fmt.Errorf("failed to create session: %w", err)
		}

		retries++
		if retries > sessionCreationMaxRetries {
			return fmt.Errorf("failed to create session after %d retries: %w", retries, err)
		}

		l.logger.Info("Unable to create message session. Will try again in 30 seconds", "error", err.Error())

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		case <-time.After(30 * time.Second):
		}
	}

	statistics, err := json.Marshal(session.Statistics)
	if err != nil {
		return fmt.Errorf("failed to marshal statistics: %w", err)
	}
	l.logger.Info("Current runner scale set statistics.", "statistics", string(statistics))

	l.session = session

	return nil
}

func (l *Listener) getMessage(ctx context.Context) (*actions.RunnerScaleSetMessage, error) {
	l.logger.Info("Getting next message", "lastMessageID", l.lastMessageID)
	msg, err := l.client.GetMessage(ctx, l.session.MessageQueueUrl, l.session.MessageQueueAccessToken, l.lastMessageID)
	if err == nil { // if NO error
		return msg, nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return nil, fmt.Errorf("failed to get next message: %w", err)
	}

	if err := l.refreshSession(ctx); err != nil {
		return nil, err
	}

	l.logger.Info("Getting next message", "lastMessageID", l.lastMessageID)

	msg, err = l.client.GetMessage(ctx, l.session.MessageQueueUrl, l.session.MessageQueueAccessToken, l.lastMessageID)
	if err != nil { // if NO error
		return nil, fmt.Errorf("failed to get next message after message session refresh: %w", err)
	}

	return msg, nil

}

func (l *Listener) deleteLastMessage(ctx context.Context) error {
	l.logger.Info("Deleting last message", "lastMessageID", l.lastMessageID)
	if err := l.client.DeleteMessage(ctx, l.session.MessageQueueUrl, l.session.MessageQueueAccessToken, l.lastMessageID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	return nil
}

func (l *Listener) parseMessage(ctx context.Context, msg *actions.RunnerScaleSetMessage) (*actions.RunnerScaleSetStatistic, []*actions.JobStarted, error) {
	l.logger.Info("Processing message", "messageId", msg.MessageId, "messageType", msg.MessageType)
	if msg.Statistics == nil {
		return nil, nil, fmt.Errorf("invalid message: statistics is nil")
	}

	l.logger.Info("New runner scale set statistics.", "statistics", msg.Statistics)

	if msg.MessageType != "RunnerScaleSetJobMessages" {
		l.logger.Info("Skipping message", "messageType", msg.MessageType)
		return nil, nil, fmt.Errorf("invalid message type: %s", msg.MessageType)
	}

	var batchedMessages []json.RawMessage
	if len(msg.Body) > 0 {
		if err := json.Unmarshal([]byte(msg.Body), &batchedMessages); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal batched messages: %w", err)
		}
	}

	var availableJobs []int64
	var startedJobs []*actions.JobStarted
	for _, msg := range batchedMessages {
		var messageType actions.JobMessageType
		if err := json.Unmarshal(msg, &messageType); err != nil {
			return nil, nil, fmt.Errorf("failed to decode job message type: %w", err)
		}

		switch messageType.MessageType {
		case messageTypeJobAvailable:
			var jobAvailable actions.JobAvailable
			if err := json.Unmarshal(msg, &jobAvailable); err != nil {
				return nil, nil, fmt.Errorf("failed to decode job available: %w", err)
			}

			l.logger.Info("Job available message received", "jobId", jobAvailable.RunnerRequestId)
			availableJobs = append(availableJobs, jobAvailable.RunnerRequestId)

		case messageTypeJobAssigned:
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(msg, &jobAssigned); err != nil {
				return nil, nil, fmt.Errorf("failed to decode job assigned: %w", err)
			}

			l.logger.Info("Job assigned message received", "jobId", jobAssigned.RunnerRequestId)

		case messageTypeJobStarted:
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(msg, &jobStarted); err != nil {
				return nil, nil, fmt.Errorf("could not decode job started message. %w", err)
			}
			l.logger.Info("Job started message received.", "RequestId", jobStarted.RunnerRequestId, "RunnerId", jobStarted.RunnerId)
			startedJobs = append(startedJobs, &jobStarted)

		case messageTypeJobCompleted:
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(msg, &jobCompleted); err != nil {
				return nil, nil, fmt.Errorf("failed to decode job completed: %w", err)
			}

			l.logger.Info("Job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result, "RunnerId", jobCompleted.RunnerId, "RunnerName", jobCompleted.RunnerName)

		default:
			l.logger.Info("unknown job message type.", "messageType", messageType.MessageType)
		}
	}

	l.logger.Info("Available jobs.", "count", len(availableJobs), "requestIds", fmt.Sprint(availableJobs))
	if len(availableJobs) > 0 {
		acquired, err := l.acquireAvailableJobs(ctx, availableJobs)
		if err != nil {
			return nil, nil, err
		}

		l.logger.Info("Jobs are acquired", "count", len(acquired), "requestIds", fmt.Sprint(acquired))
	}

	return msg.Statistics, startedJobs, nil
}

func (l *Listener) acquireAvailableJobs(ctx context.Context, availableJobs []int64) ([]int64, error) {
	l.logger.Info("Acquiring jobs")

	ids, err := l.client.AcquireJobs(ctx, l.scaleSetID, l.session.MessageQueueAccessToken, availableJobs)
	if err == nil { // if NO errors
		return ids, nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return nil, fmt.Errorf("failed to acquire jobs: %w", err)
	}

	if err := l.refreshSession(ctx); err != nil {
		return nil, err
	}

	ids, err = l.client.AcquireJobs(ctx, l.scaleSetID, l.session.MessageQueueAccessToken, availableJobs)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire jobs after session refresh: %w", err)
	}

	return ids, nil
}

func (l *Listener) refreshSession(ctx context.Context) error {
	l.logger.Info("Message queue token is expired during GetNextMessage, refreshing...")
	session, err := l.client.RefreshMessageSession(ctx, l.session.RunnerScaleSet.Id, l.session.SessionId)
	if err != nil {
		return fmt.Errorf("refresh message session failed. %w", err)
	}

	l.session = session
	return nil
}
