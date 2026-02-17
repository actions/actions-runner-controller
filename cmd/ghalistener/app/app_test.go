package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	appmocks "github.com/actions/actions-runner-controller/cmd/ghalistener/app/mocks"
	metricsMocks "github.com/actions/actions-runner-controller/cmd/ghalistener/metrics/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var discardLogger = slog.New(slog.DiscardHandler)

func TestApp_Run(t *testing.T) {
	t.Parallel()

	t.Run("ListenerWorkerGuard", func(t *testing.T) {
		listener := appmocks.NewMockListener(t)
		worker := appmocks.NewMockWorker(t)

		listener.On("Listen", mock.Anything, mock.Anything).Return(nil).Once()

		app := &App{
			logger:   discardLogger,
			listener: listener,
			worker:   worker,
		}

		err := app.Run(context.Background())
		assert.NoError(t, err)
	})

	t.Run("ExitsOnListenerError", func(t *testing.T) {
		listener := appmocks.NewMockListener(t)
		worker := appmocks.NewMockWorker(t)

		listener.On("Listen", mock.Anything, mock.Anything).Return(errors.New("listener error")).Once()

		app := &App{
			logger:   discardLogger,
			listener: listener,
			worker:   worker,
		}

		err := app.Run(context.Background())
		assert.Error(t, err)
	})

	t.Run("ExitsOnListenerNil", func(t *testing.T) {
		listener := appmocks.NewMockListener(t)
		worker := appmocks.NewMockWorker(t)

		listener.On("Listen", mock.Anything, mock.Anything).Return(nil).Once()

		app := &App{
			logger:   discardLogger,
			listener: listener,
			worker:   worker,
		}

		err := app.Run(context.Background())
		assert.NoError(t, err)
	})

	t.Run("CancelListenerOnMetricsServerError", func(t *testing.T) {
		listener := appmocks.NewMockListener(t)
		worker := appmocks.NewMockWorker(t)
		metrics := metricsMocks.NewServerPublisher(t)
		ctx := context.Background()

		listener.On("Listen", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			go func() {
				<-ctx.Done()
			}()
		}).Return(nil).Once()

		metrics.On("ListenAndServe", mock.Anything).Return(errors.New("metrics server error")).Once()

		app := &App{
			logger:   discardLogger,
			listener: listener,
			worker:   worker,
			metrics:  metrics,
		}

		err := app.Run(ctx)
		assert.Error(t, err)
	})
}
