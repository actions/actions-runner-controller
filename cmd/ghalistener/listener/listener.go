package listener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
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

type Option func(*Listener)

func WithLogger(logger logr.Logger) Option {
	return func(h *Listener) {
		logger = logger.WithName("actionhandler")
		h.logger = &logger
	}
}

type Listener struct {
	// configured fields
	scaleSetID int
	client     *actions.Client

	// internal fields
	logger   *logr.Logger
	hostname string

	// updated fields
	lastMessageID int64
	session       *actions.RunnerScaleSetSession
}

func New(client *actions.Client, scaleSetID int, options ...Option) (*Listener, error) {
	listener := &Listener{
		scaleSetID: scaleSetID,
		client:     client,
	}

	for _, option := range options {
		option(listener)
	}

	if err := listener.applyDefaults(); err != nil {
		return nil, err
	}

	return listener, nil
}

func (h *Listener) applyDefaults() error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = uuid.NewString()
		h.logger.Info("Failed to get hostname, fallback to uuid", "uuid", hostname, "error", err)
	}
	h.hostname = hostname

	if h.logger == nil {
		logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatJSON)
		if err != nil {
			return fmt.Errorf("NewLogger failed: %w", err)
		}
		h.logger = &logger
	}

	return nil
}

type Handler interface {
	HandleJobStarted(ctx context.Context, jobInfo *actions.JobStarted) error
	HandleDesiredRunnerCount(ctx context.Context, desiredRunnerCount int) error
}

func (h *Listener) Listen(ctx context.Context, handler Handler) error {
	if err := h.createSession(ctx); err != nil {
		return fmt.Errorf("createSession failed: %w", err)
	}

	initialMessage := &actions.RunnerScaleSetMessage{
		MessageId:   0,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics:  h.session.Statistics,
		Body:        "",
	}

	if h.session.Statistics.TotalAvailableJobs > 0 || h.session.Statistics.TotalAssignedJobs > 0 {
		acquirableJobs, err := h.client.GetAcquirableJobs(ctx, h.scaleSetID)
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
		msg, err := h.getMessage(ctx)
		if err != nil {
			return fmt.Errorf("getMessage failed: %w", err)
		}

		statistics, jobsStarted, err := h.parseMessage(ctx, msg)
		if err != nil {
			return fmt.Errorf("failed to parse message: %w", err)
		}

		h.lastMessageID = msg.MessageId

		if err := h.deleteLastMessage(ctx); err != nil {
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

func (h *Listener) createSession(ctx context.Context) error {
	var session *actions.RunnerScaleSetSession
	var retries int

	for {
		var err error
		session, err = h.client.CreateMessageSession(ctx, h.scaleSetID, h.hostname)
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

		h.logger.Info("Unable to create message session. Will try again in 30 seconds", "error", err.Error())

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
	h.logger.Info("Current runner scale set statistics.", "statistics", string(statistics))

	h.session = session
	return nil
}

func (h *Listener) getMessage(ctx context.Context) (*actions.RunnerScaleSetMessage, error) {
	h.logger.Info("Getting next message", "lastMessageID", h.lastMessageID)
	msg, err := h.client.GetMessage(ctx, h.session.MessageQueueUrl, h.session.MessageQueueAccessToken, h.lastMessageID)
	if err == nil { // if NO error
		return msg, nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return nil, fmt.Errorf("get message failed. %w", err)
	}

	h.logger.Info("Message queue token is expired during GetNextMessage, refreshing...")
	session, err := h.client.RefreshMessageSession(ctx, h.session.RunnerScaleSet.Id, h.session.SessionId)
	if err != nil {
		return nil, fmt.Errorf("refresh message session failed. %w", err)
	}

	h.session = session

	h.logger.Info("Getting next message", "lastMessageID", h.lastMessageID)
	msg, err = h.client.GetMessage(ctx, h.session.MessageQueueUrl, h.session.MessageQueueAccessToken, h.lastMessageID)
	if err == nil { // if NO error
		return msg, nil
	}

	return nil, fmt.Errorf("failed to get next message after message session refresh: %w", err)
}

func (h *Listener) deleteLastMessage(ctx context.Context) error {
	h.logger.Info("Deleting last message", "lastMessageID", h.lastMessageID)
	if err := h.client.DeleteMessage(ctx, h.session.MessageQueueUrl, h.session.MessageQueueAccessToken, h.lastMessageID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	return nil
}

func (h *Listener) parseMessage(ctx context.Context, msg *actions.RunnerScaleSetMessage) (*actions.RunnerScaleSetStatistic, []*actions.JobStarted, error) {
	h.logger.Info("Processing message", "messageId", msg.MessageId, "messageType", msg.MessageType)
	if msg.Statistics == nil {
		return nil, nil, fmt.Errorf("invalid message: statistics is nil")
	}

	h.logger.Info("New runner scale set statistics.", "statistics", msg.Statistics)

	if msg.MessageType != "RunnerScaleSetJobMessages" {
		h.logger.Info("Skipping message", "messageType", msg.MessageType)
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

			h.logger.Info("Job available message received", "jobId", jobAvailable.RunnerRequestId)
			availableJobs = append(availableJobs, jobAvailable.RunnerRequestId)

		case messageTypeJobAssigned:
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(msg, &jobAssigned); err != nil {
				return nil, nil, fmt.Errorf("failed to decode job assigned: %w", err)
			}

			h.logger.Info("Job assigned message received", "jobId", jobAssigned.RunnerRequestId)

		case messageTypeJobStarted:
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(msg, &jobStarted); err != nil {
				return nil, nil, fmt.Errorf("could not decode job started message. %w", err)
			}
			h.logger.Info("Job started message received.", "RequestId", jobStarted.RunnerRequestId, "RunnerId", jobStarted.RunnerId)
			startedJobs = append(startedJobs, &jobStarted)

		case messageTypeJobCompleted:
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(msg, &jobCompleted); err != nil {
				return nil, nil, fmt.Errorf("failed to decode job completed: %w", err)
			}

			h.logger.Info("Job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result, "RunnerId", jobCompleted.RunnerId, "RunnerName", jobCompleted.RunnerName)

		default:
			h.logger.Info("unknown job message type.", "messageType", messageType.MessageType)
		}
	}

	h.logger.Info("Available jobs.", "count", len(availableJobs), "requestIds", fmt.Sprint(availableJobs))
	if len(availableJobs) > 0 {
		h.logger.Info("Acquiring jobs")

		ids, err := h.client.AcquireJobs(ctx, h.scaleSetID, h.session.MessageQueueAccessToken, availableJobs)
		if err != nil {
			return nil, nil, fmt.Errorf("acquire jobs failed from refreshing client. %w", err)
		}

		h.logger.Info("Jobs are acquired", "count", len(ids), "requestIds", fmt.Sprint(ids))
	}

	return msg.Statistics, startedJobs, nil
}
