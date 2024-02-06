package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCreateSession(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	require.NoError(t, err, "Error creating autoscaler client")
	assert.Equal(t, session, session, "Session is not correct")
	assert.NotNil(t, asClient.initialMessage, "Initial message should not be nil")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be 0")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestCreateSession_CreateInitMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
			TotalAssignedJobs:  5,
		},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockActionsClient.On("GetAcquirableJobs", ctx, 1).Return(&actions.AcquirableJobList{
		Count: 1,
		Jobs: []actions.AcquirableJob{
			{
				RunnerRequestId: 1,
				OwnerName:       "owner",
				RepositoryName:  "repo",
				AcquireJobUrl:   "https://github.com",
			},
		},
	}, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	require.NoError(t, err, "Error creating autoscaler client")
	assert.Equal(t, session, session, "Session is not correct")
	assert.NotNil(t, asClient.initialMessage, "Initial message should not be nil")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be 0")
	assert.Equal(t, int64(0), asClient.initialMessage.MessageId, "Initial message id should be 0")
	assert.Equal(t, "RunnerScaleSetJobMessages", asClient.initialMessage.MessageType, "Initial message type should be RunnerScaleSetJobMessages")
	assert.Equal(t, 5, asClient.initialMessage.Statistics.TotalAssignedJobs, "Initial message total assigned jobs should be 5")
	assert.Equal(t, 1, asClient.initialMessage.Statistics.TotalAvailableJobs, "Initial message total available jobs should be 1")
	assert.Equal(t, "[{\"acquireJobUrl\":\"https://github.com\",\"messageType\":\"\",\"runnerRequestId\":1,\"repositoryName\":\"repo\",\"ownerName\":\"owner\",\"jobWorkflowRef\":\"\",\"eventName\":\"\",\"requestLabels\":null}]", asClient.initialMessage.Body, "Initial message body is not correct")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestCreateSession_CreateInitMessageWithOnlyAssignedJobs(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAssignedJobs: 5,
		},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockActionsClient.On("GetAcquirableJobs", ctx, 1).Return(&actions.AcquirableJobList{
		Count: 0,
		Jobs:  []actions.AcquirableJob{},
	}, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	require.NoError(t, err, "Error creating autoscaler client")
	assert.Equal(t, session, session, "Session is not correct")
	assert.NotNil(t, asClient.initialMessage, "Initial message should not be nil")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be 0")
	assert.Equal(t, int64(0), asClient.initialMessage.MessageId, "Initial message id should be 0")
	assert.Equal(t, "RunnerScaleSetJobMessages", asClient.initialMessage.MessageType, "Initial message type should be RunnerScaleSetJobMessages")
	assert.Equal(t, 5, asClient.initialMessage.Statistics.TotalAssignedJobs, "Initial message total assigned jobs should be 5")
	assert.Equal(t, 0, asClient.initialMessage.Statistics.TotalAvailableJobs, "Initial message total available jobs should be 0")
	assert.Equal(t, "[]", asClient.initialMessage.Body, "Initial message body is not correct")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestCreateSession_CreateInitMessageFailed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
			TotalAssignedJobs:  5,
		},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockActionsClient.On("GetAcquirableJobs", ctx, 1).Return(nil, fmt.Errorf("error"))

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	assert.ErrorContains(t, err, "get acquirable jobs failed. error", "Unexpected error")
	assert.Nil(t, asClient, "Client should be nil")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestCreateSession_RetrySessionConflict(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.WithValue(context.Background(), testIgnoreSleep, true)
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(nil, &actions.HttpClientSideError{
		Code: 409,
	}).Once()
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil).Once()

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	require.NoError(t, err, "Error creating autoscaler client")
	assert.Equal(t, session, session, "Session is not correct")
	assert.NotNil(t, asClient.initialMessage, "Initial message should not be nil")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be 0")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestCreateSession_RetrySessionConflict_RunOutOfRetry(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.WithValue(context.Background(), testIgnoreSleep, true)
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(nil, &actions.HttpClientSideError{
		Code: 409,
	})

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	assert.Error(t, err, "Error should be returned")
	assert.Nil(t, asClient, "AutoScaler should be nil")
	assert.True(t, mockActionsClient.AssertNumberOfCalls(t, "CreateMessageSession", sessionCreationMaxRetryCount), "CreateMessageSession should be called 10 times")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestCreateSession_NotRetryOnGeneralException(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.WithValue(context.Background(), testIgnoreSleep, true)
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(nil, &actions.HttpClientSideError{
		Code: 403,
	})

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)

	assert.Error(t, err, "Error should be returned")
	assert.Nil(t, asClient, "AutoScaler should be nil")
	assert.True(t, mockActionsClient.AssertNumberOfCalls(t, "CreateMessageSession", 1), "CreateMessageSession should be called 1 time and not retry on generic error")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestDeleteSession(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("Close").Return(nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.Close()
	assert.NoError(t, err, "Error deleting session")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestDeleteSession_Failed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("Close").Return(fmt.Errorf("error"))

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.Close()
	assert.Error(t, err, "Error should be returned")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetRunnerScaleSetMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("GetMessage", ctx, int64(0), mock.Anything).Return(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "test",
		Body:        "test",
	}, nil)
	mockSessionClient.On("DeleteMessage", ctx, int64(1)).Return(nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)

	assert.NoError(t, err, "Error getting message")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Initial message")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)

	assert.NoError(t, err, "Error getting message")
	assert.Equal(t, int64(1), asClient.lastMessageId, "Last message id should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetRunnerScaleSetMessage_HandleFailed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("GetMessage", ctx, int64(0), mock.Anything).Return(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "test",
		Body:        "test",
	}, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	// read initial message
	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)

	assert.NoError(t, err, "Error getting message")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return fmt.Errorf("error")
	}, 10)

	assert.ErrorContains(t, err, "handle message failed. error", "Error getting message")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should not be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetRunnerScaleSetMessage_HandleInitialMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
			TotalAssignedJobs:  2,
		},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything, mock.Anything).Return(session, nil)
	mockActionsClient.On("GetAcquirableJobs", ctx, 1).Return(&actions.AcquirableJobList{
		Count: 1,
		Jobs: []actions.AcquirableJob{
			{
				RunnerRequestId: 1,
				OwnerName:       "owner",
				RepositoryName:  "repo",
				AcquireJobUrl:   "https://github.com",
			},
		},
	}, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)
	require.NoError(t, err, "Error creating autoscaler client")
	require.NotNil(t, asClient.initialMessage, "Initial message should be set")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)

	assert.NoError(t, err, "Error getting message")
	assert.Nil(t, asClient.initialMessage, "Initial message should be nil")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetRunnerScaleSetMessage_HandleInitialMessageFailed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs: 1,
			TotalAssignedJobs:  2,
		},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockActionsClient.On("GetAcquirableJobs", ctx, 1).Return(&actions.AcquirableJobList{
		Count: 1,
		Jobs: []actions.AcquirableJob{
			{
				RunnerRequestId: 1,
				OwnerName:       "owner",
				RepositoryName:  "repo",
				AcquireJobUrl:   "https://github.com",
			},
		},
	}, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1)
	require.NoError(t, err, "Error creating autoscaler client")
	require.NotNil(t, asClient.initialMessage, "Initial message should be set")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return fmt.Errorf("error")
	}, 10)

	assert.ErrorContains(t, err, "fail to process initial message. error", "Error getting message")
	assert.NotNil(t, asClient.initialMessage, "Initial message should be nil")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetRunnerScaleSetMessage_RetryUntilGetMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("GetMessage", ctx, int64(0), mock.Anything).Return(nil, nil).Times(3)
	mockSessionClient.On("GetMessage", ctx, int64(0), mock.Anything).Return(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "test",
		Body:        "test",
	}, nil).Once()
	mockSessionClient.On("DeleteMessage", ctx, int64(1)).Return(nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)
	assert.NoError(t, err, "Error getting initial message")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)

	assert.NoError(t, err, "Error getting message")
	assert.Equal(t, int64(1), asClient.lastMessageId, "Last message id should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetRunnerScaleSetMessage_ErrorOnGetMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("GetMessage", ctx, int64(0), mock.Anything).Return(nil, fmt.Errorf("error"))

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	// process initial message
	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		return nil
	}, 10)
	assert.NoError(t, err, "Error getting initial message")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		return fmt.Errorf("Should not be called")
	}, 10)

	assert.ErrorContains(t, err, "get message failed from refreshing client. error", "Error should be returned")
	assert.Equal(t, int64(0), asClient.lastMessageId, "Last message id should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestDeleteRunnerScaleSetMessage_Error(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("GetMessage", ctx, int64(0), mock.Anything).Return(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "test",
		Body:        "test",
	}, nil)
	mockSessionClient.On("DeleteMessage", ctx, int64(1)).Return(fmt.Errorf("error"))

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)
	assert.NoError(t, err, "Error getting initial message")

	err = asClient.GetRunnerScaleSetMessage(ctx, func(msg *actions.RunnerScaleSetMessage) error {
		logger.Info("Message received", "messageId", msg.MessageId, "messageType", msg.MessageType, "body", msg.Body)
		return nil
	}, 10)

	assert.ErrorContains(t, err, "delete message failed from refreshing client. error", "Error getting message")
	assert.Equal(t, int64(1), asClient.lastMessageId, "Last message id should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestAcquireJobsForRunnerScaleSet(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("AcquireJobs", ctx, mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 && ids[1] == 2 && ids[2] == 3 })).Return([]int64{1, 2, 3}, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.AcquireJobsForRunnerScaleSet(ctx, []int64{1, 2, 3})
	assert.NoError(t, err, "Error acquiring jobs")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestAcquireJobsForRunnerScaleSet_SkipEmptyList(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.AcquireJobsForRunnerScaleSet(ctx, []int64{})
	assert.NoError(t, err, "Error acquiring jobs")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}

func TestAcquireJobsForRunnerScaleSet_Failed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	mockSessionClient := &actions.MockSessionService{}
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, err, "Error creating logger")

	ctx := context.Background()
	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
		Statistics: &actions.RunnerScaleSetStatistic{},
	}
	mockActionsClient.On("CreateMessageSession", ctx, 1, mock.Anything).Return(session, nil)
	mockSessionClient.On("AcquireJobs", ctx, mock.Anything).Return(nil, fmt.Errorf("error"))

	asClient, err := NewAutoScalerClient(ctx, mockActionsClient, &logger, 1, func(asc *AutoScalerClient) {
		asc.client = mockSessionClient
	})
	require.NoError(t, err, "Error creating autoscaler client")

	err = asClient.AcquireJobsForRunnerScaleSet(ctx, []int64{1, 2, 3})
	assert.ErrorContains(t, err, "acquire jobs failed from refreshing client. error", "Expect error acquiring jobs")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
	assert.True(t, mockSessionClient.AssertExpectations(t), "All expectations should be met")
}
