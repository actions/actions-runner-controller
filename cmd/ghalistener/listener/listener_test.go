package listener

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	listenermocks "github.com/actions/actions-runner-controller/cmd/ghalistener/listener/mocks"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("InvalidConfig", func(t *testing.T) {
		t.Parallel()
		var config Config
		_, err := New(config)
		assert.NotNil(t, err)
	})

	t.Run("ValidConfig", func(t *testing.T) {
		t.Parallel()
		config := Config{
			Client:     listenermocks.NewClient(t),
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}
		l, err := New(config)
		assert.Nil(t, err)
		assert.NotNil(t, l)
	})
}

func TestListener_createSession(t *testing.T) {
	t.Parallel()
	t.Run("FailOnce", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()

		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)
		client.On("CreateMessageSession", ctx, mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		err = l.createSession(ctx)
		assert.NotNil(t, err)
	})

	t.Run("FailContext", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)
		client.On("CreateMessageSession", ctx, mock.Anything, mock.Anything).Return(nil,
			&actions.HttpClientSideError{Code: http.StatusConflict}).Once()
		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		err = l.createSession(ctx)
		assert.True(t, errors.Is(err, context.DeadlineExceeded))
	})

	t.Run("SetsSession", func(t *testing.T) {
		t.Parallel()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              nil,
		}
		client.On("CreateMessageSession", mock.Anything, mock.Anything, mock.Anything).Return(session, nil).Once()
		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		err = l.createSession(context.Background())
		assert.Nil(t, err)
		assert.Equal(t, session, l.session)
	})
}

func TestListener_getMessage(t *testing.T) {
	t.Parallel()

	t.Run("ReceivesMessage", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)
		want := &actions.RunnerScaleSetMessage{
			MessageId: 1,
		}
		client.On("GetMessage", ctx, mock.Anything, mock.Anything, mock.Anything).Return(want, nil).Once()
		config.Client = client

		l, err := New(config)
		require.Nil(t, err)
		l.session = &actions.RunnerScaleSetSession{}

		got, err := l.getMessage(ctx)
		assert.Nil(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("NotExpiredError", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)
		client.On("GetMessage", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, &actions.HttpClientSideError{Code: http.StatusNotFound}).Once()
		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{}

		_, err = l.getMessage(ctx)
		assert.NotNil(t, err)
	})

	t.Run("RefreshAndSucceeds", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              nil,
		}
		client.On("RefreshMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()

		client.On("GetMessage", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, &actions.MessageQueueTokenExpiredError{}).Once()

		want := &actions.RunnerScaleSetMessage{
			MessageId: 1,
		}
		client.On("GetMessage", ctx, mock.Anything, mock.Anything, mock.Anything).Return(want, nil).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{
			SessionId:      &uuid,
			RunnerScaleSet: &actions.RunnerScaleSet{},
		}

		got, err := l.getMessage(ctx)
		assert.Nil(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("RefreshAndFails", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              nil,
		}
		client.On("RefreshMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()

		client.On("GetMessage", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, &actions.MessageQueueTokenExpiredError{}).Twice()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{
			SessionId:      &uuid,
			RunnerScaleSet: &actions.RunnerScaleSet{},
		}

		got, err := l.getMessage(ctx)
		assert.NotNil(t, err)
		assert.Nil(t, got)
	})
}

func TestListener_refreshSession(t *testing.T) {
	t.Parallel()

	t.Run("SuccessfullyRefreshes", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		newUUID := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &newUUID,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              nil,
		}
		client.On("RefreshMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		oldUUID := uuid.New()
		l.session = &actions.RunnerScaleSetSession{
			SessionId:      &oldUUID,
			RunnerScaleSet: &actions.RunnerScaleSet{},
		}

		err = l.refreshSession(ctx)
		assert.Nil(t, err)
		assert.Equal(t, session, l.session)
	})

	t.Run("FailsToRefresh", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		client.On("RefreshMessageSession", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("error")).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		oldUUID := uuid.New()
		oldSession := &actions.RunnerScaleSetSession{
			SessionId:      &oldUUID,
			RunnerScaleSet: &actions.RunnerScaleSet{},
		}
		l.session = oldSession

		err = l.refreshSession(ctx)
		assert.NotNil(t, err)
		assert.Equal(t, oldSession, l.session)
	})
}

func TestListener_deleteLastMessage(t *testing.T) {
	t.Parallel()

	t.Run("SuccessfullyDeletes", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		client.On("DeleteMessage", ctx, mock.Anything, mock.Anything, mock.MatchedBy(func(lastMessageID any) bool {
			return lastMessageID.(int64) == int64(5)
		})).Return(nil).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{}
		l.lastMessageID = 5

		err = l.deleteLastMessage(ctx)
		assert.Nil(t, err)
	})

	t.Run("FailsToDelete", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		client.On("DeleteMessage", ctx, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error")).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{}
		l.lastMessageID = 5

		err = l.deleteLastMessage(ctx)
		assert.NotNil(t, err)
	})
}

