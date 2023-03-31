package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
)

type ScaleSettings struct {
	Namespace    string
	ResourceName string
	MinRunners   int
	MaxRunners   int
}

type Service struct {
	ctx                context.Context
	logger             logr.Logger
	rsClient           RunnerScaleSetClient
	kubeManager        KubernetesManager
	settings           *ScaleSettings
	currentRunnerCount int
	prometheusLabels   prometheus.Labels
}

func NewService(
	ctx context.Context,
	rsClient RunnerScaleSetClient,
	manager KubernetesManager,
	settings *ScaleSettings,
	options ...func(*Service),
) *Service {
	s := &Service{
		ctx:                ctx,
		rsClient:           rsClient,
		kubeManager:        manager,
		settings:           settings,
		currentRunnerCount: 0,
		logger:             logr.FromContextOrDiscard(ctx),
	}

	for _, option := range options {
		option(s)
	}

	return s
}

func (s *Service) Start() error {
	if s.settings.MinRunners > 0 {
		s.logger.Info("scale to match minimal runners.")
		err := s.scaleForAssignedJobCount(0)
		if err != nil {
			return fmt.Errorf("could not scale to match minimal runners. %w", err)
		}
	}

	for {
		s.logger.Info("waiting for message...")
		select {
		case <-s.ctx.Done():
			s.logger.Info("service is stopped.")
			return nil
		default:
			err := s.rsClient.GetRunnerScaleSetMessage(s.ctx, s.processMessage)
			if err != nil {
				return fmt.Errorf("could not get and process message. %w", err)
			}
		}
	}
}

func (s *Service) exportStatisticsMetrics(statistics *actions.RunnerScaleSetStatistic) {
	// Export metrics
	if len(s.prometheusLabels) > 0 {
		githubRunnerScaleSetAvailableJobs.With(s.prometheusLabels).Set(float64(statistics.TotalAvailableJobs))
		githubRunnerScaleSetAcquiredJobs.With(s.prometheusLabels).Set(float64(statistics.TotalAcquiredJobs))
		githubRunnerScaleSetAssignedJobs.With(s.prometheusLabels).Set(float64(statistics.TotalAssignedJobs))
		githubRunnerScaleSetRunningJobs.With(s.prometheusLabels).Set(float64(statistics.TotalRunningJobs))
		githubRunnerScaleSetRegisteredRunners.With(s.prometheusLabels).Set(float64(statistics.TotalRegisteredRunners))
		githubRunnerScaleSetBusyRunners.With(s.prometheusLabels).Set(float64(statistics.TotalBusyRunners))
		githubRunnerScaleSetIdleRunners.With(s.prometheusLabels).Set(float64(statistics.TotalIdleRunners))
	}
}

func (s *Service) exportJobAvailableMetrics(jobAvailableMessage *actions.JobAvailable, labels *prometheus.Labels) {
	// Export metrics
	if len(s.prometheusLabels) > 0 {
		// increase total available jobs counter
		githubRunnerScaleSetJobAvailableTotal.With(*labels).Inc()
	}
}

func (s *Service) exportJobAssignedMetrics(jobAssignedMessage *actions.JobAssigned, labels *prometheus.Labels) {
	// Export metrics
	if len(s.prometheusLabels) > 0 {
		// increase total assigned jobs counter
		githubRunnerScaleSetJobAssignedTotal.With(*labels).Inc()

		// observe total queue duration
		// githubRunnerScaleSetJobQueueDurationSeconds.With(*labels).Observe(((*jobAssignedMessage.ScaleSetAssignTime).Sub(*jobAssignedMessage.QueueTime).Seconds()))
	}
}

func (s *Service) exportJobStartedMetrics(jobStartedMessage *actions.JobStarted, labels *prometheus.Labels) {
	// Export metrics
	if len(s.prometheusLabels) > 0 {
		// increase total running jobs counter
		githubRunnerScaleSetJobStartedTotal.With(*labels).Inc()

		// observe total start duration
		// githubRunnerScaleSetJobStartDurationSeconds.With(*labels).Observe(((*jobStartedMessage.RunnerAssignTime).Sub(*jobStartedMessage.ScaleSetAssignTime).Seconds()))
	}
}

func (s *Service) exportJobCompletedMetrics(jobCompletedMessage *actions.JobCompleted, labels *prometheus.Labels) {
	// Export metrics
	if len(s.prometheusLabels) > 0 {
		// increase total completed jobs counter
		githubRunnerScaleSetJobCompletedTotal.With(*labels).Inc()

		// observe total job duration
		// githubRunnerScaleSetJobRunDurationSeconds.With(*labels).Observe(((*jobCompletedMessage.FinishTime).Sub(*jobCompletedMessage.RunnerAssignTime).Seconds()))
	}
}

