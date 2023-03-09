package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
)

func TestConfigValidationMinMax(t *testing.T) {
	config := &RunnerScaleSetListenerConfig{
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
		MinRunners:                  5,
		MaxRunners:                  2,
		Token:                       "token",
	}
	err := validateConfig(config)
	assert.ErrorContains(t, err, "MinRunners '5' cannot be greater than MaxRunners '2", "Expected error about MinRunners > MaxRunners")
}

func TestConfigValidationMissingToken(t *testing.T) {
	config := &RunnerScaleSetListenerConfig{
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := validateConfig(config)
	expectedError := fmt.Sprintf("GitHub auth credential is missing, token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidationAppKey(t *testing.T) {
	config := &RunnerScaleSetListenerConfig{
		AppID:                       1,
		AppInstallationID:           10,
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := validateConfig(config)
	expectedError := fmt.Sprintf("GitHub auth credential is missing, token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidationOnlyOneTypeOfCredentials(t *testing.T) {
	config := &RunnerScaleSetListenerConfig{
		AppID:                       1,
		AppInstallationID:           10,
		AppPrivateKey:               "asdf",
		Token:                       "asdf",
		ConfigureUrl:                "github.com/some_org/some_repo",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}
	err := validateConfig(config)
	expectedError := fmt.Sprintf("only one GitHub auth method supported at a time. Have both PAT and App auth: token length: '%d', appId: '%d', installationId: '%d', private key length: '%d", len(config.Token), config.AppID, config.AppInstallationID, len(config.AppPrivateKey))
	assert.ErrorContains(t, err, expectedError, "Expected error about missing auth")
}

func TestConfigValidation(t *testing.T) {
	config := &RunnerScaleSetListenerConfig{
		ConfigureUrl:                "https://github.com/actions",
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
		MinRunners:                  1,
		MaxRunners:                  5,
		Token:                       "asdf",
	}

	err := validateConfig(config)

	assert.NoError(t, err, "Expected no error")
}

func TestConfigValidationConfigUrl(t *testing.T) {
	config := &RunnerScaleSetListenerConfig{
		EphemeralRunnerSetNamespace: "namespace",
		EphemeralRunnerSetName:      "deployment",
		RunnerScaleSetId:            1,
	}

	err := validateConfig(config)

	assert.ErrorContains(t, err, "GitHubConfigUrl is not provided", "Expected error about missing ConfigureUrl")
}

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

	config := RunnerScaleSetListenerConfig{
		ConfigureUrl: server.ConfigURLForOrg("myorg"),
		ServerRootCA: certsString,
	}
	creds := &actions.ActionsAuth{
		Token: "token",
	}

	client, err := newActionsClientFromConfig(config, creds)
	require.NoError(t, err)
	_, err = client.GetRunnerScaleSet(ctx, "test")
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

		config := RunnerScaleSetListenerConfig{
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

		config := RunnerScaleSetListenerConfig{
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

		config := RunnerScaleSetListenerConfig{
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
