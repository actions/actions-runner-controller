package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewService(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)

	require.NoError(t, err)
	assert.Equal(t, logger, service.logger)
}

func TestStart(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockRsClient.On("GetRunnerScaleSetMessage", service.ctx, mock.Anything).Run(func(args mock.Arguments) { cancel() }).Return(nil).Once()

	err = service.Start()

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestStart_ScaleToMinRunners(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   5,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 5).Run(func(args mock.Arguments) { cancel() }).Return(nil).Once()

	err = service.Start()

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestStart_ScaleToMinRunnersFailed(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   5,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 5).Return(fmt.Errorf("error")).Once()

	err = service.Start()

	assert.ErrorContains(t, err, "could not scale to match minimal runners", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestStart_GetMultipleMessages(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockRsClient.On("GetRunnerScaleSetMessage", service.ctx, mock.Anything).Return(nil).Times(5)
	mockRsClient.On("GetRunnerScaleSetMessage", service.ctx, mock.Anything).Run(func(args mock.Arguments) { cancel() }).Return(nil).Once()

	err = service.Start()

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestStart_ErrorOnMessage(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockRsClient.On("GetRunnerScaleSetMessage", service.ctx, mock.Anything).Return(nil).Times(2)
	mockRsClient.On("GetRunnerScaleSetMessage", service.ctx, mock.Anything).Return(fmt.Errorf("error")).Once()

	err = service.Start()

	assert.ErrorContains(t, err, "could not get and process message. error", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_NoStatistic(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "test",
		Body:        "test",
	})

	assert.ErrorContains(t, err, "can't process message with empty statistics", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_IgnoreUnknownMessageType(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "unknown",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
		},
		Body: "[]",
	})

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_InvalidBatchMessageJson(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)

	require.NoError(t, err)

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
		},
		Body: "invalid json",
	})

	assert.ErrorContains(t, err, "could not decode job messages", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_InvalidJobMessageJson(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
		},
		Body: "[\"something\", \"test\"]",
	})

	assert.ErrorContains(t, err, "could not decode job message type", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_MultipleMessages(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   1,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockRsClient.On("AcquireJobsForRunnerScaleSet", ctx, mock.MatchedBy(func(ids []int64) bool { return ids[0] == 3 && ids[1] == 4 })).Return(nil).Once()
	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 2).Run(func(args mock.Arguments) { cancel() }).Return(nil).Once()

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAssignedJobs:  2,
			TotalAvailableJobs: 2,
		},
		Body: "[{\"messageType\":\"JobAvailable\", \"runnerRequestId\": 3},{\"messageType\":\"JobAvailable\", \"runnerRequestId\": 4},{\"messageType\":\"JobAssigned\", \"runnerRequestId\": 2}, {\"messageType\":\"JobCompleted\", \"runnerRequestId\": 1, \"result\":\"succeed\"},{\"messageType\":\"unknown\"}]",
	})

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_AcquireJobsFailed(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockRsClient.On("AcquireJobsForRunnerScaleSet", ctx, mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 })).Return(fmt.Errorf("error")).Once()

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAssignedJobs:  1,
			TotalAvailableJobs: 1,
		},
		Body: "[{\"messageType\":\"JobAvailable\", \"runnerRequestId\": 1}]",
	})

	assert.ErrorContains(t, err, "could not acquire jobs. error", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestScaleForAssignedJobCount_DeDupScale(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   0,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 2).Return(nil).Once()

	err = service.scaleForAssignedJobCount(2)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(2)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(2)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(2)

	assert.NoError(t, err, "Unexpected error")
	assert.Equal(t, 2, service.currentRunnerCount, "Unexpected runner count")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestScaleForAssignedJobCount_ScaleWithinMinMax(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   1,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 1).Return(nil).Once()
	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 3).Return(nil).Once()
	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 5).Return(nil).Once()
	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 1).Return(nil).Once()
	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 5).Return(nil).Once()

	err = service.scaleForAssignedJobCount(0)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(3)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(5)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(1)
	require.NoError(t, err, "Unexpected error")
	err = service.scaleForAssignedJobCount(10)

	assert.NoError(t, err, "Unexpected error")
	assert.Equal(t, 5, service.currentRunnerCount, "Unexpected runner count")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestScaleForAssignedJobCount_ScaleFailed(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   1,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	mockKubeManager.On("ScaleEphemeralRunnerSet", ctx, service.settings.Namespace, service.settings.ResourceName, 2).Return(fmt.Errorf("error"))

	err = service.scaleForAssignedJobCount(2)

	assert.ErrorContains(t, err, "could not scale ephemeral runner set (namespace/resource). error", "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_JobStartedMessage(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   1,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	service.currentRunnerCount = 1

	mockKubeManager.On("UpdateEphemeralRunnerWithJobInfo", ctx, service.settings.Namespace, "runner1", "owner1", "repo1", ".github/workflows/ci.yaml", "job1", int64(100), int64(3)).Run(func(args mock.Arguments) { cancel() }).Return(nil).Once()
	mockRsClient.On("AcquireJobsForRunnerScaleSet", ctx, mock.MatchedBy(func(ids []int64) bool { return len(ids) == 0 })).Return(nil).Once()

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAssignedJobs:  1,
			TotalAvailableJobs: 0,
		},
		Body: "[{\"messageType\":\"JobStarted\", \"runnerRequestId\": 3, \"runnerId\": 1, \"runnerName\": \"runner1\", \"ownerName\": \"owner1\", \"repositoryName\": \"repo1\", \"jobWorkflowRef\": \".github/workflows/ci.yaml\", \"jobDisplayName\": \"job1\", \"workflowRunId\": 100 }]",
	})

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}

func TestProcessMessage_JobStartedMessageIgnoreRunnerUpdateError(t *testing.T) {
	mockRsClient := &MockRunnerScaleSetClient{}
	mockKubeManager := &MockKubernetesManager{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewService(
		ctx,
		mockRsClient,
		mockKubeManager,
		&ScaleSettings{
			Namespace:    "namespace",
			ResourceName: "resource",
			MinRunners:   1,
			MaxRunners:   5,
		},
		func(s *Service) {
			s.logger = logger
		},
	)
	require.NoError(t, err)

	service.currentRunnerCount = 1

	mockKubeManager.On("UpdateEphemeralRunnerWithJobInfo", ctx, service.settings.Namespace, "runner1", "owner1", "repo1", ".github/workflows/ci.yaml", "job1", int64(100), int64(3)).Run(func(args mock.Arguments) { cancel() }).Return(fmt.Errorf("error")).Once()
	mockRsClient.On("AcquireJobsForRunnerScaleSet", ctx, mock.MatchedBy(func(ids []int64) bool { return len(ids) == 0 })).Return(nil).Once()

	err = service.processMessage(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "RunnerScaleSetJobMessages",
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAssignedJobs:  0,
			TotalAvailableJobs: 0,
		},
		Body: "[{\"messageType\":\"JobStarted\", \"runnerRequestId\": 3, \"runnerId\": 1, \"runnerName\": \"runner1\", \"ownerName\": \"owner1\", \"repositoryName\": \"repo1\", \"jobWorkflowRef\": \".github/workflows/ci.yaml\", \"jobDisplayName\": \"job1\", \"workflowRunId\": 100 }]",
	})

	assert.NoError(t, err, "Unexpected error")
	assert.True(t, mockRsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockKubeManager.AssertExpectations(t), "All expectations should be met")
}
