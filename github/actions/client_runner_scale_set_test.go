package actions_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRunnerScaleSet(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	scaleSetName := "ScaleSet"
	runnerScaleSet := actions.RunnerScaleSet{Id: 1, Name: scaleSetName}

	t.Run("Get existing scale set", func(t *testing.T) {
		want := &runnerScaleSet
		runnerScaleSetsResp := []byte(`{"count":1,"value":[{"id":1,"name":"ScaleSet"}]}`)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(runnerScaleSetsResp)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		got, err := actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)
		if err != nil {
			t.Fatalf("CreateRunnerScaleSet got unexepected error, %v", err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunnerScaleSet(%v) mismatch (-want +got):\n%s", scaleSetName, diff)
		}
	},
	)

	t.Run("GetRunnerScaleSet calls correct url", func(t *testing.T) {
		runnerScaleSetsResp := []byte(`{"count":1,"value":[{"id":1,"name":"ScaleSet"}]}`)
		url := url.URL{}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(runnerScaleSetsResp)
			url = *r.URL
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)
		if err != nil {
			t.Fatalf("CreateRunnerScaleSet got unexepected error, %v", err)
		}

		u := url.String()
		expectedUrl := fmt.Sprintf("/_apis/runtime/runnerscalesets?name=%s&api-version=6.0-preview", scaleSetName)
		assert.Equal(t, expectedUrl, u)

	},
	)

	t.Run("Status code not found", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)
		if err == nil {
			t.Fatalf("GetRunnerScaleSet did not get exepected error, ")
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
		_, err := actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)
		if err == nil {
			t.Fatalf("GetRunnerScaleSet did not get exepected error,")
		}
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
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		retryClient.RetryWaitMax = retryWaitMax
		retryClient.RetryMax = retryMax
		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, _ = actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("Custom retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryMax := 1
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}
		_, _ = actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("RunnerScaleSet count is zero", func(t *testing.T) {
		want := (*actions.RunnerScaleSet)(nil)
		runnerScaleSetsResp := []byte(`{"count":0,"value":[{"id":1,"name":"ScaleSet"}]}`)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(runnerScaleSetsResp)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		got, _ := actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunnerScaleSet(%v) mismatch (-want +got):\n%s", scaleSetName, diff)
		}

	},
	)

	t.Run("Multiple runner scale sets found", func(t *testing.T) {
		wantErr := fmt.Errorf("multiple runner scale sets found with name %s", scaleSetName)
		runnerScaleSetsResp := []byte(`{"count":2,"value":[{"id":1,"name":"ScaleSet"}]}`)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(runnerScaleSetsResp)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetRunnerScaleSet(context.Background(), scaleSetName)

		if err == nil {
			t.Fatalf("GetRunnerScaleSet did not get exepected error, %v", wantErr)
		}

		if diff := cmp.Diff(wantErr.Error(), err.Error()); diff != "" {
			t.Errorf("GetRunnerScaleSet(%v) mismatch (-want +got):\n%s", scaleSetName, diff)
		}

	},
	)
}

func TestGetRunnerScaleSetById(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	scaleSetCreationDateTime := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	runnerScaleSet := actions.RunnerScaleSet{Id: 1, Name: "ScaleSet", CreatedOn: scaleSetCreationDateTime, RunnerSetting: actions.RunnerSetting{}}

	t.Run("Get existing scale set by Id", func(t *testing.T) {
		want := &runnerScaleSet
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
		got, err := actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)
		if err != nil {
			t.Fatalf("GetRunnerScaleSetById got unexepected error, %v", err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunnerScaleSetById(%d) mismatch (-want +got):\n%s", runnerScaleSet.Id, diff)
		}
	},
	)

	t.Run("GetRunnerScaleSetById calls correct url", func(t *testing.T) {
		rsl, err := json.Marshal(&runnerScaleSet)
		if err != nil {
			t.Fatalf("%v", err)
		}
		url := url.URL{}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(rsl)
			url = *r.URL
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err = actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)
		if err != nil {
			t.Fatalf("GetRunnerScaleSetById got unexepected error, %v", err)
		}

		u := url.String()
		expectedUrl := fmt.Sprintf("/_apis/runtime/runnerscalesets/%d?api-version=6.0-preview", runnerScaleSet.Id)
		assert.Equal(t, expectedUrl, u)

	},
	)

	t.Run("Status code not found", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)
		if err == nil {
			t.Fatalf("GetRunnerScaleSetById did not get exepected error, ")
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
		_, err := actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)
		if err == nil {
			t.Fatalf("GetRunnerScaleSetById did not get exepected error,")
		}
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
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		retryClient.RetryWaitMax = retryWaitMax
		retryClient.RetryMax = retryMax
		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, _ = actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("Custom retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryMax := 1
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}
		_, _ = actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("No RunnerScaleSet found", func(t *testing.T) {
		want := (*actions.RunnerScaleSet)(nil)
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
		got, _ := actionsClient.GetRunnerScaleSetById(context.Background(), runnerScaleSet.Id)

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GetRunnerScaleSetById(%v) mismatch (-want +got):\n%s", runnerScaleSet.Id, diff)
		}

	},
	)
}

