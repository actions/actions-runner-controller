package config_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/require"
)

var discardLogger = slog.New(slog.DiscardHandler)

func TestProxySettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme string
		env    string
	}{
		{name: "http", scheme: "http", env: "http_proxy"},
		{name: "https", scheme: "https", env: "https_proxy"},
		{name: "no_proxy", scheme: "https", env: "https_proxy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, env := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY"} {
				t.Setenv(env, "")
			}
			var requests atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer proxy.Close()
			t.Setenv(tc.env, proxy.URL)
			if tc.name == "no_proxy" {
				t.Setenv("no_proxy", "example.com")
			}

			config := config.Config{
				ConfigureURL: tc.scheme + "://github.com/org/repo",
				AppConfig:    &appconfig.AppConfig{Token: "token"},
			}
			client, err := config.ActionsClient(discardLogger, scaleset.WithRetry(scaleset.RetryConfig{Max: 0}))
			require.NoError(t, err)

			_, err = client.GenerateJitRunnerConfig(t.Context(), &scaleset.RunnerScaleSetJitRunnerSetting{Name: "runner"}, 1)
			require.Error(t, err)
			require.EqualValues(t, 1, requests.Load(), "the request must use the configured proxy")
		})
	}
}
