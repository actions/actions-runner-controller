package config_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discardLogger = slog.New(slog.DiscardHandler)

func TestProxySettings(t *testing.T) {
	assertHasProxy := func(t *testing.T, debugInfo string, want bool) {
		type debugInfoContent struct {
			HasProxy bool `json:"has_proxy"`
		}
		var got debugInfoContent
		err := json.Unmarshal([]byte(debugInfo), &got)
		require.NoError(t, err)
		assert.Equal(t, want, got.HasProxy)
	}

	t.Run("http", func(t *testing.T) {
		prevProxy := os.Getenv("http_proxy")
		os.Setenv("http_proxy", "http://proxy:8080")
		defer os.Setenv("http_proxy", prevProxy)

		config := config.Config{
			ConfigureURL: "https://github.com/org/repo",
			AppConfig: &appconfig.AppConfig{
				Token: "token",
			},
		}

		client, err := config.ActionsClient(discardLogger)
		require.NoError(t, err)

		assertHasProxy(t, client.DebugInfo(), true)
	})

	t.Run("https", func(t *testing.T) {
		prevProxy := os.Getenv("https_proxy")
		os.Setenv("https_proxy", "https://proxy:443")
		defer os.Setenv("https_proxy", prevProxy)

		config := config.Config{
			ConfigureURL: "https://github.com/org/repo",
			AppConfig: &appconfig.AppConfig{
				Token: "token",
			},
		}

		client, err := config.ActionsClient(
			discardLogger,
			scaleset.WithRetryMax(0),
		)
		require.NoError(t, err)

		assertHasProxy(t, client.DebugInfo(), true)
	})

	t.Run("no_proxy", func(t *testing.T) {
		prevNoProxy := os.Getenv("no_proxy")
		os.Setenv("no_proxy", "example.com")
		defer os.Setenv("no_proxy", prevNoProxy)

		config := config.Config{
			ConfigureURL: "https://github.com/org/repo",
			AppConfig: &appconfig.AppConfig{
				Token: "token",
			},
		}

		client, err := config.ActionsClient(discardLogger)
		require.NoError(t, err)

		assertHasProxy(t, client.DebugInfo(), true)
	})
}
