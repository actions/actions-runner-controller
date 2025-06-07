package v1alpha1_test

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
)

func TestGitHubServerTLSConfig_ToCertPool(t *testing.T) {
	t.Run("returns an error if CertificateFrom not specified", func(t *testing.T) {
		c := &v1alpha1.TLSConfig{
			CertificateFrom: nil,
		}

		pool, err := c.ToCertPool(nil)
		assert.Nil(t, pool)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "certificateFrom not specified")
	})

	t.Run("returns an error if CertificateFrom.ConfigMapKeyRef not specified", func(t *testing.T) {
		c := &v1alpha1.TLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{},
		}

		pool, err := c.ToCertPool(nil)
		assert.Nil(t, pool)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "configMapKeyRef not specified")
	})

	t.Run("returns a valid cert pool with correct configuration", func(t *testing.T) {
		c := &v1alpha1.TLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{
						Name: "name",
					},
					Key: "key",
				},
			},
		}

		certsFolder := filepath.Join(
			"../../../",
			"github",
			"actions",
			"testdata",
		)

		fetcher := func(name, key string) ([]byte, error) {
			cert, err := os.ReadFile(filepath.Join(certsFolder, "rootCA.crt"))
			require.NoError(t, err)

			pool := x509.NewCertPool()
			ok := pool.AppendCertsFromPEM(cert)
			assert.True(t, ok)

			return cert, nil
		}

		pool, err := c.ToCertPool(fetcher)
		require.NoError(t, err)
		require.NotNil(t, pool)

		// can be used to communicate with a server
		serverSuccessfullyCalled := false
		server := testserver.NewUnstarted(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverSuccessfullyCalled = true
			w.WriteHeader(http.StatusOK)
		}))

		cert, err := tls.LoadX509KeyPair(
			filepath.Join(certsFolder, "server.crt"),
			filepath.Join(certsFolder, "server.key"),
		)
		require.NoError(t, err)

		server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
		server.StartTLS()

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: pool,
				},
			},
		}

		_, err = client.Get(server.URL)
		assert.NoError(t, err)
		assert.True(t, serverSuccessfullyCalled)
	})
}
