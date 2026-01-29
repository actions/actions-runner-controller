package actions_test

import (
	"context"
	"errors"
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
		errMsg := err.Error()
		assert.Contains(t, errMsg, "POST", "Error message should include HTTP method")
		assert.Contains(t, errMsg, "generatejitconfig", "Error message should include URL path")

		// The error might be an ActionsError (if response was received) or a wrapped error (if Do() failed)
		// In either case, the error message should include request details
		var actionsErr *actions.ActionsError
		if errors.As(err, &actionsErr) {
			// If we got an ActionsError, verify the status code is included
			assert.Equal(t, http.StatusInternalServerError, actionsErr.StatusCode)
		}
		// If it's a wrapped error from Do(), the error message already includes the method and URL
		// which is what we're testing for
	})
}
