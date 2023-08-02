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

type Option func(*Listener)

func WithLogger(logger logr.Logger) Option {
	return func(h *Listener) {
		logger = logger.WithName("actionhandler")
		h.logger = &logger
	}
}

type HandlerFunc func(ctx context.Context, msg *actions.RunnerScaleSetMessage) error

type Listener struct {
	scaleSetID int
	client     *actions.Client
	handle     HandlerFunc
	logger     *logr.Logger

	hostname string
}

func New(client *actions.Client, handle HandlerFunc, scaleSetID int, options ...Option) (*Listener, error) {
	listener := &Listener{
		scaleSetID: scaleSetID,
		handle:     handle,
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

func (h *Listener) Listen(ctx context.Context) error {
	sessionMsg, err := h.createSession(ctx)
	if err != nil {
		return fmt.Errorf("createSession failed: %w", err)
	}

	initialMessage := &actions.RunnerScaleSetMessage{
		MessageId:   0,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics:  sessionMsg.Statistics,
		Body:        "",
	}

	if sessionMsg.Statistics.TotalAvailableJobs > 0 || sessionMsg.Statistics.TotalAssignedJobs > 0 {
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

	if err := h.handle(ctx, initialMessage); err != nil {
		return fmt.Errorf("handling initial message failed: %w", err)
	}

	return nil
}

func (h *Listener) createSession(ctx context.Context) (*actions.RunnerScaleSetSession, error) {
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
			return nil, fmt.Errorf("failed to create session: %w", err)
		}

		if clientErr.Code != http.StatusConflict {
			return nil, fmt.Errorf("failed to create session: %w", err)
		}

		retries++
		if retries > sessionCreationMaxRetries {
			return nil, fmt.Errorf("failed to create session after %d retries: %w", retries, err)
		}

		h.logger.Info("unable to create message session. Will try again in 30 seconds", "error", err.Error())

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		case <-time.After(30 * time.Second):
		}
	}

	statistics, err := json.Marshal(session.Statistics)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal statistics: %w", err)
	}
	h.logger.Info("current runner scale set statistics.", "statistics", string(statistics))

	return session, nil
}
