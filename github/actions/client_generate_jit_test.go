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

func TestGenerateJitRunnerConfig(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	t.Run("Get JIT Config for Runner", func(t *testing.T) {
		want := &actions.RunnerScaleSetJitRunnerConfig{}
		response := []byte(`{"count":1,"value":[{"id":1,"name":"scale-set-name"}]}`)

		runnerSettings := &actions.RunnerScaleSetJitRunnerSetting{}

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))
		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GenerateJitRunnerConfig(ctx, runnerSettings, 1)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		runnerSettings := &actions.RunnerScaleSetJitRunnerSetting{}

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
			actions.WithRetryMax(1),
			actions.WithRetryWaitMax(1*time.Millisecond),
		)
		require.NoError(t, err)

		_, err = client.GenerateJitRunnerConfig(ctx, runnerSettings, 1)
		assert.NotNil(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})

	t.Run("Error includes HTTP method and URL when request fails", func(t *testing.T) {
		runnerSettings := &actions.RunnerScaleSetJitRunnerSetting{}

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))

		client, err := actions.NewClient(
			server.configURLForOrg("my-org"),
			auth,
			actions.WithRetryMax(0), // No retries to get immediate error
			actions.WithRetryWaitMax(1*time.Millisecond),
		)
		require.NoError(t, err)

		_, err = client.GenerateJitRunnerConfig(ctx, runnerSettings, 1)
		require.NotNil(t, err)
		// Verify error message includes HTTP method and URL for better debugging
		assert.Contains(t, err.Error(), "POST")
		assert.Contains(t, err.Error(), "generatejitconfig")
		// The status code will be included through ParseActionsErrorFromResponse
		var actionsErr *actions.ActionsError
		if assert.ErrorAs(t, err, &actionsErr) {
			assert.Equal(t, http.StatusInternalServerError, actionsErr.StatusCode)
		}
	})
}
