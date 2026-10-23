package actionsgithubcom

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"go.uber.org/multierr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func newRetryTestClient(t *testing.T, handler http.HandlerFunc) multiclient.Client {
	t.Helper()

	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())))
	token := "eyJhbGciOiJSUzI1NiJ9." + claims + ".c2lnbmF0dXJl"
	server := httptest.NewTLSServer(nil)
	t.Cleanup(server.Close)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runners/registration-token"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"registration-token"}`))
		case strings.HasSuffix(r.URL.Path, "/actions/runner-registration"):
			require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
				"url": server.URL + "/tenant/123/", "token": token,
			}))
		default:
			handler(w, r)
		}
	})
	return newRetryTestClientForServer(t, server)
}

func newRetryTestClientForServer(t *testing.T, server *httptest.Server) multiclient.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client, err := multiclient.NewScaleset().GetClientFor(t.Context(), &multiclient.ClientForOptions{
		GithubConfigURL: server.URL + "/org", Namespace: "default",
		AppConfig: appconfig.AppConfig{Token: "token"}, RootCAs: pool,
	})
	require.NoError(t, err)
	return client
}

func TestScalesetRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		retryAfter string
		reset      bool
		slow       bool
		attempts   int32
		retries    int
		minElapsed time.Duration
		maxElapsed time.Duration
	}{
		{name: "long secondary limit", status: 429, retryAfter: "60", attempts: 1, maxElapsed: 2 * time.Second},
		{name: "wait reaches budget", status: 429, retryAfter: "10", attempts: 1, maxElapsed: 2 * time.Second},
		{name: "primary reset", status: 403, reset: true, attempts: 1, maxElapsed: 2 * time.Second},
		{name: "secondary forbidden", status: 403, retryAfter: "60", attempts: 1, maxElapsed: 2 * time.Second},
		{name: "two short retries", status: 429, retryAfter: "1", attempts: 3, retries: 2, minElapsed: 2 * time.Second, maxElapsed: 5 * time.Second},
		{name: "total budget", status: 429, retryAfter: "6", attempts: 2, retries: 1, minElapsed: 6 * time.Second, maxElapsed: 9 * time.Second},
		{name: "no delay hint", status: 429, attempts: 3, retries: 2, maxElapsed: 6 * time.Second},
		{name: "transient server failure", status: 503, attempts: 3, minElapsed: 3 * time.Second, maxElapsed: 6 * time.Second},
		{name: "slow attempt", slow: true, attempts: 1, minElapsed: 10 * time.Second, maxElapsed: 13 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			client := newRetryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				if tc.slow {
					select {
					case <-r.Context().Done():
					case <-time.After(12 * time.Second):
					}
					return
				}
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				if tc.reset {
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Minute).Unix()))
				}
				w.WriteHeader(tc.status)
			})

			start := time.Now()
			_, err := client.GenerateJitRunnerConfig(t.Context(), &scaleset.RunnerScaleSetJitRunnerSetting{Name: "runner"}, 1)
			elapsed := time.Since(start)
			require.Error(t, err)
			require.Equal(t, tc.attempts, attempts.Load())
			require.GreaterOrEqual(t, elapsed, tc.minElapsed)
			require.Less(t, elapsed, tc.maxElapsed)
			if tc.slow {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else if tc.status != 503 {
				var rateLimit *scaleset.RateLimitError
				require.ErrorAs(t, err, &rateLimit)
				require.Equal(t, tc.retries, rateLimit.Retries)
				if tc.retryAfter != "" {
					want, parseErr := time.ParseDuration(tc.retryAfter + "s")
					require.NoError(t, parseErr)
					require.Equal(t, want, rateLimit.RetryAfter)
				}
			}
		})
	}
}

func TestRequeueOnRateLimit(t *testing.T) {
	limited := &scaleset.RateLimitError{RetryAfter: 45 * time.Second}
	ordinary := errors.New("service failure")
	for _, tc := range []struct {
		name string
		err  error
		wait time.Duration
	}{
		{name: "success"},
		{name: "ordinary error", err: ordinary},
		{name: "wrapped rate limit", err: fmt.Errorf("generate JIT: %w", limited), wait: 45 * time.Second},
		{name: "missing hint", err: &scaleset.RateLimitError{}, wait: time.Minute},
		{name: "negative hint", err: &scaleset.RateLimitError{RetryAfter: -time.Second}, wait: time.Minute},
		{name: "multiple limits", err: multierr.Combine(limited, &scaleset.RateLimitError{RetryAfter: 90 * time.Second}), wait: 90 * time.Second},
		{name: "mixed errors", err: multierr.Combine(limited, ordinary), wait: 45 * time.Second},
		{name: "ordinary error before rate limit", err: multierr.Combine(ordinary, limited), wait: 45 * time.Second},
		{name: "wrapped aggregate", err: fmt.Errorf("cleanup: %w", multierr.Combine(ordinary, limited, &scaleset.RateLimitError{RetryAfter: 90 * time.Second})), wait: 90 * time.Second},
		{name: "joined errors", err: errors.Join(limited, ordinary, &scaleset.RateLimitError{RetryAfter: 90 * time.Second}), wait: 90 * time.Second},
		{name: "individual call cancellation", err: fmt.Errorf("%w: %w", limited, context.Canceled), wait: 45 * time.Second},
		{name: "individual call deadline", err: multierr.Combine(limited, context.DeadlineExceeded), wait: 45 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := ctrl.Result{RequeueAfter: time.Second}
			result, err := requeueOnRateLimit(t.Context(), original, tc.err, logr.Discard())
			if tc.wait > 0 {
				require.NoError(t, err)
				require.Equal(t, tc.wait, result.RequeueAfter)
			} else {
				require.Equal(t, original, result)
				require.Equal(t, tc.err, err)
			}
		})
	}
}

