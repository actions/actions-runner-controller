package actions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMessage(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	runnerScaleSetMessage := &actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "rssType",
	}

	t.Run("Get Runner Scale Set Message", func(t *testing.T) {
		want := runnerScaleSetMessage
		response := []byte(`{"messageId":1,"messageType":"rssType"}`)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}

		got, err := actionsClient.GetMessage(context.Background(), s.URL, token, 0)
		if err != nil {
			t.Fatalf("GetMessage got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetMessage mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("Default retries on server error", func(t *testing.T) {
		retryClient := retryablehttp.NewClient()
		retryClient.RetryWaitMax = 1 * time.Nanosecond
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

		_, _ = actionsClient.GetMessage(context.Background(), s.URL, token, 0)

		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})

	t.Run("Custom retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryMax := 1
		retryWaitMax := 1 * time.Nanosecond
		actionsClient := actions.Client{
			ActionsServiceURL:        &s.URL,
			ActionsServiceAdminToken: &token,
			RetryMax:                 &retryMax,
			RetryWaitMax:             &retryWaitMax,
		}
		_, _ = actionsClient.GetMessage(context.Background(), s.URL, token, 0)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("Message token expired", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetMessage(context.Background(), s.URL, token, 0)
		if err == nil {
			t.Fatalf("GetMessage did not get exepected error, ")
		}
		var expectedErr *actions.MessageQueueTokenExpiredError
		require.True(t, errors.As(err, &expectedErr))
	},
	)

	t.Run("Status code not found", func(t *testing.T) {
		want := actions.ActionsError{
			Message:    "Request returned status: 404 Not Found",
			StatusCode: 404,
		}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetMessage(context.Background(), s.URL, token, 0)
		if err == nil {
			t.Fatalf("GetMessage did not get exepected error, ")
		}
		if diff := cmp.Diff(want.Error(), err.Error()); diff != "" {
			t.Errorf("GetMessage mismatch (-want +got):\n%s", diff)
		}
	},
	)

	t.Run("Error when Content-Type is text/plain", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Set("Content-Type", "text/plain")
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetMessage(context.Background(), s.URL, token, 0)
		if err == nil {
			t.Fatalf("GetMessage did not get exepected error,")
		}
	},
	)
}

func TestDeleteMessage(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	runnerScaleSetMessage := &actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "rssType",
	}

	t.Run("Delete existing message", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err := actionsClient.DeleteMessage(context.Background(), s.URL, token, runnerScaleSetMessage.MessageId)
		if err != nil {
			t.Fatalf("DeleteMessage got unexepected error, %v", err)
		}
	},
	)

	t.Run("Message token expired", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err := actionsClient.DeleteMessage(context.Background(), s.URL, token, 0)
		if err == nil {
			t.Fatalf("DeleteMessage did not get exepected error, ")
		}
		var expectedErr *actions.MessageQueueTokenExpiredError
		require.True(t, errors.As(err, &expectedErr))
	},
	)

	t.Run("Error when Content-Type is text/plain", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Set("Content-Type", "text/plain")
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err := actionsClient.DeleteMessage(context.Background(), s.URL, token, runnerScaleSetMessage.MessageId)
		if err == nil {
			t.Fatalf("DeleteMessage did not get exepected error")
		}
		var expectedErr *actions.ActionsError
		require.True(t, errors.As(err, &expectedErr))
	},
	)

	t.Run("Default retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryClient := retryablehttp.NewClient()
		retryMax := 1
		retryClient.RetryWaitMax = time.Nanosecond
		retryClient.RetryMax = retryMax
		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_ = actionsClient.DeleteMessage(context.Background(), s.URL, token, runnerScaleSetMessage.MessageId)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("No message found", func(t *testing.T) {
		want := (*actions.RunnerScaleSetMessage)(nil)
		rsl, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("%v", err)
		}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(rsl)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err = actionsClient.DeleteMessage(context.Background(), s.URL, token, runnerScaleSetMessage.MessageId+1)
		var expectedErr *actions.ActionsError
		require.True(t, errors.As(err, &expectedErr))
	},
	)
}
