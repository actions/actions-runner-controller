package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/actions/actions-runner-controller/cmd/githubrunnerscalesetlistener/config"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
)

func TestCustomerServerRootCA(t *testing.T) {
	ctx := context.Background()
	certsFolder := filepath.Join(
		"../../",
		"github",
		"actions",
		"testdata",
	)
	certPath := filepath.Join(certsFolder, "server.crt")
	keyPath := filepath.Join(certsFolder, "server.key")

	serverCalledSuccessfully := false

	server := testserver.NewUnstarted(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalledSuccessfully = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count": 0}`))
	}))
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)

	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()

	var certsString string
	rootCA, err := os.ReadFile(filepath.Join(certsFolder, "rootCA.crt"))
	require.NoError(t, err)
	certsString = string(rootCA)

	intermediate, err := os.ReadFile(filepath.Join(certsFolder, "intermediate.pem"))
	require.NoError(t, err)
	certsString = certsString + string(intermediate)

	config := config.Config{
		ConfigureUrl: server.ConfigURLForOrg("myorg"),
		ServerRootCA: certsString,
	}
	creds := &actions.ActionsAuth{
		Token: "token",
	}

	client, err := newActionsClientFromConfig(config, creds)
	require.NoError(t, err)
	_, err = client.GetRunnerScaleSet(ctx, 1, "test")
	require.NoError(t, err)
	assert.True(t, serverCalledSuccessfully)
}

func TestProxySettings(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		wentThroughProxy := false

		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wentThroughProxy = true
		}))
		t.Cleanup(func() {
			proxy.Close()
		})

		prevProxy := os.Getenv("http_proxy")
		os.Setenv("http_proxy", proxy.URL)
		defer os.Setenv("http_proxy", prevProxy)

		config := config.Config{
			ConfigureUrl: "https://github.com/org/repo",
		}
		creds := &actions.ActionsAuth{
			Token: "token",
		}

		client, err := newActionsClientFromConfig(config, creds)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)
		_, err = client.Do(req)
		require.NoError(t, err)

		assert.True(t, wentThroughProxy)
	})

	t.Run("https", func(t *testing.T) {
		wentThroughProxy := false

		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wentThroughProxy = true
		}))
		t.Cleanup(func() {
			proxy.Close()
		})

		prevProxy := os.Getenv("https_proxy")
		os.Setenv("https_proxy", proxy.URL)
		defer os.Setenv("https_proxy", prevProxy)

		config := config.Config{
			ConfigureUrl: "https://github.com/org/repo",
		}
		creds := &actions.ActionsAuth{
			Token: "token",
		}

		client, err := newActionsClientFromConfig(config, creds, actions.WithRetryMax(0))
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)

		_, err = client.Do(req)
		// proxy doesn't support https
		assert.Error(t, err)
		assert.True(t, wentThroughProxy)
	})

	t.Run("no_proxy", func(t *testing.T) {
		wentThroughProxy := false

		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wentThroughProxy = true
		}))
		t.Cleanup(func() {
			proxy.Close()
		})

		prevProxy := os.Getenv("http_proxy")
		os.Setenv("http_proxy", proxy.URL)
		defer os.Setenv("http_proxy", prevProxy)

		prevNoProxy := os.Getenv("no_proxy")
		os.Setenv("no_proxy", "example.com")
		defer os.Setenv("no_proxy", prevNoProxy)

		config := config.Config{
			ConfigureUrl: "https://github.com/org/repo",
		}
		creds := &actions.ActionsAuth{
			Token: "token",
		}

		client, err := newActionsClientFromConfig(config, creds)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)

		_, err = client.Do(req)
		require.NoError(t, err)
		assert.False(t, wentThroughProxy)
	})
}
