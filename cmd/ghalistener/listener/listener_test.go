package listener

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	listenermocks "github.com/actions/actions-runner-controller/cmd/ghalistener/listener/mocks"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	metricsmocks "github.com/actions/actions-runner-controller/cmd/ghalistener/metrics/mocks"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var _ = metricsmocks.Publisher{}

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
		client.On("CreateMessageSession", ctx, mock.AnythingOfType("int"), mock.AnythingOfType("string")).Return(nil, assert.AnError).Once()
		config.Client = client

		l, err := New(config)
		assert.Nil(t, err)
		assert.NotNil(t, l)

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
		client.On("CreateMessageSession", ctx, mock.AnythingOfType("int"), mock.AnythingOfType("string")).Return(nil,
			&actions.HttpClientSideError{Code: http.StatusConflict}).Once()
		config.Client = client

		l, err := New(config)
		assert.Nil(t, err)
		assert.NotNil(t, l)

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
		assert.Nil(t, err)

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
		assert.Nil(t, err)
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
		assert.Nil(t, err)

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
		assert.Nil(t, err)

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
		assert.Nil(t, err)

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
		assert.Nil(t, err)

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
		assert.Nil(t, err)

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