func getPrometheusLabelsFromJobAvailableMessage(jobAvailableMessage *actions.JobAvailable) *prometheus.Labels {
	return getPrometheusLabelsFromJobMessage(&jobAvailableMessage.JobMessageBase)
}

func getPrometheusLabelsFromJobAssignedMessage(jobAssignedMessage *actions.JobAssigned) *prometheus.Labels {
	return getPrometheusLabelsFromJobMessage(&jobAssignedMessage.JobMessageBase)
}

func getPrometheusLabelsFromJobStartedMessage(jobStartedMessage *actions.JobStarted) *prometheus.Labels {
	labels := getPrometheusLabelsFromJobMessage(&jobStartedMessage.JobMessageBase)
	(*labels)["runner_id"] = fmt.Sprintf("%d", jobStartedMessage.RunnerId)
	(*labels)["runner_name"] = jobStartedMessage.RunnerName
	return labels
}

func getPrometheusLabelsFromJobCompletedMessage(jobCompletedMessage *actions.JobCompleted) *prometheus.Labels {
	labels := getPrometheusLabelsFromJobMessage(&jobCompletedMessage.JobMessageBase)
	(*labels)["runner_id"] = fmt.Sprintf("%d", jobCompletedMessage.RunnerId)
	(*labels)["runner_name"] = jobCompletedMessage.RunnerName
	(*labels)["job_result"] = jobCompletedMessage.Result
	return labels
}

func getPrometheusLabelsFromJobMessage(jobMessage *actions.JobMessageBase) *prometheus.Labels {
	labels := make(prometheus.Labels)
	// labels["request_id"] = fmt.Sprintf("%d", jobMessage.RunnerRequestId)
	labels["owner_name"] = jobMessage.OwnerName
	labels["repository_name"] = jobMessage.RepositoryName
	// labels["workflow_run_id"] = fmt.Sprintf("%d", jobMessage.WorkflowRunId)
	labels["job_name"] = jobMessage.JobDisplayName
	labels["job_workflow_ref"] = jobMessage.JobWorkflowRef
	labels["event_name"] = jobMessage.EventName
	// if jobMessage.QueueTime != nil {
	// 	labels["queue_time"] = jobMessage.QueueTime.UTC().Format(time.RFC3339)
	// }
	// if jobMessage.ScaleSetAssignTime != nil {
	// 	labels["scale_set_assign_time"] = jobMessage.ScaleSetAssignTime.UTC().Format(time.RFC3339)
	// }
	// if jobMessage.RunnerAssignTime != nil {
	// 	labels["runner_assign_time"] = jobMessage.RunnerAssignTime.UTC().Format(time.RFC3339)
	// }
	// if jobMessage.FinishTime != nil {
	// 	labels["finish_time"] = jobMessage.FinishTime.UTC().Format(time.RFC3339)
	// }

	return &labels
}