func TestRequeueOnRateLimitPreservesReconcileCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	original := ctrl.Result{RequeueAfter: time.Second}
	failure := multierr.Combine(&scaleset.RateLimitError{RetryAfter: time.Minute}, context.Canceled)
	result, err := requeueOnRateLimit(ctx, original, failure, logr.Discard())
	require.Equal(t, original, result)
	require.Equal(t, failure, err)
}

func TestScalesetAuthenticationDoesNotBlockOtherCalls(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	defer close(release)
	service := newRetryTestClientForServer(t, server)
	backgroundCtx, cancelBackground := context.WithCancel(t.Context())
	defer cancelBackground()
	backgroundDone := make(chan error, 1)
	go func() { backgroundDone <- service.RemoveRunner(backgroundCtx, 7) }()
	<-started

	controllerCtx, cancelController := context.WithTimeout(t.Context(), time.Second)
	defer cancelController()
	start := time.Now()
	_, err := service.GenerateJitRunnerConfig(controllerCtx, &scaleset.RunnerScaleSetJitRunnerSetting{Name: "runner"}, 1)
	var limited *scaleset.RateLimitError
	require.ErrorAs(t, err, &limited)
	require.Equal(t, time.Minute, limited.RetryAfter)
	require.Less(t, time.Since(start), time.Second)
	select {
	case err := <-backgroundDone:
		t.Fatalf("background authentication should still be active: %v", err)
	default:
	}
	cancelBackground()
	require.ErrorIs(t, <-backgroundDone, context.Canceled)
}

func TestScalesetCallsRemainConcurrent(t *testing.T) {
	const concurrency = 8
	started := make(chan struct{}, concurrency)
	release := make(chan struct{})
	releaseCalls := sync.OnceFunc(func() { close(release) })
	defer releaseCalls()
	service := newRetryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"id":7,"name":"runner"}`))
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, concurrency)
	for range concurrency {
		go func() {
			_, err := service.GetRunner(ctx, 7)
			done <- err
		}()
	}
	for range concurrency {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("service calls were serialized or blocked during authentication")
		}
	}
	releaseCalls()
	for range concurrency {
		require.NoError(t, <-done)
	}
}

func TestScalesetCallDeadlineIncludesAuthentication(t *testing.T) {
	var serviceRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/runners/registration-token") && !strings.HasSuffix(r.URL.Path, "/actions/runner-registration") {
			serviceRequests.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		timer := time.NewTimer(60 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		if strings.HasSuffix(r.URL.Path, "/runners/registration-token") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"registration-token"}`))
		}
	}))
	defer server.Close()
	service := newRetryTestClientForServer(t, server)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := service.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{Name: "runner"}, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
	require.Zero(t, serviceRequests.Load(), "authentication must not restart the call's deadline")
}

func TestScalesetServiceCallsRespectCallerDeadline(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	service := newRetryTestClientForServer(t, server)
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"JIT config", func(ctx context.Context) error {
			_, err := service.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{Name: "runner"}, 1)
			return err
		}},
		{"get runner", func(ctx context.Context) error {
			_, err := service.GetRunner(ctx, 7)
			return err
		}},
		{"get runner by name", func(ctx context.Context) error {
			_, err := service.GetRunnerByName(ctx, "runner")
			return err
		}},
		{"remove runner", func(ctx context.Context) error { return service.RemoveRunner(ctx, 7) }},
		{"get group", func(ctx context.Context) error {
			_, err := service.GetRunnerGroupByName(ctx, "group")
			return err
		}},
		{"get set", func(ctx context.Context) error {
			_, err := service.GetRunnerScaleSet(ctx, 1, "set")
			return err
		}},
		{"get set by ID", func(ctx context.Context) error {
			_, err := service.GetRunnerScaleSetByID(ctx, 1)
			return err
		}},
		{"create set", func(ctx context.Context) error {
			_, err := service.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{Name: "set"})
			return err
		}},
		{"update set", func(ctx context.Context) error {
			_, err := service.UpdateRunnerScaleSet(ctx, 1, &scaleset.RunnerScaleSet{Name: "set"})
			return err
		}},
		{"delete set", func(ctx context.Context) error { return service.DeleteRunnerScaleSet(ctx, 1) }},
		{"message session", func(ctx context.Context) error {
			_, err := service.MessageSessionClient(ctx, 1, "listener")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			require.ErrorIs(t, tc.call(ctx), context.DeadlineExceeded)
			require.Less(t, time.Since(start), time.Second)
		})
	}
}

func TestScalesetRetryDuringAuthentication(t *testing.T) {
	for _, stage := range []string{"registration token", "admin token"} {
		t.Run(stage, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "admin token" && strings.HasSuffix(r.URL.Path, "/runners/registration-token") {
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"token":"registration-token"}`))
					return
				}
				attempts.Add(1)
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			pool := x509.NewCertPool()
			pool.AddCert(server.Certificate())
			client, err := multiclient.NewScaleset().GetClientFor(t.Context(), &multiclient.ClientForOptions{
				GithubConfigURL: server.URL + "/org", Namespace: "default",
				AppConfig: appconfig.AppConfig{Token: "token"}, RootCAs: pool,
			})
			require.NoError(t, err)

			start := time.Now()
			_, err = client.GenerateJitRunnerConfig(t.Context(), &scaleset.RunnerScaleSetJitRunnerSetting{Name: "runner"}, 1)
			var limited *scaleset.RateLimitError
			require.ErrorAs(t, err, &limited)
			require.Equal(t, time.Minute, limited.RetryAfter)
			require.EqualValues(t, 1, attempts.Load())
			require.Less(t, time.Since(start), 2*time.Second)
		})
	}
}
