package actions_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
)

func TestGenerateJitRunnerConfig(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"

	t.Run("Get JIT Config for Runner", func(t *testing.T) {
		name := "Get JIT Config for Runner"
		want := &actions.RunnerScaleSetJitRunnerConfig{}
		response := []byte(`{"count":1,"value":[{"id":1,"name":"scale-set-name"}]}`)

		runnerSettings := &actions.RunnerScaleSetJitRunnerSetting{}

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		got, err := actionsClient.GenerateJitRunnerConfig(context.Background(), runnerSettings, 1)
		if err != nil {
			t.Fatalf("GenerateJitRunnerConfig got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GenerateJitRunnerConfig(%v) mismatch (-want +got):\n%s", name, diff)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		runnerSettings := &actions.RunnerScaleSetJitRunnerSetting{}

		retryClient := retryablehttp.NewClient()
		retryClient.RetryWaitMax = 1 * time.Millisecond
		retryClient.RetryMax = 1

		actualRetry := 0
		expectedRetry := retryClient.RetryMax + 1

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()

		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		_, _ = actionsClient.GenerateJitRunnerConfig(context.Background(), runnerSettings, 1)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}
