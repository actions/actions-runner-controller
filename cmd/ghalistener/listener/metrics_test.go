package listener

import (
	"context"
	"encoding/json"
	"testing"

	listenermocks "github.com/actions/actions-runner-controller/cmd/ghalistener/listener/mocks"
	metricsmocks "github.com/actions/actions-runner-controller/cmd/ghalistener/metrics/mocks"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestInitialMetrics(t *testing.T) {
	t.Parallel()

	t.Run("SetStaticMetrics", func(t *testing.T) {
		t.Parallel()

		metrics := metricsmocks.NewPublisher(t)

		minRunners := 5
		maxRunners := 10
		metrics.On("PublishStatic", minRunners, maxRunners).Once()

		config := Config{
			Client:     listenermocks.NewClient(t),
			ScaleSetID: 1,
			Metrics:    metrics,
			MinRunners: minRunners,
			MaxRunners: maxRunners,
		}
		l, err := New(config)

		assert.Nil(t, err)
		assert.NotNil(t, l)
	})

	t.Run("InitialMessageStatistics", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())

		sessionStatistics := &actions.RunnerScaleSetStatistic{
			TotalAvailableJobs:     1,
			TotalAcquiredJobs:      2,
			TotalAssignedJobs:      3,
			TotalRunningJobs:       4,
			TotalRegisteredRunners: 5,
			TotalBusyRunners:       6,
			TotalIdleRunners:       7,
		}

		uuid := uuid.New()
		session := &actions.RunnerScaleSetSession{
			SessionId:               &uuid,
			OwnerName:               "example",
			RunnerScaleSet:          &actions.RunnerScaleSet{},
			MessageQueueUrl:         "https://example.com",
			MessageQueueAccessToken: "1234567890",
			Statistics:              sessionStatistics,
		}

		metrics := metricsmocks.NewPublisher(t)
		metrics.On("PublishStatic", mock.Anything, mock.Anything).Once()
		metrics.On("PublishStatistics", sessionStatistics).Once()
		metrics.On("PublishDesiredRunners", sessionStatistics.TotalAssignedJobs).
			Run(
				func(mock.Arguments) {
					cancel()
				},
			).Once()

		config := Config{
			Client:     listenermocks.NewClient(t),
			ScaleSetID: 1,
			Metrics:    metrics,
		}

		client := listenermocks.NewClient(t)
		client.On("CreateMessageSession", mock.Anything, mock.Anything, mock.Anything).Return(session, nil).Once()
		client.On("DeleteMessageSession", mock.Anything, session.RunnerScaleSet.Id, session.SessionId).Return(nil).Once()
		config.Client = client

		handler := listenermocks.NewHandler(t)
		handler.On("HandleDesiredRunnerCount", mock.Anything, sessionStatistics.TotalAssignedJobs, 0).
			Return(sessionStatistics.TotalAssignedJobs, nil).
			Once()

		l, err := New(config)
		assert.Nil(t, err)
		assert.NotNil(t, l)

		assert.ErrorIs(t, context.Canceled, l.Listen(ctx, handler))
	})
}

func TestHandleMessageMetrics(t *testing.T) {
	t.Parallel()

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
	jobsStarted := []*actions.JobStarted{
		{
			JobMessageBase: actions.JobMessageBase{
				JobMessageType: actions.JobMessageType{
					MessageType: messageTypeJobStarted,
				},
				RunnerRequestId: 8,
			},
			RunnerId:   3,
			RunnerName: "runner3",
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
		{
			JobMessageBase: actions.JobMessageBase{
				JobMessageType: actions.JobMessageType{
					MessageType: messageTypeJobCompleted,
				},
				RunnerRequestId: 7,
			},
			Result:     "success",
			RunnerId:   2,
			RunnerName: "runner2",
		},
	}
	for _, msg := range jobsCompleted {
		batchedMessages = append(batchedMessages, msg)
	}

	b, err := json.Marshal(batchedMessages)
	require.NoError(t, err)

	msg.Body = string(b)

	desiredResult := 4

	metrics := metricsmocks.NewPublisher(t)
	metrics.On("PublishStatic", 0, 0).Once()
	metrics.On("PublishStatistics", msg.Statistics).Once()
	metrics.On("PublishJobCompleted", jobsCompleted[0]).Once()
	metrics.On("PublishJobCompleted", jobsCompleted[1]).Once()
	metrics.On("PublishJobStarted", jobsStarted[0]).Once()
	metrics.On("PublishDesiredRunners", desiredResult).Once()

	handler := listenermocks.NewHandler(t)
	handler.On("HandleJobStarted", mock.Anything, jobsStarted[0]).Return(nil).Once()
	handler.On("HandleDesiredRunnerCount", mock.Anything, mock.Anything, 2).Return(desiredResult, nil).Once()

	client := listenermocks.NewClient(t)
	client.On("DeleteMessage", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	config := Config{
		Client:     listenermocks.NewClient(t),
		ScaleSetID: 1,
		Metrics:    metrics,
	}

	l, err := New(config)
	require.NoError(t, err)
	l.client = client
	l.session = &actions.RunnerScaleSetSession{
		OwnerName:               "",
		RunnerScaleSet:          &actions.RunnerScaleSet{},
		MessageQueueUrl:         "",
		MessageQueueAccessToken: "",
		Statistics:              &actions.RunnerScaleSetStatistic{},
	}

	err = l.handleMessage(context.Background(), handler, msg)
	require.NoError(t, err)
}
