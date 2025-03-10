package actions_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerWithSelfSignedCertificates(t *testing.T) {
	ctx := context.Background()
	// this handler is a very very barebones replica of actions api
	// used during the creation of a a new client
	var u string
	h := func(w http.ResponseWriter, r *http.Request) {
		// handle get registration token
		if strings.HasSuffix(r.URL.Path, "/runners/registration-token") {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"token":"token"}`))
			return
		}

		// handle getActionsServiceAdminConnection
		if strings.HasSuffix(r.URL.Path, "/actions/runner-registration") {
			claims := &jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Minute)),
				Issuer:    "123",
			}

			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(samplePrivateKey))
			require.NoError(t, err)
			tokenString, err := token.SignedString(privateKey)
			require.NoError(t, err)
			w.Write([]byte(`{"url":"` + u + `","token":"` + tokenString + `"}`))
			return
		}

		// default happy response for RemoveRunner
		w.WriteHeader(http.StatusNoContent)
	}

	certPath := filepath.Join("testdata", "server.crt")
	keyPath := filepath.Join("testdata", "server.key")

	t.Run("client without ca certs", func(t *testing.T) {
		server := startNewTLSTestServer(t, certPath, keyPath, http.HandlerFunc(h))
		u = server.URL
		configURL := server.URL + "/my-org"

		auth := &actions.ActionsAuth{
			Token: "token",
		}
		client, err := actions.NewClient(configURL, auth)
		require.NoError(t, err)
		require.NotNil(t, client)

		err = client.RemoveRunner(ctx, 1)
		require.NotNil(t, err)

		if runtime.GOOS == "linux" {
			assert.True(t, errors.As(err, &x509.UnknownAuthorityError{}))
		}

		// on macOS we only get an untyped error from the system verifying the
		// certificate
		if runtime.GOOS == "darwin" {
			assert.True(t, strings.HasSuffix(err.Error(), "certificate is not trusted"))
		}
	})

	t.Run("client with ca certs", func(t *testing.T) {
		server := startNewTLSTestServer(
			t,
			certPath,
			keyPath,
			http.HandlerFunc(h),
		)
		u = server.URL
		configURL := server.URL + "/my-org"

		auth := &actions.ActionsAuth{
			Token: "token",
		}

		cert, err := os.ReadFile(filepath.Join("testdata", "rootCA.crt"))
		require.NoError(t, err)

		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(cert))

		client, err := actions.NewClient(
			configURL,
			auth,
			actions.WithRootCAs(pool),
		)
		require.NoError(t, err)
		assert.NotNil(t, client)

		err = client.RemoveRunner(ctx, 1)
		assert.NoError(t, err)
	})

	t.Run("client with ca chain certs", func(t *testing.T) {
		server := startNewTLSTestServer(
			t,
			filepath.Join("testdata", "leaf.crt"),
			filepath.Join("testdata", "leaf.key"),
			http.HandlerFunc(h),
		)
		u = server.URL
		configURL := server.URL + "/my-org"

		auth := &actions.ActionsAuth{
			Token: "token",
		}

		cert, err := os.ReadFile(filepath.Join("testdata", "intermediate.crt"))
		require.NoError(t, err)

		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(cert))

		client, err := actions.NewClient(
			configURL,
			auth,
			actions.WithRootCAs(pool),
			actions.WithRetryMax(0),
		)
		require.NoError(t, err)
		require.NotNil(t, client)

		err = client.RemoveRunner(ctx, 1)
		assert.NoError(t, err)
	})

	t.Run("client skipping tls verification", func(t *testing.T) {
		server := startNewTLSTestServer(t, certPath, keyPath, http.HandlerFunc(h))
		configURL := server.URL + "/my-org"

		auth := &actions.ActionsAuth{
			Token: "token",
		}

		client, err := actions.NewClient(configURL, auth, actions.WithoutTLSVerify())
		require.NoError(t, err)
		assert.NotNil(t, client)
	})
}

func startNewTLSTestServer(t *testing.T, certPath, keyPath string, handler http.Handler) *httptest.Server {
	server := httptest.NewUnstartedServer(handler)
	t.Cleanup(func() {
		server.Close()
	})

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)

	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()

	return server
}
