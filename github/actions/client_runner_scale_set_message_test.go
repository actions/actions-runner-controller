package actions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMessage(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	runnerScaleSetMessage := &actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "rssType",
	}

	t.Run("Get Runner Scale Set Message", func(t *testing.T) {
		want := runnerScaleSetMessage
		response := []byte(`{"messageId":1,"messageType":"rssType"}`)
		s := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(response)
		}))

		client, err := actions.NewClient(s.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetMessage(ctx, s.URL, token, 0)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("GetMessage sets the last message id if not 0", func(t *testing.T) {
		want := runnerScaleSetMessage
		response := []byte(`{"messageId":1,"messageType":"rssType"}`)
		s := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "1", q.Get("lastMessageId"))
			w.Write(response)
		}))

		client, err := actions.NewClient(s.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		got, err := client.GetMessage(ctx, s.URL, token, 1)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("Default retries on server error", func(t *testing.T) {
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

		_, err = client.GetMessage(ctx, server.URL, token, 0)
		assert.NotNil(t, err)
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})

	t.Run("Message token expired", func(t *testing.T) {
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		_, err = client.GetMessage(ctx, server.URL, token, 0)
		require.NotNil(t, err)

		var expectedErr *actions.MessageQueueTokenExpiredError
		require.True(t, errors.As(err, &expectedErr))
	})

	t.Run("Status code not found", func(t *testing.T) {
		want := actions.ActionsError{
			Err:        errors.New("unknown exception"),
			StatusCode: 404,
		}
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		_, err = client.GetMessage(ctx, server.URL, token, 0)
		require.NotNil(t, err)
		assert.Equal(t, want.Error(), err.Error())
	})

	t.Run("Error when Content-Type is text/plain", func(t *testing.T) {
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Set("Content-Type", "text/plain")
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		_, err = client.GetMessage(ctx, server.URL, token, 0)
		assert.NotNil(t, err)
	})
}

func TestDeleteMessage(t *testing.T) {
	ctx := context.Background()
	auth := &actions.ActionsAuth{
		Token: "token",
	}

	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	runnerScaleSetMessage := &actions.RunnerScaleSetMessage{
		MessageId:   1,
		MessageType: "rssType",
	}

	t.Run("Delete existing message", func(t *testing.T) {
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		err = client.DeleteMessage(ctx, server.URL, token, runnerScaleSetMessage.MessageId)
		assert.Nil(t, err)
	})

	t.Run("Message token expired", func(t *testing.T) {
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		err = client.DeleteMessage(ctx, server.URL, token, 0)
		require.NotNil(t, err)
		var expectedErr *actions.MessageQueueTokenExpiredError
		assert.True(t, errors.As(err, &expectedErr))
	})

	t.Run("Error when Content-Type is text/plain", func(t *testing.T) {
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Set("Content-Type", "text/plain")
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		err = client.DeleteMessage(ctx, server.URL, token, runnerScaleSetMessage.MessageId)
		require.NotNil(t, err)
		var expectedErr *actions.ActionsError
		assert.True(t, errors.As(err, &expectedErr))
	},
	)

	t.Run("Default retries on server error", func(t *testing.T) {
		actualRetry := 0
		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))

		retryMax := 1
		client, err := actions.NewClient(
			server.configURLForOrg("my-org"),
			auth,
			actions.WithRetryMax(retryMax),
			actions.WithRetryWaitMax(1*time.Nanosecond),
		)
		require.NoError(t, err)
		err = client.DeleteMessage(ctx, server.URL, token, runnerScaleSetMessage.MessageId)
		assert.NotNil(t, err)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	})

	t.Run("No message found", func(t *testing.T) {
		want := (*actions.RunnerScaleSetMessage)(nil)
		rsl, err := json.Marshal(want)
		require.NoError(t, err)

		server := newActionsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(rsl)
		}))

		client, err := actions.NewClient(server.configURLForOrg("my-org"), auth)
		require.NoError(t, err)

		err = client.DeleteMessage(ctx, server.URL, token, runnerScaleSetMessage.MessageId+1)
		var expectedErr *actions.ActionsError
		require.True(t, errors.As(err, &expectedErr))
	})
}