func TestCreateRunnerScaleSet(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	scaleSetCreationDateTime := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	runnerScaleSet := actions.RunnerScaleSet{Id: 1, Name: "ScaleSet", CreatedOn: scaleSetCreationDateTime, RunnerSetting: actions.RunnerSetting{}}

	t.Run("Create runner scale set", func(t *testing.T) {
		want := &runnerScaleSet
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
		got, err := actionsClient.CreateRunnerScaleSet(context.Background(), &runnerScaleSet)
		if err != nil {
			t.Fatalf("CreateRunnerScaleSet got exepected error, %v", err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("CreateRunnerScaleSet(%d) mismatch (-want +got):\n%s", runnerScaleSet.Id, diff)
		}
	},
	)

	t.Run("CreateRunnerScaleSet calls correct url", func(t *testing.T) {
		rsl, err := json.Marshal(&runnerScaleSet)
		if err != nil {
			t.Fatalf("%v", err)
		}
		url := url.URL{}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(rsl)
			url = *r.URL
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err = actionsClient.CreateRunnerScaleSet(context.Background(), &runnerScaleSet)
		if err != nil {
			t.Fatalf("CreateRunnerScaleSet got unexepected error, %v", err)
		}

		u := url.String()
		expectedUrl := "/_apis/runtime/runnerscalesets?api-version=6.0-preview"
		assert.Equal(t, expectedUrl, u)

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
		_, err := actionsClient.CreateRunnerScaleSet(context.Background(), &runnerScaleSet)
		if err == nil {
			t.Fatalf("CreateRunnerScaleSet did not get exepected error, %v", &actions.ActionsError{})
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
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		retryClient.RetryMax = retryMax
		retryClient.RetryWaitMax = retryWaitMax

		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, _ = actionsClient.CreateRunnerScaleSet(context.Background(), &runnerScaleSet)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("Custom retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryMax := 1
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}
		_, _ = actionsClient.CreateRunnerScaleSet(context.Background(), &runnerScaleSet)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)
}

func TestUpdateRunnerScaleSet(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	scaleSetCreationDateTime := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	runnerScaleSet := actions.RunnerScaleSet{Id: 1, Name: "ScaleSet", CreatedOn: scaleSetCreationDateTime, RunnerSetting: actions.RunnerSetting{}}

	t.Run("Update existing scale set", func(t *testing.T) {
		want := &runnerScaleSet
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
		got, err := actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, want)
		if err != nil {
			t.Fatalf("UpdateRunnerScaleSet got exepected error, %v", err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("UpdateRunnerScaleSet(%d) mismatch (-want +got):\n%s", runnerScaleSet.Id, diff)
		}
	},
	)

	t.Run("UpdateRunnerScaleSet calls correct url", func(t *testing.T) {
		rsl, err := json.Marshal(&runnerScaleSet)
		if err != nil {
			t.Fatalf("%v", err)
		}
		url := url.URL{}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(rsl)
			url = *r.URL
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err = actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, &runnerScaleSet)
		if err != nil {
			t.Fatalf("UpdateRunnerScaleSet got unexepected error, %v", err)
		}

		u := url.String()
		expectedUrl := fmt.Sprintf("/_apis/runtime/runnerscalesets/%d?api-version=6.0-preview", runnerScaleSet.Id)
		assert.Equal(t, expectedUrl, u)

	},
	)

	t.Run("Status code not found", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, err := actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, &runnerScaleSet)
		if err == nil {
			t.Fatalf("UpdateRunnerScaleSet did not get exepected error,")
		}
		var expectedErr *actions.ActionsError
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
		_, err := actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, &runnerScaleSet)
		if err == nil {
			t.Fatalf("UpdateRunnerScaleSet did not get exepected error")
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
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		retryClient.RetryWaitMax = retryWaitMax
		retryClient.RetryMax = retryMax
		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_, _ = actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, &runnerScaleSet)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("Custom retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryMax := 1
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}
		_, _ = actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, &runnerScaleSet)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("No RunnerScaleSet found", func(t *testing.T) {
		want := (*actions.RunnerScaleSet)(nil)
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
		got, err := actionsClient.UpdateRunnerScaleSet(context.Background(), runnerScaleSet.Id, &runnerScaleSet)
		if err != nil {
			t.Fatalf("UpdateRunnerScaleSet got unexepected error, %v", err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("UpdateRunnerScaleSet(%v) mismatch (-want +got):\n%s", runnerScaleSet.Id, diff)
		}

	},
	)
}

func TestDeleteRunnerScaleSet(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
	scaleSetCreationDateTime := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	runnerScaleSet := actions.RunnerScaleSet{Id: 1, Name: "ScaleSet", CreatedOn: scaleSetCreationDateTime, RunnerSetting: actions.RunnerSetting{}}

	t.Run("Delete existing scale set", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer s.Close()

		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err := actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		if err != nil {
			t.Fatalf("DeleteRunnerScaleSet got unexepected error, %v", err)
		}
	},
	)

	t.Run("DeleteRunnerScaleSet calls correct url", func(t *testing.T) {
		url := url.URL{}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
			url = *r.URL
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err := actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		if err != nil {
			t.Fatalf("DeleteRunnerScaleSet got unexepected error, %v", err)
		}

		u := url.String()
		expectedUrl := fmt.Sprintf("/_apis/runtime/runnerscalesets/%d?api-version=6.0-preview", runnerScaleSet.Id)
		assert.Equal(t, expectedUrl, u)

	},
	)

	t.Run("Status code not found", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer s.Close()
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		err := actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		if err == nil {
			t.Fatalf("DeleteRunnerScaleSet did not get exepected error, ")
		}
		var expectedErr *actions.ActionsError
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
		err := actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		if err == nil {
			t.Fatalf("DeleteRunnerScaleSet did not get exepected error")
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
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		retryClient.RetryWaitMax = retryWaitMax
		retryClient.RetryMax = retryMax
		httpClient := retryClient.StandardClient()
		actionsClient := actions.Client{
			Client:                            httpClient,
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
		}
		_ = actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("Custom retries on server error", func(t *testing.T) {
		actualRetry := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			actualRetry++
		}))
		defer s.Close()
		retryMax := 1
		retryWaitMax, err := time.ParseDuration("1µs")
		if err != nil {
			t.Fatalf("%v", err)
		}
		actionsClient := actions.Client{
			ActionsServiceURL:                 &s.URL,
			ActionsServiceAdminToken:          &token,
			ActionsServiceAdminTokenExpiresAt: &tokenExpireAt,
			RetryMax:                          &retryMax,
			RetryWaitMax:                      &retryWaitMax,
		}
		_ = actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		expectedRetry := retryMax + 1
		assert.Equalf(t, actualRetry, expectedRetry, "A retry was expected after the first request but got: %v", actualRetry)
	},
	)

	t.Run("No RunnerScaleSet found", func(t *testing.T) {
		want := (*actions.RunnerScaleSet)(nil)
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
		err = actionsClient.DeleteRunnerScaleSet(context.Background(), runnerScaleSet.Id)
		var expectedErr *actions.ActionsError
		require.True(t, errors.As(err, &expectedErr))
	},
	)
}
