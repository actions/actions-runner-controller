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

//go:generate mockery --name Client --output ./mocks --outpkg mocks --case underscore
type Client interface {
	GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*actions.AcquirableJobList, error)
	CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*actions.RunnerScaleSetSession, error)
	GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64) (*actions.RunnerScaleSetMessage, error)
	DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error
	AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error)
	RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*actions.RunnerScaleSetSession, error)
	DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error
}

type Config struct {
	Client     Client
	ScaleSetID int
	MinRunners int
	MaxRunners int
	Logger     logr.Logger
	Metrics    metrics.Publisher
}

func (c *Config) Validate() error {
	if c.Client == nil {
		return errors.New("client is required")
	}
	if c.ScaleSetID == 0 {
		return errors.New("scaleSetID is required")
	}
	if c.MinRunners < 0 {
		return errors.New("minRunners must be greater than or equal to 0")
	}
	if c.MaxRunners < 0 {
		return errors.New("maxRunners must be greater than or equal to 0")
	}
	if c.MaxRunners > 0 && c.MinRunners > c.MaxRunners {
		return errors.New("minRunners must be less than or equal to maxRunners")
	}
	return nil
}

// The Listener's role is to manage all interactions with the actions service.
// It receives messages and processes them using the given handler.
type Listener struct {
	// configured fields
	scaleSetID int               // The ID of the scale set associated with the listener.
	client     Client            // The client used to interact with the scale set.
	metrics    metrics.Publisher // The publisher used to publish metrics.

	// internal fields
	logger   logr.Logger // The logger used for logging.
	hostname string      // The hostname of the listener.

	// updated fields
	lastMessageID int64                          // The ID of the last processed message.
	session       *actions.RunnerScaleSetSession // The session for managing the runner scale set.
}

func New(config Config) (*Listener, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

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

//go:generate mockery --name Handler --output ./mocks --outpkg mocks --case underscore
type Handler interface {
	HandleJobStarted(ctx context.Context, jobInfo *actions.JobStarted) error
	HandleDesiredRunnerCount(ctx context.Context, count, jobsCompleted int) (int, error)
}

// Listen listens for incoming messages and handles them using the provided handler.
// It continuously listens for messages until the context is cancelled.
// The initial message contains the current statistics and acquirable jobs, if any.
// The handler is responsible for handling the initial message and subsequent messages.
// If an error occurs during any step, Listen returns an error.
func (l *Listener) Listen(ctx context.Context, handler Handler) error {
	if err := l.createSession(ctx); err != nil {
		return fmt.Errorf("createSession failed: %w", err)
	}

	defer func() {
		if err := l.deleteMessageSession(); err != nil {
			l.logger.Error(err, "failed to delete message session")
		}
	}()

	initialMessage := &actions.RunnerScaleSetMessage{
		MessageId:   0,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics:  l.session.Statistics,
		Body:        "",
	}

	if l.session.Statistics == nil {
		return fmt.Errorf("session statistics is nil")
	}
	l.metrics.PublishStatistics(initialMessage.Statistics)

	desiredRunners, err := handler.HandleDesiredRunnerCount(ctx, initialMessage.Statistics.TotalAssignedJobs, 0)
	if err != nil {
		return fmt.Errorf("handling initial message failed: %w", err)
	}
	l.metrics.PublishDesiredRunners(desiredRunners)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := l.getMessage(ctx)
		if err != nil {
			return fmt.Errorf("failed to get message: %w", err)
		}

		if msg == nil {
			continue
		}

		// New context is created to avoid cancelation during message handling.
		if err := l.handleMessage(context.Background(), handler, msg); err != nil {
			return fmt.Errorf("failed to handle message: %w", err)
		}
	}
}