func TestListener_Listen(t *testing.T) {
	t.Parallel()

	t.Run("CreateSessionFails", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)
		client.On("CreateMessageSession", ctx, mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		err = l.Listen(ctx, nil)
		assert.NotNil(t, err)
	})

	t.Run("CallHandleRegardlessOfInitialMessage", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())

		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              &actions.RunnerScaleSetStatistic{},
		}
		client.On("CreateMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()
		client.On("DeleteMessageSession", mock.Anything, session.RunnerScaleSet.Id, session.SessionId).Return(nil).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		var called bool
		handler := listenermocks.NewHandler(t)
		handler.On("HandleDesiredRunnerCount", mock.Anything, mock.Anything, 0).
			Return(0, nil).
			Run(
				func(mock.Arguments) {
					called = true
					cancel()
				},
			).
			Once()

		err = l.Listen(ctx, handler)
		assert.True(t, errors.Is(err, context.Canceled))
		assert.True(t, called)
	})

	t.Run("CancelContextAfterGetMessage", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())

		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)
		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              &actions.RunnerScaleSetStatistic{},
		}
		client.On("CreateMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()
		client.On("DeleteMessageSession", mock.Anything, session.RunnerScaleSet.Id, session.SessionId).Return(nil).Once()

		msg := &actions.RunnerScaleSetMessage{
			MessageId:   1,
			MessageType: "RunnerScaleSetJobMessages",
			Statistics:  &actions.RunnerScaleSetStatistic{},
		}
		client.On("GetMessage", ctx, mock.Anything, mock.Anything, mock.Anything).
			Return(msg, nil).
			Run(
				func(mock.Arguments) {
					cancel()
				},
			).
			Once()

			// Ensure delete message is called with background context
		client.On("DeleteMessage", context.Background(), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		config.Client = client

		handler := listenermocks.NewHandler(t)
		handler.On("HandleDesiredRunnerCount", mock.Anything, mock.Anything, 0).
			Return(0, nil).
			Once()

		handler.On("HandleDesiredRunnerCount", mock.Anything, mock.Anything, 0).
			Return(0, nil).
			Once()

		l, err := New(config)
		require.Nil(t, err)

		err = l.Listen(ctx, handler)
		assert.ErrorIs(t, context.Canceled, err)
	})
}

func TestListener_acquireAvailableJobs(t *testing.T) {
	t.Parallel()

	t.Run("FailingToAcquireJobs", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		client.On("AcquireJobs", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		uuid := uuid.New()
		l.session = &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              &actions.RunnerScaleSetStatistic{},
		}

		availableJobs := []*actions.JobAvailable{
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 1,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 2,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 3,
				},
			},
		}
		_, err = l.acquireAvailableJobs(ctx, availableJobs)
		assert.Error(t, err)
	})

	t.Run("SuccessfullyAcquiresJobsOnFirstRun", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		jobIDs := []int64{1, 2, 3}

		client.On("AcquireJobs", ctx, mock.Anything, mock.Anything, mock.Anything).Return(jobIDs, nil).Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		uuid := uuid.New()
		l.session = &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              &actions.RunnerScaleSetStatistic{},
		}

		availableJobs := []*actions.JobAvailable{
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 1,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 2,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 3,
				},
			},
		}
		acquiredJobIDs, err := l.acquireAvailableJobs(ctx, availableJobs)
		assert.NoError(t, err)
		assert.Equal(t, []int64{1, 2, 3}, acquiredJobIDs)
	})

	t.Run("RefreshAndSucceeds", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              nil,
		}
		client.On("RefreshMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()

		// Second call to AcquireJobs will succeed
		want := []int64{1, 2, 3}
		availableJobs := []*actions.JobAvailable{
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 1,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 2,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 3,
				},
			},
		}

		// First call to AcquireJobs will fail with a token expired error
		client.On("AcquireJobs", ctx, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				ids := args.Get(3).([]int64)
				assert.Equal(t, want, ids)
			}).
			Return(nil, &actions.MessageQueueTokenExpiredError{}).
			Once()

		// Second call should succeed
		client.On("AcquireJobs", ctx, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				ids := args.Get(3).([]int64)
				assert.Equal(t, want, ids)
			}).
			Return(want, nil).
			Once()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{
			SessionId:      &uuid,
			RunnerScaleSet: &actions.RunnerScaleSet{},
		}

		got, err := l.acquireAvailableJobs(ctx, availableJobs)
		assert.Nil(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("RefreshAndFails", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		config := Config{
			ScaleSetID: 1,
			Metrics:    metrics.Discard,
		}

		client := listenermocks.NewClient(t)

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              nil,
		}
		client.On("RefreshMessageSession", ctx, mock.Anything, mock.Anything).Return(session, nil).Once()

		client.On("AcquireJobs", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, &actions.MessageQueueTokenExpiredError{}).Twice()

		config.Client = client

		l, err := New(config)
		require.Nil(t, err)

		l.session = &actions.RunnerScaleSetSession{
			SessionId:      &uuid,
			RunnerScaleSet: &actions.RunnerScaleSet{},
		}

		availableJobs := []*actions.JobAvailable{
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 1,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 2,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					RunnerRequestId: 3,
				},
			},
		}

		got, err := l.acquireAvailableJobs(ctx, availableJobs)
		assert.NotNil(t, err)
		assert.Nil(t, got)
	})
}

