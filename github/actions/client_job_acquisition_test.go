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

func TestAcquireJobs(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"

	t.Run("Acquire Job", func(t *testing.T) {
		name := "Acquire Job"

		want := []int64{1}
		response := []byte(`{"value": [1]}`)

		session := &actions.RunnerScaleSetSession{
			RunnerScaleSet:          &actions.RunnerScaleSet{Id: 1},
			MessageQueueAccessToken: "abc",
		}
		requestIDs := want

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:        &s.URL,
			ActionsServiceAdminToken: &token,
		}

		got, err := actionsClient.AcquireJobs(context.Background(), session.RunnerScaleSet.Id, session.MessageQueueAccessToken, requestIDs)
		if err != nil {
			t.Fatalf("CreateRunnerScaleSet got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunnerScaleSet(%v) mismatch (-want +got):\n%s", name, diff)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		session := &actions.RunnerScaleSetSession{
			RunnerScaleSet:          &actions.RunnerScaleSet{Id: 1},
			MessageQueueAccessToken: "abc",
		}
		var requestIDs []int64 = []int64{1}

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
			Client:                   httpClient,
			ActionsServiceURL:        &s.URL,
			ActionsServiceAdminToken: &token,
		}

		_, _ = actionsClient.AcquireJobs(context.Background(), session.RunnerScaleSet.Id, session.MessageQueueAccessToken, requestIDs)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}

func TestGetAcquirableJobs(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"

	t.Run("Acquire Job", func(t *testing.T) {
		name := "Acquire Job"

		want := &actions.AcquirableJobList{}
		response := []byte(`{"count": 0}`)

		runnerScaleSet := &actions.RunnerScaleSet{Id: 1}

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		got, err := actionsClient.GetAcquirableJobs(context.Background(), runnerScaleSet.Id)
		if err != nil {
			t.Fatalf("GetAcquirableJobs got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetAcquirableJobs(%v) mismatch (-want +got):\n%s", name, diff)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		runnerScaleSet := &actions.RunnerScaleSet{Id: 1}

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

		_, _ = actionsClient.GetAcquirableJobs(context.Background(), runnerScaleSet.Id)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}