func (l *Listener) handleMessage(ctx context.Context, handler Handler, msg *actions.RunnerScaleSetMessage) error {
	parsedMsg, err := l.parseMessage(ctx, msg)
	if err != nil {
		return fmt.Errorf("failed to parse message: %w", err)
	}
	l.metrics.PublishStatistics(parsedMsg.statistics)

	if len(parsedMsg.jobsAvailable) > 0 {
		acquiredJobIDs, err := l.acquireAvailableJobs(ctx, parsedMsg.jobsAvailable)
		if err != nil {
			return fmt.Errorf("failed to acquire jobs: %w", err)
		}

		l.logger.Info("Jobs are acquired", "count", len(acquiredJobIDs), "requestIds", fmt.Sprint(acquiredJobIDs))
	}

	for _, jobCompleted := range parsedMsg.jobsCompleted {
		l.metrics.PublishJobCompleted(jobCompleted)
	}

	l.lastMessageID = msg.MessageId

	if err := l.deleteLastMessage(ctx); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	for _, jobStarted := range parsedMsg.jobsStarted {
		if err := handler.HandleJobStarted(ctx, jobStarted); err != nil {
			return fmt.Errorf("failed to handle job started: %w", err)
		}
		l.metrics.PublishJobStarted(jobStarted)
	}

	desiredRunners, err := handler.HandleDesiredRunnerCount(ctx, parsedMsg.statistics.TotalAssignedJobs, len(parsedMsg.jobsCompleted))
	if err != nil {
		return fmt.Errorf("failed to handle desired runner count: %w", err)
	}
	l.metrics.PublishDesiredRunners(desiredRunners)
	return nil
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
		if retries >= sessionCreationMaxRetries {
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

type parsedMessage struct {
	statistics    *actions.RunnerScaleSetStatistic
	jobsStarted   []*actions.JobStarted
	jobsAvailable []*actions.JobAvailable
	jobsCompleted []*actions.JobCompleted
}

func (l *Listener) parseMessage(ctx context.Context, msg *actions.RunnerScaleSetMessage) (*parsedMessage, error) {
	if msg.MessageType != "RunnerScaleSetJobMessages" {
		l.logger.Info("Skipping message", "messageType", msg.MessageType)
		return nil, fmt.Errorf("invalid message type: %s", msg.MessageType)
	}

	l.logger.Info("Processing message", "messageId", msg.MessageId, "messageType", msg.MessageType)
	if msg.Statistics == nil {
		return nil, fmt.Errorf("invalid message: statistics is nil")
	}

	l.logger.Info("New runner scale set statistics.", "statistics", msg.Statistics)

	var batchedMessages []json.RawMessage
	if len(msg.Body) > 0 {
		if err := json.Unmarshal([]byte(msg.Body), &batchedMessages); err != nil {
			return nil, fmt.Errorf("failed to unmarshal batched messages: %w", err)
		}
	}

	parsedMsg := &parsedMessage{
		statistics: msg.Statistics,
	}

	for _, msg := range batchedMessages {
		var messageType actions.JobMessageType
		if err := json.Unmarshal(msg, &messageType); err != nil {
			return nil, fmt.Errorf("failed to decode job message type: %w", err)
		}

		switch messageType.MessageType {
		case messageTypeJobAvailable:
			var jobAvailable actions.JobAvailable
			if err := json.Unmarshal(msg, &jobAvailable); err != nil {
				return nil, fmt.Errorf("failed to decode job available: %w", err)
			}

			l.logger.Info("Job available message received", "jobId", jobAvailable.RunnerRequestId)
			parsedMsg.jobsAvailable = append(parsedMsg.jobsAvailable, &jobAvailable)

		case messageTypeJobAssigned:
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(msg, &jobAssigned); err != nil {
				return nil, fmt.Errorf("failed to decode job assigned: %w", err)
			}

			l.logger.Info("Job assigned message received", "jobId", jobAssigned.RunnerRequestId)

		case messageTypeJobStarted:
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(msg, &jobStarted); err != nil {
				return nil, fmt.Errorf("could not decode job started message. %w", err)
			}
			l.logger.Info("Job started message received.", "RequestId", jobStarted.RunnerRequestId, "RunnerId", jobStarted.RunnerId)
			parsedMsg.jobsStarted = append(parsedMsg.jobsStarted, &jobStarted)

		case messageTypeJobCompleted:
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(msg, &jobCompleted); err != nil {
				return nil, fmt.Errorf("failed to decode job completed: %w", err)
			}

			l.logger.Info("Job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result, "RunnerId", jobCompleted.RunnerId, "RunnerName", jobCompleted.RunnerName)
			parsedMsg.jobsCompleted = append(parsedMsg.jobsCompleted, &jobCompleted)

		default:
			l.logger.Info("unknown job message type.", "messageType", messageType.MessageType)
		}
	}

	return parsedMsg, nil
}

func (l *Listener) acquireAvailableJobs(ctx context.Context, jobsAvailable []*actions.JobAvailable) ([]int64, error) {
	ids := make([]int64, 0, len(jobsAvailable))
	for _, job := range jobsAvailable {
		ids = append(ids, job.RunnerRequestId)
	}

	l.logger.Info("Acquiring jobs", "count", len(ids), "requestIds", fmt.Sprint(ids))

	idsAcquired, err := l.client.AcquireJobs(ctx, l.scaleSetID, l.session.MessageQueueAccessToken, ids)
	if err == nil { // if NO errors
		return idsAcquired, nil
	}

	expiredError := &actions.MessageQueueTokenExpiredError{}
	if !errors.As(err, &expiredError) {
		return nil, fmt.Errorf("failed to acquire jobs: %w", err)
	}

	if err := l.refreshSession(ctx); err != nil {
		return nil, err
	}

	idsAcquired, err = l.client.AcquireJobs(ctx, l.scaleSetID, l.session.MessageQueueAccessToken, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire jobs after session refresh: %w", err)
	}

	return idsAcquired, nil
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

func (l *Listener) deleteMessageSession() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	l.logger.Info("Deleting message session")

	if err := l.client.DeleteMessageSession(ctx, l.session.RunnerScaleSet.Id, l.session.SessionId); err != nil {
		return fmt.Errorf("failed to delete message session: %w", err)
	}

	return nil
}