func (s *Service) processMessage(message *actions.RunnerScaleSetMessage) error {
	s.logger.Info("process message.", "messageId", message.MessageId, "messageType", message.MessageType)
	if message.Statistics == nil {
		return fmt.Errorf("can't process message with empty statistics")
	}

	s.logger.Info("current runner scale set statistics.",
		"available jobs", message.Statistics.TotalAvailableJobs,
		"acquired jobs", message.Statistics.TotalAcquiredJobs,
		"assigned jobs", message.Statistics.TotalAssignedJobs,
		"running jobs", message.Statistics.TotalRunningJobs,
		"registered runners", message.Statistics.TotalRegisteredRunners,
		"busy runners", message.Statistics.TotalBusyRunners,
		"idle runners", message.Statistics.TotalIdleRunners)

	s.exportStatisticsMetrics(message.Statistics)

	if message.MessageType != "RunnerScaleSetJobMessages" {
		s.logger.Info("skip message with unknown message type.", "messageType", message.MessageType)
		return nil
	}

	var batchedMessages []json.RawMessage
	if err := json.NewDecoder(strings.NewReader(message.Body)).Decode(&batchedMessages); err != nil {
		return fmt.Errorf("could not decode job messages. %w", err)
	}

	s.logger.Info("process batched runner scale set job messages.", "messageId", message.MessageId, "batchSize", len(batchedMessages))

	var availableJobs []int64
	for _, message := range batchedMessages {
		var messageType actions.JobMessageType
		if err := json.Unmarshal(message, &messageType); err != nil {
			return fmt.Errorf("could not decode job message type. %w", err)
		}

		// jobLabels := new(prometheus.Labels)
		switch messageType.MessageType {
		case "JobAvailable":
			var jobAvailable actions.JobAvailable
			if err := json.Unmarshal(message, &jobAvailable); err != nil {
				return fmt.Errorf("could not decode job available message. %w", err)
			}
			s.logger.Info("job available message received.", "RequestId", jobAvailable.RunnerRequestId)
			jobLabels := getPrometheusLabelsFromJobAvailableMessage(&jobAvailable)
			s.exportJobAvailableMetrics(&jobAvailable, jobLabels)
			availableJobs = append(availableJobs, jobAvailable.RunnerRequestId)
		case "JobAssigned":
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(message, &jobAssigned); err != nil {
				return fmt.Errorf("could not decode job assigned message. %w", err)
			}
			s.logger.Info("job assigned message received.", "RequestId", jobAssigned.RunnerRequestId)
			jobLabels := getPrometheusLabelsFromJobAssignedMessage(&jobAssigned)
			s.exportJobAssignedMetrics(&jobAssigned, jobLabels)
		case "JobStarted":
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(message, &jobStarted); err != nil {
				return fmt.Errorf("could not decode job started message. %w", err)
			}
			s.logger.Info("job started message received.", "RequestId", jobStarted.RunnerRequestId, "RunnerId", jobStarted.RunnerId)
			jobLabels := getPrometheusLabelsFromJobStartedMessage(&jobStarted)
			s.exportJobStartedMetrics(&jobStarted, jobLabels)
			s.updateJobInfoForRunner(jobStarted)
		case "JobCompleted":
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(message, &jobCompleted); err != nil {
				return fmt.Errorf("could not decode job completed message. %w", err)
			}
			s.logger.Info("job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result, "RunnerId", jobCompleted.RunnerId, "RunnerName", jobCompleted.RunnerName)
			jobLabels := getPrometheusLabelsFromJobCompletedMessage(&jobCompleted)
			s.exportJobCompletedMetrics(&jobCompleted, jobLabels)
		default:
			s.logger.Info("unknown job message type.", "messageType", messageType.MessageType)
		}
	}

	err := s.rsClient.AcquireJobsForRunnerScaleSet(s.ctx, availableJobs)
	if err != nil {
		return fmt.Errorf("could not acquire jobs. %w", err)
	}
	// export metrics for acquired jobs
	if len(s.prometheusLabels) > 0 {
		githubRunnerScaleSetAcquireJobTotal.With(s.prometheusLabels).Add(float64(len(availableJobs)))
	}

	return s.scaleForAssignedJobCount(message.Statistics.TotalAssignedJobs)
}

func (s *Service) scaleForAssignedJobCount(count int) error {
	targetRunnerCount := int(math.Max(math.Min(float64(s.settings.MaxRunners), float64(count)), float64(s.settings.MinRunners)))
	if targetRunnerCount != s.currentRunnerCount {
		s.logger.Info("try scale runner request up/down base on assigned job count",
			"assigned job", count,
			"decision", targetRunnerCount,
			"min", s.settings.MinRunners,
			"max", s.settings.MaxRunners,
			"currentRunnerCount", s.currentRunnerCount)
		err := s.kubeManager.ScaleEphemeralRunnerSet(s.ctx, s.settings.Namespace, s.settings.ResourceName, targetRunnerCount)
		if err != nil {
			return fmt.Errorf("could not scale ephemeral runner set (%s/%s). %w", s.settings.Namespace, s.settings.ResourceName, err)
		}

		s.currentRunnerCount = targetRunnerCount
		//export metrics about current runner count
		if len(s.prometheusLabels) > 0 {
			githubRunnerScaleSetDesiredEphemeralRunnerPods.With(s.prometheusLabels).Set(float64(targetRunnerCount))
		}
	}

	return nil
}

// updateJobInfoForRunner updates the ephemeral runner with the job info and this is best effort since the info is only for better telemetry
func (s *Service) updateJobInfoForRunner(jobInfo actions.JobStarted) {
	s.logger.Info("update job info for runner",
		"runnerName", jobInfo.RunnerName,
		"ownerName", jobInfo.OwnerName,
		"repoName", jobInfo.RepositoryName,
		"workflowRef", jobInfo.JobWorkflowRef,
		"workflowRunId", jobInfo.WorkflowRunId,
		"jobDisplayName", jobInfo.JobDisplayName,
		"requestId", jobInfo.RunnerRequestId)
	err := s.kubeManager.UpdateEphemeralRunnerWithJobInfo(s.ctx, s.settings.Namespace, jobInfo.RunnerName, jobInfo.OwnerName, jobInfo.RepositoryName, jobInfo.JobWorkflowRef, jobInfo.JobDisplayName, jobInfo.WorkflowRunId, jobInfo.RunnerRequestId)
	if err != nil {
		s.logger.Error(err, "could not update ephemeral runner with job info", "runnerName", jobInfo.RunnerName, "requestId", jobInfo.RunnerRequestId)
	}
}
