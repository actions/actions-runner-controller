package scalesetclient

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/github/runnermanager"
	"github.com/go-logr/logr"
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

type JobMessageType struct {
	MessageType string `json:"messageType"`
}

type JobMessageBase struct {
	JobMessageType
	RunnerRequestId int64    `json:"runnerRequestId"`
	RepositoryName  string   `json:"repositoryName"`
	OwnerName       string   `json:"ownerName"`
	JobWorkflowRef  string   `json:"jobWorkflowRef"`
	EventName       string   `json:"eventName"`
	RequestLabels   []string `json:"requestLabels"`
}

func MaybeAcquireJob(ctx context.Context, logger logr.Logger, client *github.ActionsClient, session *github.RunnerScaleSetSession, message *github.RunnerScaleSetMessage) {
	var jobAvailable JobAvailable
	if err := json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobAvailable); err != nil {
		logger.Error(err, "Error: Decode RunnerScaleSetJobAvailable message body failed.")
		return
	}

	logger.Info("Runner scale set job available message received.", "messageId", message.MessageId, "RequestId", jobAvailable.RunnerRequestId)

	if err := client.AcquireJob(ctx, jobAvailable.AcquireJobUrl, session.MessageQueueAccessToken); err != nil {
		logger.Error(err, "Error: Acquire job failed.")
		return
	}

	logger.Info("Tried to acquire job.", "RequestId", jobAvailable.RunnerRequestId)
}

func HandleJobAssignment(ctx context.Context, logger logr.Logger, client *github.ActionsClient, runnerScaleSet *github.RunnerScaleSet, message *github.RunnerScaleSetMessage) {
	var jobAssigned JobAssigned

	if err := json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobAssigned); err != nil {
		logger.Error(err, "Error: Decode RunnerScaleSetJobAssigned message body failed.")
		return
	}

	logger.Info("Runner scale set job assigned message received.", "messageId", message.MessageId, "RequestId", jobAssigned.RunnerRequestId, "JitConfigUrl", runnerScaleSet.RunnerJitConfigUrl)

	jitConfig, err := client.GenerateJitRunnerConfig(ctx, &github.RunnerScaleSetJitRunnerSetting{WorkFolder: "__work"}, runnerScaleSet.RunnerJitConfigUrl)
	if err != nil {
		logger.Error(err, "Error: Generate JIT runner config failed.")
		return
	}

	logger.Info("Generated JIT runner config.", "RequestId", jobAssigned.RunnerRequestId, "RunnerId", jitConfig.Runner.Id, "JitConfig", jitConfig.EncodedJITConfig)

	runnerJob, err := runnermanager.CreateJob(ctx, jitConfig, runnerScaleSet.Name)
	if err != nil {
		// TODO: Need to handle this.
		logger.Error(err, "Error: Could not create job.")
		return
	}

	logger.Info("Started a job", "job", runnerJob.Name)
}

func NoopHandleJobCompletion(logger logr.Logger, message *github.RunnerScaleSetMessage) {
	var jobCompleted JobCompleted

	if err := json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobCompleted); err != nil {
		logger.Error(err, "Error: Decode RunnerScaleSetJobCompleted message body failed.")
		return
	}

	logger.Info("Runner scale set job completed message received.", "messageId", message.MessageId, "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result)
}

func HandleBatchedRunnerScaleSetMessages(ctx context.Context, logger logr.Logger, namespace, deploymentName string, client *github.ActionsClient, session *github.RunnerScaleSetSession, message *github.RunnerScaleSetMessage) {
	var batchedMessages []json.RawMessage

	if err := json.NewDecoder(strings.NewReader(message.Body)).Decode(&batchedMessages); err != nil {
		logger.Error(err, "Error: Decode RunnerScaleSetJobMessages message body failed.")
		return
	}

	logger.Info("Runner scale set batched job message received.", "messageId", message.MessageId, "BatchCount", len(batchedMessages))

	for _, message := range batchedMessages {
		var messageType JobMessageType
		if err := json.Unmarshal(message, &messageType); err != nil {
			logger.Error(err, "Error: Unmarshal RunnerScaleSetMessageType failed.")
			return
		}

		switch messageType.MessageType {
		case "JobAvailable":
			var jobAvailable JobAvailable
			if err := json.Unmarshal(message, &jobAvailable); err != nil {
				logger.Error(err, "Error: Unmarshal RunnerScaleSetJobAvailable failed.")
				return
			}
			AcquireJob(ctx, logger, client, session, &jobAvailable)
		case "JobAssigned":
			var jobAssigned JobAssigned
			if err := json.Unmarshal(message, &jobAssigned); err != nil {
				logger.Error(err, "Error: Unmarshal RunnerScaleSetJobAssigned failed.")
				return
			}
			logger.Info("Runner scale set job assigned message received.", "RequestId", jobAssigned.RunnerRequestId)
		case "JobCompleted":
			var jobCompleted JobCompleted
			if err := json.Unmarshal(message, &jobCompleted); err != nil {
				logger.Error(err, "Error: Unmarshal RunnerScaleSetJobCompleted failed.")
				return
			}
			logger.Info("Runner scale set job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result)
		default:
			logger.Info("Unknown message type.", "messageType", messageType.MessageType)
		}
	}

	if message.Statistics.TotalAssignedJobs > 0 {
		logger.Info("Need to patched runner deployment.", "patch replicas", message.Statistics.TotalAssignedJobs)
		// if patched, err := runnermanager.PatchRunnerDeployment(ctx, namespace, deploymentName, &message.Statistics.TotalAssignedJobs); err != nil {
		// 	logger.Error(err, "Error: Patch runner deployment failed.")
		// 	return
		// } else {
		// 	logger.Info("Patched runner deployment.", "patched replicas", patched.Spec.Replicas)
		// }
	}
}

func AcquireJob(ctx context.Context, logger logr.Logger, client *github.ActionsClient, session *github.RunnerScaleSetSession, jobAvailable *JobAvailable) {
	logger.Info("Runner scale set job available message received.", "RequestId", jobAvailable.RunnerRequestId)

	if err := client.AcquireJob(ctx, jobAvailable.AcquireJobUrl, session.MessageQueueAccessToken); err != nil {
		logger.Error(err, "Error: Acquire job failed.")
		return
	}

	logger.Info("Tried to acquire job.", "RequestId", jobAvailable.RunnerRequestId)
}
