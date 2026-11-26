package ratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTransport struct {
	status int
	header http.Header
	body   string
	err    error
}

func (s stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	header := s.header
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: s.status, Header: header, Body: io.NopCloser(strings.NewReader(s.body)), Request: req}, nil
}

func send(t *testing.T, stub stubTransport, url string) []Response {
	t.Helper()
	var got []Response
	client := &http.Client{Transport: Transport{Transport: stub, Observe: func(r Response) { got = append(got, r) }}}
	resp, err := client.Get(url)
	if stub.err != nil {
		require.Error(t, err)
		return got
	}
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, stub.body, string(body), "the caller still reads the whole body")
	return got
}

func rateLimitHeader(remaining, resource string) http.Header {
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", remaining)
	h.Set("X-RateLimit-Resource", resource)
	return h
}

func TestTransport(t *testing.T) {
	tests := []struct {
		name string
		url  string
		stub stubTransport
		want Response
	}{
		{
			name: "github api",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 201, header: rateLimitHeader("4990", "core")},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 201, Resource: "core"},
		},
		{
			name: "github enterprise server api",
			url:  "https://ghes.example.com/api/v3/repos/o/r/actions/runners/registration-token",
			stub: stubTransport{status: 201},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 201},
		},
		{
			name: "actions service",
			url:  "https://pipelines.actions.githubusercontent.com/abc/_apis/runtime/runnerscalesets/1",
			stub: stubTransport{status: 200},
			want: Response{Backend: BackendActions, Operation: OperationScaleSet, StatusCode: 200},
		},
		{
			name: "actions service on the broker host",
			url:  "https://broker.actions.githubusercontent.com/rest/_apis/distributedtask/pools/0/agents/7",
			stub: stubTransport{status: 200},
			want: Response{Backend: BackendActions, Operation: OperationRunner, StatusCode: 200},
		},
		{
			name: "broker secondary limit",
			url:  "https://broker.actions.githubusercontent.com/scalesets/message?sessionId=1",
			stub: stubTransport{status: 429},
			want: Response{Backend: BackendBroker, Operation: OperationMessage, StatusCode: 429, RateLimited: Secondary},
		},
		{
			name: "too many runners configured",
			url:  "https://broker.actions.githubusercontent.com/rest/_apis/runtime/runnerscalesets/42/generatejitconfig?api-version=6.0-preview",
			stub: stubTransport{status: 429, body: `{"typeName":"Microsoft.TeamFoundation.DistributedTask.WebApi.ConfigureTooManyRunnersException, Microsoft.TeamFoundation.DistributedTask.WebApi","message":"You have configured more than 1500 runners within the last 5 minutes."}`},
			want: Response{Backend: BackendActions, Operation: OperationJITConfig, StatusCode: 429, RateLimited: Secondary, Reason: "ConfigureTooManyRunnersException"},
		},
		{
			name: "spent primary quota",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, header: rateLimitHeader("0", "actions_runner_registration")},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403, RateLimited: Primary, Resource: "actions_runner_registration"},
		},
		{
			name: "403 with Retry-After is a secondary limit",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, header: http.Header{"Retry-After": []string{"60"}}},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403, RateLimited: Secondary},
		},
		{
			name: "secondary limit named only in the body",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, body: `{"message":"You have exceeded a secondary rate limit."}`},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403, RateLimited: Secondary},
		},
		{
			name: "secondary message wins over spent quota",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, header: rateLimitHeader("0", "core"), body: `{"message":"You have exceeded a secondary rate limit."}`},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403, RateLimited: Secondary, Resource: "core"},
		},
		{
			name: "escaped secondary message in JSON",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, body: `{"message":"You have exceeded a secondary\u0020rate limit."}`},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403, RateLimited: Secondary},
		},
		{
			name: "secondary rate outside the JSON message is not a rate limit",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, body: `{"message":"Forbidden","detail":"secondary rate"}`},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403},
		},
		{
			name: "plain text secondary message",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403, header: http.Header{"Content-Type": []string{"text/plain"}}, body: "You have exceeded a secondary rate limit."},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403, RateLimited: Secondary},
		},
		{
			name: "plain 403 is not a rate limit",
			url:  "https://api.github.com/orgs/o/actions/runners/registration-token",
			stub: stubTransport{status: 403},
			want: Response{Backend: BackendREST, Operation: OperationRegistrationToken, StatusCode: 403},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := send(t, tt.stub, tt.url)
			require.Len(t, got, 1)
			assert.Equal(t, tt.want, got[0])
		})
	}
}

func TestOperations(t *testing.T) {
	tests := map[string]Operation{
		"https://api.github.com/app/installations/1/access_tokens":                        OperationAccessToken,
		"https://api.github.com/repos/o/r/actions/runners/registration-token":             OperationRegistrationToken,
		"https://api.github.com/actions/runner-registration":                              OperationRunnerRegistration,
		"https://pipelines.example/abc/_apis/runtime/runnerscalesets?name=s":              OperationScaleSet,
		"https://pipelines.example/abc/_apis/runtime/runnerscalesets/1":                   OperationScaleSet,
		"https://pipelines.example/abc/_apis/runtime/runnergroups/?groupName=g":           OperationRunnerGroup,
		"https://pipelines.example/abc/_apis/runtime/runnerscalesets/1/sessions":          OperationSession,
		"https://pipelines.example/abc/_apis/runtime/runnerscalesets/1/sessions/9b2b":     OperationSession,
		"https://pipelines.example/abc/_apis/runtime/runnerscalesets/1/acquirejobs":       OperationAcquireJobs,
		"https://pipelines.example/abc/_apis/runtime/runnerscalesets/1/generatejitconfig": OperationJITConfig,
		"https://pipelines.example/abc/_apis/distributedtask/pools/0/agents?agentName=r":  OperationRunner,
		"https://broker.actions.githubusercontent.com/scalesets/message/12":               OperationMessage,
		"https://api.github.com/meta":                                                     OperationOther,
	}
	for url, want := range tests {
		got := send(t, stubTransport{status: 200}, url)
		require.Len(t, got, 1)
		assert.Equal(t, want, got[0].Operation, url)
	}
}

func TestTransportReportsFailedAttempts(t *testing.T) {
	got := send(t, stubTransport{err: errors.New("connection reset")}, "https://broker.actions.githubusercontent.com/scalesets/message")
	require.Len(t, got, 1)
	assert.Equal(t, Response{Backend: BackendBroker, Operation: OperationMessage}, got[0])
	assert.Equal(t, "error", got[0].Status())
}

func TestTransportSkipsCanceledRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var got []Response
	transport := Transport{Transport: stubTransport{err: context.Canceled}, Observe: func(r Response) { got = append(got, r) }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://broker.actions.githubusercontent.com/message", nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.Error(t, err)
	assert.Empty(t, got)
}

type truncatedBody struct {
	data     io.Reader
	reported bool
}

func (b *truncatedBody) Read(p []byte) (int, error) {
	n, err := b.data.Read(p)
	if err == io.EOF && !b.reported {
		b.reported = true
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (*truncatedBody) Close() error { return nil }

type truncatedTransport struct{}

func (truncatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       &truncatedBody{data: strings.NewReader(`{"message":"secondary rate`)},
		Request:    req,
	}, nil
}

func TestTransportKeepsBodyReadErrors(t *testing.T) {
	transport := Transport{Transport: truncatedTransport{}, Observe: func(Response) {}}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.Equal(t, `{"message":"secondary rate`, string(body))
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
