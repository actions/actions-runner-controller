package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/require"
)

type brokerPollHTTPClient func(*http.Request) (*http.Response, error)

func (f brokerPollHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Exercise the public SDK session lifecycle without sockets or credentials.
// synctest advances the real SDK's two-minute attempt timers in virtual time.
func newBrokerPollTestSession(t *testing.T, poll brokerPollHTTPClient) *scaleset.MessageSessionClient {
	t.Helper()
	transport := brokerPollHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v3/orgs/test-org/actions/runners/registration-token":
			return brokerPollResponse(req, http.StatusCreated, `{"token":"test-registration"}`), nil
		case "/api/v3/actions/runner-registration":
			claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())))
			token := "eyJhbGciOiJIUzI1NiJ9." + claims + ".test-signature"
			body, err := json.Marshal(map[string]string{"url": "https://actions.test", "token": token})
			require.NoError(t, err)
			return brokerPollResponse(req, http.StatusOK, string(body)), nil
		case "/_apis/runtime/runnerscalesets/1/sessions", "/_apis/runtime/runnerscalesets/1/sessions/00000000-0000-4000-8000-000000000001":
			if strings.Contains(req.URL.Path, "/sessions/") {
				require.Equal(t, http.MethodPatch, req.Method, "an expired queue token must refresh the session")
			}
			return brokerPollResponse(req, http.StatusOK, `{"sessionId":"00000000-0000-4000-8000-000000000001","messageQueueUrl":"https://broker.test/queue","messageQueueAccessToken":"test-queue","statistics":{"totalAssignedJobs":1}}`), nil
		case "/queue":
			return poll(req)
		default:
			return nil, fmt.Errorf("unexpected test request: %s %s", req.Method, req.URL)
		}
	})
	client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
		GitHubConfigURL: "https://github.test/test-org", PersonalAccessToken: "test-token",
	}, scaleset.WithHTTPClient(transport))
	require.NoError(t, err)
	session, err := client.MessageSessionClient(t.Context(), 1, "test-listener")
	require.NoError(t, err)
	return session
}

func brokerPollResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func getMessageWithBrokerBudget(ctx context.Context, session *scaleset.MessageSessionClient) (*scaleset.RunnerScaleSetMessage, error) {
	return (&brokerMessageClient{MessageSessionClient: session}).GetMessage(ctx, 0, 2)
}

func TestStockBrokerPollRetryDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
			attempts++
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		start := time.Now()
		_, err := session.GetMessage(t.Context(), 0, 2)
		elapsed := time.Since(start)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 5, attempts)
		require.GreaterOrEqual(t, elapsed, 10*time.Minute+15*time.Second)
		require.LessOrEqual(t, elapsed, 10*time.Minute+22500*time.Millisecond)
		t.Logf("stock broker request: %d attempts, %s elapsed", attempts, elapsed)
	})
}

func TestBrokerPollBudgetBoundsStalledAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
			attempts++
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		start := time.Now()
		_, err := getMessageWithBrokerBudget(t.Context(), session)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 3*time.Minute, time.Since(start))
		require.Equal(t, 2, attempts, "the first full attempt must retain its two-minute window")
	})
}

func TestBrokerPollBudgetIncludesSessionRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				// Consume part of the whole-call budget before the SDK refreshes
				// the session and starts its next HTTP request with a new timer.
				time.Sleep(100 * time.Second)
				return brokerPollResponse(req, http.StatusUnauthorized, ""), nil
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		start := time.Now()
		_, err := getMessageWithBrokerBudget(t.Context(), session)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 3*time.Minute, time.Since(start), "refresh must not start a new whole-call budget")
		require.Equal(t, 2, attempts)
	})
}

func TestBrokerPollBudgetPreservesNormalLongPolls(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				attempts := 0
				session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
					attempts++
					time.Sleep(50 * time.Second)
					return brokerPollResponse(req, status, `{"messageId":7,"messageType":"RunnerScaleSetJobMessages","statistics":{"totalAssignedJobs":1}}`), nil
				})
				start := time.Now()
				message, err := getMessageWithBrokerBudget(t.Context(), session)
				require.NoError(t, err)
				require.Equal(t, 50*time.Second, time.Since(start))
				require.Equal(t, 1, attempts)
				if status == http.StatusAccepted {
					require.Nil(t, message)
				} else {
					require.NotNil(t, message)
					require.Equal(t, 7, message.MessageID)
				}
			})
		})
	}
}

func TestBrokerPollBudgetHonorsParentCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
			attempts++
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			time.Sleep(15 * time.Second)
			cancel()
		}()
		start := time.Now()
		_, err := getMessageWithBrokerBudget(ctx, session)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 15*time.Second, time.Since(start))
		require.Equal(t, 1, attempts)
	})
}

func TestBrokerPollBudgetAllowsRecoveryAfterOneStalledAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			time.Sleep(50 * time.Second)
			return brokerPollResponse(req, http.StatusAccepted, ""), nil
		})
		start := time.Now()
		_, err := getMessageWithBrokerBudget(t.Context(), session)
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.GreaterOrEqual(t, time.Since(start), 171*time.Second)
		require.LessOrEqual(t, time.Since(start), 171500*time.Millisecond)
	})
}

func TestBrokerPollBudgetRetriesTransientServiceFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		session := newBrokerPollTestSession(t, func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return brokerPollResponse(req, http.StatusServiceUnavailable, ""), nil
			}
			time.Sleep(50 * time.Second)
			return brokerPollResponse(req, http.StatusAccepted, ""), nil
		})
		start := time.Now()
		_, err := getMessageWithBrokerBudget(t.Context(), session)
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.GreaterOrEqual(t, time.Since(start), 51*time.Second)
		require.LessOrEqual(t, time.Since(start), 51500*time.Millisecond)
	})
}
