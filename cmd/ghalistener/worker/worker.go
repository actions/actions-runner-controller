package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/go-logr/logr"
)

// message types
const (
	messageTypeJobAvailable = "JobAvailable"
	messageTypeJobAssigned  = "JobAssigned"
	messageTypeJobStarted   = "JobStarted"
	messageTypeJobCompleted = "JobCompleted"
)

const kubernetesWorkerName = "kubernetesworker"

type KubernetesWorkerOption func(*KubernetesWorker)

func WithLogger(logger logr.Logger) KubernetesWorkerOption {
	return func(w *KubernetesWorker) {
		logger = logger.WithName(kubernetesWorkerName)
		w.logger = &logger
	}
}

type KubernetesWorker struct {
	logger *logr.Logger
}

func NewKubernetesWorker(options ...KubernetesWorkerOption) (*KubernetesWorker, error) {
	w := &KubernetesWorker{}

	for _, option := range options {
		option(w)
	}

	if err := w.applyDefaults(); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *KubernetesWorker) applyDefaults() error {
	if w.logger == nil {
		logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatJSON)
		if err != nil {
			return fmt.Errorf("NewLogger failed: %w", err)
		}
		logger = logger.WithName(kubernetesWorkerName)
		w.logger = &logger
	}

	return nil
}

func (w *KubernetesWorker) Do(ctx context.Context, msg *actions.RunnerScaleSetMessage) error {
	w.logger.Info("Processing message", "messageId", msg.MessageId, "messageType", msg.MessageType)
	if msg.Statistics == nil {
		return fmt.Errorf("invalid message: statistics is nil")
	}

	w.logger.Info("current runner scale set statistics.", "statistics", msg.Statistics)

	if msg.MessageType != "RunnerScaleSetJobMessages" {
		w.logger.Info("Skipping message", "messageType", msg.MessageType)
		return nil
	}

	var batchedMessages []json.RawMessage
	if err := json.Unmarshal([]byte(msg.Body), &batchedMessages); err != nil {
		return fmt.Errorf("failed to unmarshal batched messages: %w", err)
	}

	var availableJobs []int64
	for _, msg := range batchedMessages {
		var messageType actions.JobMessageType
		if err := json.Unmarshal(msg, &messageType); err != nil {
			return fmt.Errorf("failed to decode job message type: %w", err)
		}

		switch messageType.MessageType {
		case messageTypeJobAvailable:
			var jobAvailable actions.JobAvailable
			if err := json.Unmarshal(msg, &jobAvailable); err != nil {
				return fmt.Errorf("failed to decode job available: %w", err)
			}

			w.logger.Info("Job available message received", "jobId", jobAvailable.RunnerRequestId)
			availableJobs = append(availableJobs, jobAvailable.RunnerRequestId)

		case messageTypeJobAssigned:
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(msg, &jobAssigned); err != nil {
				return fmt.Errorf("failed to decode job assigned: %w", err)
			}

			w.logger.Info("Job assigned message received", "jobId", jobAssigned.RunnerRequestId)

		case messageTypeJobStarted:
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(msg, &jobStarted); err != nil {
				return fmt.Errorf("could not decode job started message. %w", err)
			}
			w.logger.Info("job started message received.", "RequestId", jobStarted.RunnerRequestId, "RunnerId", jobStarted.RunnerId)

		case messageTypeJobCompleted:
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(msg, &jobCompleted); err != nil {
				return fmt.Errorf("failed to decode job completed: %w", err)
			}

			w.logger.Info("job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result, "RunnerId", jobCompleted.RunnerId, "RunnerName", jobCompleted.RunnerName)

		default:
			w.logger.Info("unknown job message type.", "messageType", messageType.MessageType)

		}
	}

	return nil
}
