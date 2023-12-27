package listener

import (
	"context"
	"testing"

	listenermocks "github.com/actions/actions-runner-controller/cmd/ghalistener/listener/mocks"
	metricsmocks "github.com/actions/actions-runner-controller/cmd/ghalistener/metrics/mocks"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		config.Client = client

		handler := listenermocks.NewHandler(t)
		handler.On("HandleDesiredRunnerCount", mock.Anything, sessionStatistics.TotalAssignedJobs).
			Return(sessionStatistics.TotalAssignedJobs, nil).
			Once()

		l, err := New(config)
		assert.Nil(t, err)
		assert.NotNil(t, l)

		assert.ErrorIs(t, context.Canceled, l.Listen(ctx, handler))
	})
}
