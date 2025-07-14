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
		"no jwt and no cert path": {
			TenantID:        tenantID,
			ClientID:        clientID,
			URL:             url,
			CertificatePath: "",
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
