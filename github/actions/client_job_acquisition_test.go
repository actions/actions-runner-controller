package actions_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireJobs(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Acquire Job", func(t *testing.T) {
		want := []int64{1}
		response := []byte(`{"value": [1]}`)

		session := &actions.RunnerScaleSetSession{
			RunnerScaleSet:          &actions.RunnerScaleSet{Id: 1},
			MessageQueueAccessToken: "abc",
		}
		requestIDs := want

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/acquirablejobs") {
				w.Write([]byte(`{"count": 1}`))
				return
			}

			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		_, err = client.GetAcquirableJobs(ctx, 1)
		require.NoError(t, err)

		got, err := client.AcquireJobs(ctx, session.RunnerScaleSet.Id, session.MessageQueueAccessToken, requestIDs)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		session := &actions.RunnerScaleSetSession{
			RunnerScaleSet:          &actions.RunnerScaleSet{Id: 1},
			MessageQueueAccessToken: "abc",
		}
		var requestIDs = []int64{1}

		retryMax := 1
		actualRetry := 0
		expectedRetry := retryMax + 1

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/acquirablejobs") {
				w.Write([]byte(`{"count": 1}`))
				return
			}

			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))

		client, err := actions.NewClient(
			server.configURLForOrg("my-org"),
			auth,
			actions.WithRetryMax(retryMax),
			actions.WithRetryWaitMax(1*time.Millisecond),
		)
		require.NoError(t, err)

		_, err = client.GetAcquirableJobs(ctx, 1)
		require.NoError(t, err)

		_, err = client.AcquireJobs(context.Background(), session.RunnerScaleSet.Id, session.MessageQueueAccessToken, requestIDs)
		assert.NotNil(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})

	t.Run("Should return MessageQueueTokenExpiredError when http error is not Unauthorized", func(t *testing.T) {
		want := []int64{1}

		session := &actions.RunnerScaleSetSession{
			RunnerScaleSet:          &actions.RunnerScaleSet{Id: 1},
			MessageQueueAccessToken: "abc",
		}
		requestIDs := want

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/acquirablejobs") {
				w.Write([]byte(`{"count": 1}`))
				return
			}
			if r.Method == http.MethodPost {
				http.Error(w, "Session expired", http.StatusUnauthorized)
				return
			}
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		_, err = client.GetAcquirableJobs(ctx, 1)
		require.NoError(t, err)

		got, err := client.AcquireJobs(ctx, session.RunnerScaleSet.Id, session.MessageQueueAccessToken, requestIDs)
		require.Error(t, err)
		assert.Nil(t, got)
		var expectedErr *actions.MessageQueueTokenExpiredError
		assert.True(t, errors.As(err, &expectedErr))
	})
}

func TestGetAcquirableJobs(t *testing.T) {
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Acquire Job", func(t *testing.T) {
		want := &actions.AcquirableJobList{}
		response := []byte(`{"count": 0}`)

		runnerScaleSet := &actions.RunnerScaleSet{Id: 1}

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetAcquirableJobs(context.Background(), runnerScaleSet.Id)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		runnerScaleSet := &actions.RunnerScaleSet{Id: 1}

		retryMax := 1

		actualRetry := 0
		expectedRetry := retryMax + 1

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))

		client, err := actions.NewClient(
			server.configURLForOrg("my-org"),
			auth,
			actions.WithRetryMax(retryMax),
			actions.WithRetryWaitMax(1*time.Millisecond),
		)
		require.NoError(t, err)

		_, err = client.GetAcquirableJobs(context.Background(), runnerScaleSet.Id)
		require.Error(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}
