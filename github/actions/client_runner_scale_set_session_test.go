package actions_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCreateMessageSession(t *testing.T) {
	t.Run("CreateMessageSession unmarshals correctly", func(t *testing.T) {
		token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
		owner := "foo"
		runnerScaleSet := actions.RunnerScaleSet{
			Id:            1,
			Name:          "ScaleSet",
			CreatedOn:     time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
			RunnerSetting: actions.RunnerSetting{},
		}

		want := &actions.RunnerScaleSetSession{
			OwnerName: "foo",
			RunnerScaleSet: &actions.RunnerScaleSet{
				Id:   1,
				Name: "ScaleSet",
			},
			MessageQueueUrl:         "http://fake.actions.github.com/123",
			MessageQueueAccessToken: "fake.jwt.here",
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			resp := []byte(`{
					"ownerName": "foo",
					"runnerScaleSet": {
						"id": 1,
						"name": "ScaleSet"
					},
					"messageQueueUrl": "http://fake.actions.github.com/123",
					"messageQueueAccessToken": "fake.jwt.here"
				}`)
			w.Write(resp)
		}))
		defer srv.Close()

		retryMax := 1
		retryWaitMax := 1 * time.Microsecond

		actionsClient := actions.Client{
			ActionsServiceURL:                 &srv.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}

		got, err := actionsClient.CreateMessageSession(context.Background(), runnerScaleSet.Id, owner)
		if err != nil {
			t.Fatalf("CreateMessageSession got unexpected error: %v", err)
		}

		if diff := cmp.Diff(got, want); diff != "" {
			t.Fatalf("CreateMessageSession got unexpected diff: -want +got: %v", diff)
		}
	})

	t.Run("CreateMessageSession unmarshals errors into ActionsError", func(t *testing.T) {
		token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
		owner := "foo"
		runnerScaleSet := actions.RunnerScaleSet{
			Id:            1,
			Name:          "ScaleSet",
			CreatedOn:     time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
			RunnerSetting: actions.RunnerSetting{},
		}

		want := &actions.ActionsError{
			ExceptionName: "CSharpExceptionNameHere",
			Message:       "could not do something",
			StatusCode:    http.StatusBadRequest,
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			resp := []byte(`{"typeName": "CSharpExceptionNameHere","message": "could not do something"}`)
			w.Write(resp)
		}))
		defer srv.Close()

		retryMax := 1
		retryWaitMax := 1 * time.Microsecond

		actionsClient := actions.Client{
			ActionsServiceURL:                 &srv.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}

		got, err := actionsClient.CreateMessageSession(context.Background(), runnerScaleSet.Id, owner)
		if err == nil {
			t.Fatalf("CreateMessageSession did not get expected error: %v", got)
		}

		errorTypeForComparison := &actions.ActionsError{}
		if isActionsError := errors.As(err, &errorTypeForComparison); !isActionsError {
			t.Fatalf("CreateMessageSession expected to be able to parse the error into ActionsError type: %v", err)
		}

		gotErr := err.(*actions.ActionsError)

		if diff := cmp.Diff(want, gotErr); diff != "" {
			t.Fatalf("CreateMessageSession got unexpected diff: -want +got: %v", diff)
		}
	})

	t.Run("CreateMessageSession call is retried the correct amount of times", func(t *testing.T) {
		token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
		owner := "foo"
		runnerScaleSet := actions.RunnerScaleSet{
			Id:            1,
			Name:          "ScaleSet",
			CreatedOn:     time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
			RunnerSetting: actions.RunnerSetting{},
		}

		gotRetries := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			gotRetries++
		}))
		defer srv.Close()

		retryMax := 3
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}

		wantRetries := retryMax + 1

		actionsClient := actions.Client{
			ActionsServiceURL:                 &srv.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}

		_, _ = actionsClient.CreateMessageSession(context.Background(), runnerScaleSet.Id, owner)

		assert.Equalf(t, gotRetries, wantRetries, "CreateMessageSession got unexpected retry count: got=%v, want=%v", gotRetries, wantRetries)
	})
}

func TestDeleteMessageSession(t *testing.T) {
	t.Run("DeleteMessageSession call is retried the correct amount of times", func(t *testing.T) {
		token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
		runnerScaleSet := actions.RunnerScaleSet{
			Id:            1,
			Name:          "ScaleSet",
			CreatedOn:     time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
			RunnerSetting: actions.RunnerSetting{},
		}

		gotRetries := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			gotRetries++
		}))
		defer srv.Close()

		retryMax := 3
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}

		wantRetries := retryMax + 1

		actionsClient := actions.Client{
			ActionsServiceURL:                 &srv.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}

		sessionId := uuid.New()

		_ = actionsClient.DeleteMessageSession(context.Background(), runnerScaleSet.Id, &sessionId)

		assert.Equalf(t, gotRetries, wantRetries, "CreateMessageSession got unexpected retry count: got=%v, want=%v", gotRetries, wantRetries)
	})
}

func TestRefreshMessageSession(t *testing.T) {
	t.Run("RefreshMessageSession call is retried the correct amount of times", func(t *testing.T) {
		token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
		runnerScaleSet := actions.RunnerScaleSet{
			Id:            1,
			Name:          "ScaleSet",
			CreatedOn:     time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
			RunnerSetting: actions.RunnerSetting{},
		}

		gotRetries := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			gotRetries++
		}))
		defer srv.Close()

		retryMax := 3
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}

		wantRetries := retryMax + 1

		actionsClient := actions.Client{
			ActionsServiceURL:                 &srv.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}

		sessionId := uuid.New()

		_, _ = actionsClient.RefreshMessageSession(context.Background(), runnerScaleSet.Id, &sessionId)

		assert.Equalf(t, gotRetries, wantRetries, "CreateMessageSession got unexpected retry count: got=%v, want=%v", gotRetries, wantRetries)
	})
}
