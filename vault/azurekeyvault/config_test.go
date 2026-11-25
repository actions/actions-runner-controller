package azurekeyvault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http/httpproxy"
)

func TestConfigValidate_invalid(t *testing.T) {
	tenantID := "tenantID"
	clientID := "clientID"
	url := "https://example.com"

	cp, err := os.CreateTemp("", "")
	require.NoError(t, err)
	err = cp.Close()
	require.NoError(t, err)
	certPath := cp.Name()

	t.Cleanup(func() {
		os.Remove(certPath)
	})

	tt := map[string]*Config{
		"empty": {},
		"no tenant id": {
			TenantID:        "",
			ClientID:        clientID,
			URL:             url,
			CertificatePath: certPath,
		},
		"no client id": {
			TenantID:        tenantID,
			ClientID:        "",
			URL:             url,
			CertificatePath: certPath,
		},
		"no url": {
			TenantID:        tenantID,
			ClientID:        clientID,
			URL:             "",
			CertificatePath: certPath,
		},
		"cert path that does not exist": {
			TenantID:        tenantID,
			ClientID:        clientID,
			URL:             url,
			CertificatePath: certPath + ".missing",
		},
		"invalid proxy": {
			TenantID:        tenantID,
			ClientID:        clientID,
			URL:             url,
			CertificatePath: certPath,
			Proxy:           &httpproxy.Config{},
		},
	}

	for name, cfg := range tt {
		t.Run(name, func(t *testing.T) {
			err := cfg.Validate()
			require.Error(t, err)
		})
	}
}

func TestValidate_valid(t *testing.T) {
	tenantID := "tenantID"
	clientID := "clientID"
	url := "https://example.com"

	certPath, err := filepath.Abs("testdata/server.crt")
	require.NoError(t, err)

	tt := map[string]*Config{
		"with cert": {
			TenantID:        tenantID,
			ClientID:        clientID,
			URL:             url,
			CertificatePath: certPath,
		},
		"without proxy": {
			TenantID:        tenantID,
			ClientID:        clientID,
			URL:             url,
			CertificatePath: certPath,
		},
	}

	for name, cfg := range tt {
		t.Run(name, func(t *testing.T) {
			err := cfg.Validate()
			require.NoError(t, err)
		})
	}
}

func TestValidate_validWithoutCert(t *testing.T) {
	cfg := &Config{TenantID: "tenantID", ClientID: "clientID", URL: "https://example.com"}
	require.NoError(t, cfg.Validate())
}

func TestClient_workloadIdentity(t *testing.T) {
	cfg := &Config{TenantID: "tenantID", ClientID: "clientID", URL: "https://example.com"}

	t.Run("token file set", func(t *testing.T) {
		tokenFile := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte("jwt"), 0o600))
		t.Setenv("AZURE_FEDERATED_TOKEN_FILE", tokenFile)

		client, err := cfg.Client()
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("token file not set", func(t *testing.T) {
		t.Setenv("AZURE_FEDERATED_TOKEN_FILE", "")

		_, err := cfg.Client()
		require.Error(t, err)
	})
}
