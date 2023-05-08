package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/go-logr/logr"
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
	metricsExporter    metricsExporter
	errs               []error
}

func WithPrometheusMetrics(conf RunnerScaleSetListenerConfig) func(*Service) {
	return func(svc *Service) {
		parsedURL, err := actions.ParseGitHubConfigFromURL(conf.ConfigureUrl)
		if err != nil {
			svc.errs = append(svc.errs, err)
		}

		svc.metricsExporter.withBaseLabels(baseLabels{
			scaleSetName:      conf.EphemeralRunnerSetName,
			scaleSetNamespace: conf.EphemeralRunnerSetNamespace,
			enterprise:        parsedURL.Enterprise,
			organization:      parsedURL.Organization,
			repository:        parsedURL.Repository,
		})
	}
}

func WithLogger(logger logr.Logger) func(*Service) {
	return func(s *Service) {
		s.logger = logger.WithName("service")
	}
}

func NewService(
	ctx context.Context,
	rsClient RunnerScaleSetClient,
	manager KubernetesManager,
	settings *ScaleSettings,
	options ...func(*Service),
) (*Service, error) {
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

	if len(s.errs) > 0 {
		return nil, errors.Join(s.errs...)
	}

	return s, nil
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

func (s *Service) processMessage(message *actions.RunnerScaleSetMessage) error {
	s.logger.Info("process message.", "messageId", message.MessageId, "messageType", message.MessageType)
	if message.Statistics == nil {
		return fmt.Errorf("can't process message with empty statistics")
	}

	s.metricsExporter.reset()
	defer s.metricsExporter.do()

	s.logger.Info("current runner scale set statistics.",
		"available jobs", message.Statistics.TotalAvailableJobs,
		"acquired jobs", message.Statistics.TotalAcquiredJobs,
		"assigned jobs", message.Statistics.TotalAssignedJobs,
		"running jobs", message.Statistics.TotalRunningJobs,
		"registered runners", message.Statistics.TotalRegisteredRunners,
		"busy runners", message.Statistics.TotalBusyRunners,
		"idle runners", message.Statistics.TotalIdleRunners)

	s.metricsExporter.withStatistics(message.Statistics)

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

		switch messageType.MessageType {
		case "JobAvailable":
			var jobAvailable actions.JobAvailable
			if err := json.Unmarshal(message, &jobAvailable); err != nil {
				return fmt.Errorf("could not decode job available message. %w", err)
			}
			s.logger.Info(
				"job available message received.",
				"RequestId",
				jobAvailable.RunnerRequestId,
			)
			s.metricsExporter.withJobAvailable(&jobAvailable)
			availableJobs = append(availableJobs, jobAvailable.RunnerRequestId)
		case "JobAssigned":
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(message, &jobAssigned); err != nil {
				return fmt.Errorf("could not decode job assigned message. %w", err)
			}
			s.logger.Info(
				"job assigned message received.",
				"RequestId",
				jobAssigned.RunnerRequestId,
			)
			s.metricsExporter.withJobAssigned(&jobAssigned)
		case "JobStarted":
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(message, &jobStarted); err != nil {
				return fmt.Errorf("could not decode job started message. %w", err)
			}
			s.logger.Info(
				"job started message received.",
				"RequestId",
				jobStarted.RunnerRequestId,
				"RunnerId",
				jobStarted.RunnerId,
			)
			s.metricsExporter.withJobStarted(&jobStarted)
			s.updateJobInfoForRunner(jobStarted)
		case "JobCompleted":
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(message, &jobCompleted); err != nil {
				return fmt.Errorf("could not decode job completed message. %w", err)
			}
			s.logger.Info(
				"job completed message received.",
				"RequestId",
				jobCompleted.RunnerRequestId,
				"Result",
				jobCompleted.Result,
				"RunnerId",
				jobCompleted.RunnerId,
				"RunnerName",
				jobCompleted.RunnerName,
			)
			s.metricsExporter.withJobCompleted(&jobCompleted)
		default:
			s.logger.Info("unknown job message type.", "messageType", messageType.MessageType)
		}
	}

	err := s.rsClient.AcquireJobsForRunnerScaleSet(s.ctx, availableJobs)
	if err != nil {
		return fmt.Errorf("could not acquire jobs. %w", err)
	}
	s.metricsExporter.withJobsAcquired(len(availableJobs))

	return s.scaleForAssignedJobCount(message.Statistics.TotalAssignedJobs)
}

func (s *Service) scaleForAssignedJobCount(count int) error {
	targetRunnerCount := int(math.Max(math.Min(float64(s.settings.MaxRunners), float64(count)), float64(s.settings.MinRunners)))
	s.metricsExporter.withDesiredRunners(targetRunnerCount)
	if targetRunnerCount != s.currentRunnerCount {
		s.logger.Info("try scale runner request up/down base on assigned job count",
			"assigned job", count,
			"decision", targetRunnerCount,
			"min", s.settings.MinRunners,
			"max", s.settings.MaxRunners,
			"currentRunnerCount", s.currentRunnerCount,
		)
		err := s.kubeManager.ScaleEphemeralRunnerSet(s.ctx, s.settings.Namespace, s.settings.ResourceName, targetRunnerCount)
		if err != nil {
			return fmt.Errorf("could not scale ephemeral runner set (%s/%s). %w", s.settings.Namespace, s.settings.ResourceName, err)
		}

		s.currentRunnerCount = targetRunnerCount
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
		"requestId", jobInfo.RunnerRequestId,
	)
	err := s.kubeManager.UpdateEphemeralRunnerWithJobInfo(s.ctx, s.settings.Namespace, jobInfo.RunnerName, jobInfo.OwnerName, jobInfo.RepositoryName, jobInfo.JobWorkflowRef, jobInfo.JobDisplayName, jobInfo.WorkflowRunId, jobInfo.RunnerRequestId)
	if err != nil {
		s.logger.Error(err, "could not update ephemeral runner with job info", "runnerName", jobInfo.RunnerName, "requestId", jobInfo.RunnerRequestId)
	}
}
