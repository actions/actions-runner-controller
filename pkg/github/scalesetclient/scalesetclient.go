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

type JobMessageBase struct {
	MessageType     string   `json:"messageType"`
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

	createdJob, err := runnermanager.CreateJob(ctx, jitConfig, runnerScaleSet.Name)
	if err != nil {
		// TODO: Need to handle this.
		logger.Error(err, "Error: Could not create job.")
		return
	}

	logger.Info("Started a job", "job", createdJob)
}

func NoopHandleJobCompletion(logger logr.Logger, message *github.RunnerScaleSetMessage) {
	var jobCompleted JobCompleted

	if err := json.NewDecoder(strings.NewReader(message.Body)).Decode(&jobCompleted); err != nil {
		logger.Error(err, "Error: Decode RunnerScaleSetJobCompleted message body failed.")
		return
	}

	logger.Info("Runner scale set job completed message received.", "messageId", message.MessageId, "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result)
}
