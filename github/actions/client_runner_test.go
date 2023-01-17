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

var tokenExpireAt = time.Now().Add(10 * time.Minute)

func TestGetRunner(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"

	t.Run("Get Runner", func(t *testing.T) {
		name := "Get Runner"
		var runnerID int64 = 1
		want := &actions.RunnerReference{
			Id:   int(runnerID),
			Name: "self-hosted-ubuntu",
		}
		response := []byte(`{"id": 1, "name": "self-hosted-ubuntu"}`)

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		got, err := actionsClient.GetRunner(context.Background(), runnerID)
		if err != nil {
			t.Fatalf("GetRunner got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunner(%v) mismatch (-want +got):\n%s", name, diff)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		var runnerID int64 = 1
		retryClient := retryablehttp.NewClient()
		retryClient.RetryWaitMax = 1 * time.Millisecond
		retryClient.RetryMax = 1

		actualRetry := 0
		expectedRetry := retryClient.RetryMax + 1

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		_, _ = actionsClient.GetRunner(context.Background(), runnerID)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}

func TestGetRunnerByName(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"

	t.Run("Get Runner by Name", func(t *testing.T) {
		var runnerID int64 = 1
		var runnerName string = "self-hosted-ubuntu"
		want := &actions.RunnerReference{
			Id:   int(runnerID),
			Name: runnerName,
		}
		response := []byte(`{"count": 1, "value": [{"id": 1, "name": "self-hosted-ubuntu"}]}`)

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		got, err := actionsClient.GetRunnerByName(context.Background(), runnerName)
		if err != nil {
			t.Fatalf("GetRunnerByName got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunnerByName(%v) mismatch (-want +got):\n%s", runnerName, diff)
		}
	})

	t.Run("Get Runner by name with not exist runner", func(t *testing.T) {
		var runnerName string = "self-hosted-ubuntu"
		response := []byte(`{"count": 0, "value": []}`)

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		got, err := actionsClient.GetRunnerByName(context.Background(), runnerName)
		if err != nil {
			t.Fatalf("GetRunnerByName got unexepected error, %v", err)
		}

		if diff := cmp.Diff((*actions.RunnerReference)(nil), got); diff != "" {
			t.Errorf("GetRunnerByName(%v) mismatch (-want +got):\n%s", runnerName, diff)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		var runnerName string = "self-hosted-ubuntu"
		retryClient := retryablehttp.NewClient()
		retryClient.RetryWaitMax = 1 * time.Millisecond
		retryClient.RetryMax = 1

		actualRetry := 0
		expectedRetry := retryClient.RetryMax + 1

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		_, _ = actionsClient.GetRunnerByName(context.Background(), runnerName)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}

func TestDeleteRunner(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"

	t.Run("Delete Runner", func(t *testing.T) {
		var runnerID int64 = 1

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		if err := actionsClient.RemoveRunner(context.Background(), runnerID); err != nil {
			t.Fatalf("RemoveRunner got unexepected error, %v", err)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		var runnerID int64 = 1

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

		_ = actionsClient.RemoveRunner(context.Background(), runnerID)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})
}
