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
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: nil,
		}

		pool, err := c.ToCertPool(nil)
		assert.Nil(t, pool)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "certificateFrom not specified")
	})

	t.Run("returns an error if CertificateFrom.ConfigMapKeyRef not specified", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{},
		}

		pool, err := c.ToCertPool(nil)
		assert.Nil(t, pool)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "configMapKeyRef not specified")
	})

	t.Run("returns a valid cert pool with correct configuration", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
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

func TestGitHubServerTLSConfig_ToVolume(t *testing.T) {
	t.Run("returns an error if CertificateFrom not specified", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: nil,
		}

		volume, err := c.ToVolume()
		assert.Nil(t, volume)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "certificateFrom not specified")
	})

	t.Run("returns an error if CertificateFrom.ConfigMapKeyRef not specified", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{},
		}

		volume, err := c.ToVolume()
		assert.Nil(t, volume)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "configMapKeyRef not specified")
	})

	t.Run("returns a volume with correct configuration", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{
						Name: "name",
					},
					Key: "ca.pem",
				},
			},
		}

		volume, err := c.ToVolume()
		require.NoError(t, err)
		require.NotNil(t, volume)
		assert.Equal(t, volume.Name, "github-server-tls-cert")
		require.Len(t, volume.VolumeSource.ConfigMap.Items, 1)
		assert.Equal(t, volume.VolumeSource.ConfigMap.Items[0].Key, "ca.pem")
		assert.Equal(t, volume.VolumeSource.ConfigMap.Items[0].Path, "ca.pem")
	})
}

func TestGitHubServerTLSConfig_ToVolumeMount(t *testing.T) {
	t.Run("returns an error if CertificateFrom not specified", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: nil,
		}

		mount, err := c.ToVolumeMount()
		assert.Nil(t, mount)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "certificateFrom not specified")
	})

	t.Run("returns an error if CertificateFrom.ConfigMapKeyRef not specified", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{},
		}

		mount, err := c.ToVolumeMount()
		assert.Nil(t, mount)

		require.Error(t, err)
		assert.Equal(t, err.Error(), "configMapKeyRef not specified")
	})

	t.Run("returns a volume mount with correct configuration", func(t *testing.T) {
		c := &v1alpha1.GitHubServerTLSConfig{
			CertificateFrom: &v1alpha1.TLSCertificateSource{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{
						Name: "name",
					},
					Key: "ca.pem",
				},
			},
			RunnerMountPath: "/path/to/runner/certs",
		}

		mount, err := c.ToVolumeMount()
		require.NoError(t, err)
		require.NotNil(t, mount)
		assert.Equal(t, mount.Name, "github-server-tls-cert")
		assert.Equal(t, mount.MountPath, "/path/to/runner/certs/ca.pem")
		assert.Equal(t, mount.SubPath, "ca.pem")
	})
}
