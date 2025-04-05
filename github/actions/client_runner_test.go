package actions_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRunner(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Get Runner", func(t *testing.T) {
		var runnerID int64 = 1
		want := &actions.RunnerReference{
			Id:   int(runnerID),
			Name: "self-hosted-ubuntu",
		}
		response := []byte(`{"id": 1, "name": "self-hosted-ubuntu"}`)

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetRunner(ctx, runnerID)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		var runnerID int64 = 1
		retryWaitMax := 1 * time.Millisecond
		retryMax := 1

		actualRetry := 0
		expectedRetry := retryMax + 1

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth, actions.WithRetryMax(retryMax), actions.WithRetryWaitMax(retryWaitMax))
		require.NoError(t, err)

		_, err = client.GetRunner(ctx, runnerID)
		require.Error(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}

func TestGetRunnerByName(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Get Runner by Name", func(t *testing.T) {
		var runnerID int64 = 1
		var runnerName = "self-hosted-ubuntu"
		want := &actions.RunnerReference{
			Id:   int(runnerID),
			Name: runnerName,
		}
		response := []byte(`{"count": 1, "value": [{"id": 1, "name": "self-hosted-ubuntu"}]}`)

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetRunnerByName(ctx, runnerName)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Get Runner by name with not exist runner", func(t *testing.T) {
		var runnerName = "self-hosted-ubuntu"
		response := []byte(`{"count": 0, "value": []}`)

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetRunnerByName(ctx, runnerName)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		var runnerName = "self-hosted-ubuntu"

		retryWaitMax := 1 * time.Millisecond
		retryMax := 1

		actualRetry := 0
		expectedRetry := retryMax + 1

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth, actions.WithRetryMax(retryMax), actions.WithRetryWaitMax(retryWaitMax))
		require.NoError(t, err)

		_, err = client.GetRunnerByName(ctx, runnerName)
		require.Error(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}

func TestDeleteRunner(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Delete Runner", func(t *testing.T) {
		var runnerID int64 = 1

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		err = client.RemoveRunner(ctx, runnerID)
		assert.NoError(t, err)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		var runnerID int64 = 1

		retryWaitMax := 1 * time.Millisecond
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
			actions.WithRetryWaitMax(retryWaitMax),
		)
		require.NoError(t, err)

		err = client.RemoveRunner(ctx, runnerID)
		require.Error(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}

func TestGetRunnerGroupByName(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Get RunnerGroup by Name", func(t *testing.T) {
		var runnerGroupID int64 = 1
		var runnerGroupName = "test-runner-group"
		want := &actions.RunnerGroup{
			ID:   runnerGroupID,
			Name: runnerGroupName,
		}
		response := []byte(`{"count": 1, "value": [{"id": 1, "name": "test-runner-group"}]}`)

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetRunnerGroupByName(ctx, runnerGroupName)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Get RunnerGroup by name with not exist runner group", func(t *testing.T) {
		var runnerGroupName = "test-runner-group"
		response := []byte(`{"count": 0, "value": []}`)

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetRunnerGroupByName(ctx, runnerGroupName)
		assert.ErrorContains(t, err, "no runner group found with name")
		assert.Nil(t, got)
	})
}