func TestListener_parseMessage(t *testing.T) {
	t.Run("FailOnEmptyStatistics", func(t *testing.T) {
		msg := &actions.RunnerScaleSetMessage{
			MessageId:   1,
			MessageType: "RunnerScaleSetJobMessages",
			Statistics:  nil,
		}

		l := &Listener{}
		parsedMsg, err := l.parseMessage(context.Background(), msg)
		assert.Error(t, err)
		assert.Nil(t, parsedMsg)
	})

	t.Run("FailOnIncorrectMessageType", func(t *testing.T) {
		msg := &actions.RunnerScaleSetMessage{
			MessageId:   1,
			MessageType: "RunnerMessages", // arbitrary message type
			Statistics:  &actions.RunnerScaleSetStatistic{},
		}

		l := &Listener{}
		parsedMsg, err := l.parseMessage(context.Background(), msg)
		assert.Error(t, err)
		assert.Nil(t, parsedMsg)
	})

	t.Run("ParseAll", func(t *testing.T) {
		msg := &actions.RunnerScaleSetMessage{
			MessageId:   1,
			MessageType: "RunnerScaleSetJobMessages",
			Body:        "",
			Statistics: &actions.RunnerScaleSetStatistic{
				TotalAvailableJobs:     1,
				TotalAcquiredJobs:      2,
				TotalAssignedJobs:      3,
				TotalRunningJobs:       4,
				TotalRegisteredRunners: 5,
				TotalBusyRunners:       6,
				TotalIdleRunners:       7,
			},
		}

		var batchedMessages []any
		jobsAvailable := []*actions.JobAvailable{
			{
				AcquireJobUrl: "https://github.com/example",
				JobMessageBase: actions.JobMessageBase{
					JobMessageType: actions.JobMessageType{
						MessageType: messageTypeJobAvailable,
					},
					RunnerRequestId: 1,
				},
			},
			{
				AcquireJobUrl: "https://github.com/example",
				JobMessageBase: actions.JobMessageBase{
					JobMessageType: actions.JobMessageType{
						MessageType: messageTypeJobAvailable,
					},
					RunnerRequestId: 2,
				},
			},
		}
		for _, msg := range jobsAvailable {
			batchedMessages = append(batchedMessages, msg)
		}

		jobsAssigned := []*actions.JobAssigned{
			{
				JobMessageBase: actions.JobMessageBase{
					JobMessageType: actions.JobMessageType{
						MessageType: messageTypeJobAssigned,
					},
					RunnerRequestId: 3,
				},
			},
			{
				JobMessageBase: actions.JobMessageBase{
					JobMessageType: actions.JobMessageType{
						MessageType: messageTypeJobAssigned,
					},
					RunnerRequestId: 4,
				},
			},
		}
		for _, msg := range jobsAssigned {
			batchedMessages = append(batchedMessages, msg)
		}

		jobsStarted := []*actions.JobStarted{
			{
				JobMessageBase: actions.JobMessageBase{
					JobMessageType: actions.JobMessageType{
						MessageType: messageTypeJobStarted,
					},
					RunnerRequestId: 5,
				},
				RunnerId:   2,
				RunnerName: "runner2",
			},
		}
		for _, msg := range jobsStarted {
			batchedMessages = append(batchedMessages, msg)
		}

		jobsCompleted := []*actions.JobCompleted{
			{
				JobMessageBase: actions.JobMessageBase{
					JobMessageType: actions.JobMessageType{
						MessageType: messageTypeJobCompleted,
					},
					RunnerRequestId: 6,
				},
				Result:     "success",
				RunnerId:   1,
				RunnerName: "runner1",
			},
		}
		for _, msg := range jobsCompleted {
			batchedMessages = append(batchedMessages, msg)
		}

		b, err := json.Marshal(batchedMessages)
		require.NoError(t, err)

		msg.Body = string(b)

		l := &Listener{}
		parsedMsg, err := l.parseMessage(context.Background(), msg)
		require.NoError(t, err)

		assert.Equal(t, msg.Statistics, parsedMsg.statistics)
		assert.Equal(t, jobsAvailable, parsedMsg.jobsAvailable)
		assert.Equal(t, jobsStarted, parsedMsg.jobsStarted)
		assert.Equal(t, jobsCompleted, parsedMsg.jobsCompleted)
	})
}
