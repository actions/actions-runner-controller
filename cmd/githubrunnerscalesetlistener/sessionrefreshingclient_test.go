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

func TestGetMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("GetMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(0), 10).Return(nil, nil).Once()
	mockActionsClient.On("GetMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(0), 10).Return(&actions.RunnerScaleSetMessage{MessageId: 1}, nil).Once()

	client := newSessionClient(mockActionsClient, &logger, session)

	msg, err := client.GetMessage(ctx, 0, 10)
	require.NoError(t, err, "GetMessage should not return an error")

	assert.Nil(t, msg, "GetMessage should return nil message")

	msg, err = client.GetMessage(ctx, 0, 10)
	require.NoError(t, err, "GetMessage should not return an error")

	assert.Equal(t, int64(1), msg.MessageId, "GetMessage should return a message with id 1")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestDeleteMessage(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("DeleteMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(1)).Return(nil).Once()

	client := newSessionClient(mockActionsClient, &logger, session)

	err := client.DeleteMessage(ctx, int64(1))
	assert.NoError(t, err, "DeleteMessage should not return an error")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestAcquireJobs(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}
	mockActionsClient.On("AcquireJobs", ctx, mock.Anything, "token", mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 && ids[1] == 2 && ids[2] == 3 })).Return([]int64{1}, nil)

	client := newSessionClient(mockActionsClient, &logger, session)

	ids, err := client.AcquireJobs(ctx, []int64{1, 2, 3})
	assert.NoError(t, err, "AcquireJobs should not return an error")
	assert.Equal(t, []int64{1}, ids, "AcquireJobs should return a slice with one id")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestClose(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	sessionId := uuid.New()
	session := &actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		OwnerName:               "owner",
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
	}

	mockActionsClient.On("DeleteMessageSession", mock.Anything, 1, &sessionId).Return(nil).Once()

	client := newSessionClient(mockActionsClient, &logger, session)

	err := client.Close()
	assert.NoError(t, err, "DeleteMessageSession should not return an error")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestGetMessage_Error(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("GetMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(0), 10).Return(nil, fmt.Errorf("error")).Once()

	client := newSessionClient(mockActionsClient, &logger, session)

	msg, err := client.GetMessage(ctx, 0, 10)
	assert.ErrorContains(t, err, "get message failed. error", "GetMessage should return an error")
	assert.Nil(t, msg, "GetMessage should return nil message")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestDeleteMessage_SessionError(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("DeleteMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(1)).Return(fmt.Errorf("error")).Once()

	client := newSessionClient(mockActionsClient, &logger, session)

	err := client.DeleteMessage(ctx, int64(1))
	assert.ErrorContains(t, err, "delete message failed. error", "DeleteMessage should return an error")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestAcquireJobs_Error(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}
	mockActionsClient.On("AcquireJobs", ctx, mock.Anything, "token", mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 && ids[1] == 2 && ids[2] == 3 })).Return(nil, fmt.Errorf("error")).Once()

	client := newSessionClient(mockActionsClient, &logger, session)

	ids, err := client.AcquireJobs(ctx, []int64{1, 2, 3})
	assert.ErrorContains(t, err, "acquire jobs failed. error", "AcquireJobs should return an error")
	assert.Nil(t, ids, "AcquireJobs should return nil ids")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expected calls to mockActionsClient should have been made")
}

func TestGetMessage_RefreshToken(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}
	mockActionsClient.On("GetMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(0), 10).Return(nil, &actions.MessageQueueTokenExpiredError{}).Once()
	mockActionsClient.On("GetMessage", ctx, session.MessageQueueUrl, "token2", int64(0), 10).Return(&actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "test",
		Body:        "test",
	}, nil).Once()
	mockActionsClient.On("RefreshMessageSession", ctx, session.RunnerScaleSet.Id, session.SessionId).Return(&actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token2",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
	}, nil).Once()

	client := newSessionClient(mockActionsClient, &logger, session)
	msg, err := client.GetMessage(ctx, 0, 10)
	assert.NoError(t, err, "Error getting message")
	assert.Equal(t, int64(1), msg.MessageId, "message id should be updated")
	assert.Equal(t, "token2", client.session.MessageQueueAccessToken, "Message queue access token should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestDeleteMessage_RefreshSessionToken(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("DeleteMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(1)).Return(&actions.MessageQueueTokenExpiredError{}).Once()
	mockActionsClient.On("DeleteMessage", ctx, session.MessageQueueUrl, "token2", int64(1)).Return(nil).Once()
	mockActionsClient.On("RefreshMessageSession", ctx, session.RunnerScaleSet.Id, session.SessionId).Return(&actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token2",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
	}, nil)

	client := newSessionClient(mockActionsClient, &logger, session)
	err := client.DeleteMessage(ctx, 1)
	assert.NoError(t, err, "Error delete message")
	assert.Equal(t, "token2", client.session.MessageQueueAccessToken, "Message queue access token should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestAcquireJobs_RefreshToken(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("AcquireJobs", ctx, mock.Anything, session.MessageQueueAccessToken, mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 && ids[1] == 2 && ids[2] == 3 })).Return(nil, &actions.MessageQueueTokenExpiredError{}).Once()
	mockActionsClient.On("AcquireJobs", ctx, mock.Anything, "token2", mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 && ids[1] == 2 && ids[2] == 3 })).Return([]int64{1, 2, 3}, nil)
	mockActionsClient.On("RefreshMessageSession", ctx, session.RunnerScaleSet.Id, session.SessionId).Return(&actions.RunnerScaleSetSession{
		SessionId:               &sessionId,
		MessageQueueUrl:         "https://github.com",
		MessageQueueAccessToken: "token2",
		RunnerScaleSet: &actions.RunnerScaleSet{
			Id: 1,
		},
	}, nil)

	client := newSessionClient(mockActionsClient, &logger, session)
	ids, err := client.AcquireJobs(ctx, []int64{1, 2, 3})
	assert.NoError(t, err, "Error acquiring jobs")
	assert.Equal(t, []int64{1, 2, 3}, ids, "Job ids should be returned")
	assert.Equal(t, "token2", client.session.MessageQueueAccessToken, "Message queue access token should be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestGetMessage_RefreshToken_Failed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}
	mockActionsClient.On("GetMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(0), 10).Return(nil, &actions.MessageQueueTokenExpiredError{}).Once()
	mockActionsClient.On("RefreshMessageSession", ctx, session.RunnerScaleSet.Id, session.SessionId).Return(nil, fmt.Errorf("error"))

	client := newSessionClient(mockActionsClient, &logger, session)
	msg, err := client.GetMessage(ctx, 0, 10)
	assert.ErrorContains(t, err, "refresh message session failed. error", "Error should be returned")
	assert.Nil(t, msg, "Message should be nil")
	assert.Equal(t, "token", client.session.MessageQueueAccessToken, "Message queue access token should not be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestDeleteMessage_RefreshToken_Failed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}
	mockActionsClient.On("DeleteMessage", ctx, session.MessageQueueUrl, session.MessageQueueAccessToken, int64(1)).Return(&actions.MessageQueueTokenExpiredError{}).Once()
	mockActionsClient.On("RefreshMessageSession", ctx, session.RunnerScaleSet.Id, session.SessionId).Return(nil, fmt.Errorf("error"))

	client := newSessionClient(mockActionsClient, &logger, session)
	err := client.DeleteMessage(ctx, 1)

	assert.ErrorContains(t, err, "refresh message session failed. error", "Error getting message")
	assert.Equal(t, "token", client.session.MessageQueueAccessToken, "Message queue access token should not be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestAcquireJobs_RefreshToken_Failed(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

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
	}

	mockActionsClient.On("AcquireJobs", ctx, mock.Anything, session.MessageQueueAccessToken, mock.MatchedBy(func(ids []int64) bool { return ids[0] == 1 && ids[1] == 2 && ids[2] == 3 })).Return(nil, &actions.MessageQueueTokenExpiredError{}).Once()
	mockActionsClient.On("RefreshMessageSession", ctx, session.RunnerScaleSet.Id, session.SessionId).Return(nil, fmt.Errorf("error"))

	client := newSessionClient(mockActionsClient, &logger, session)
	ids, err := client.AcquireJobs(ctx, []int64{1, 2, 3})
	assert.ErrorContains(t, err, "refresh message session failed. error", "Expect error refreshing message session")
	assert.Nil(t, ids, "Job ids should be nil")
	assert.Equal(t, "token", client.session.MessageQueueAccessToken, "Message queue access token should not be updated")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}

func TestClose_Skip(t *testing.T) {
	mockActionsClient := &actions.MockActionsService{}
	logger, log_err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	logger = logger.WithName(t.Name())
	require.NoError(t, log_err, "Error creating logger")

	client := newSessionClient(mockActionsClient, &logger, nil)
	err := client.Close()
	require.NoError(t, err, "Error closing session client")
	assert.True(t, mockActionsClient.AssertExpectations(t), "All expectations should be met")
}
