package azurekeyvault

import (
	"os"
	"testing"

	"github.com/actions/actions-runner-controller/proxyconfig"
	"github.com/stretchr/testify/require"
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

	proxy := &proxyconfig.ProxyConfig{
		HTTP: &proxyconfig.ProxyServerConfig{
			URL:      "http://httpconfig.com",
			Username: "user",
			Password: "pass",
		},
		HTTPS: &proxyconfig.ProxyServerConfig{
			URL:      "https://httpsconfig.com",
			Username: "user",
			Password: "pass",
		},
		NoProxy: []string{
			"http://noproxy.com",
		},
	}

	tt := map[string]*Config{
		"empty": {},
		"no tenant id": {
			TenantID:     "",
			ClientID:     clientID,
			URL:          url,
			CertPath:     certPath,
			CertPassword: "",
			Proxy:        proxy,
		},
		"no client id": {
			TenantID:     tenantID,
			ClientID:     "",
			URL:          url,
			CertPath:     certPath,
			CertPassword: "",
			Proxy:        proxy,
		},
		"no url": {
			TenantID:     tenantID,
			ClientID:     clientID,
			URL:          "",
			CertPath:     certPath,
			CertPassword: "",
			Proxy:        proxy,
		},
		"no jwt and no cert path": {
			TenantID:     tenantID,
			ClientID:     clientID,
			URL:          url,
			CertPath:     "",
			CertPassword: "",
			Proxy:        proxy,
		},
		"invalid proxy": {
			TenantID:     tenantID,
			ClientID:     clientID,
			URL:          url,
			CertPath:     certPath,
			CertPassword: "",
			Proxy: &proxyconfig.ProxyConfig{
				HTTP: &proxyconfig.ProxyServerConfig{},
			},
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

	cp, err := os.CreateTemp("", "")
	require.NoError(t, err)
	err = cp.Close()
	require.NoError(t, err)
	certPath := cp.Name()

	t.Cleanup(func() {
		os.Remove(certPath)
	})

	proxy := &proxyconfig.ProxyConfig{
		HTTP: &proxyconfig.ProxyServerConfig{
			URL:      "http://httpconfig.com",
			Username: "user",
			Password: "pass",
		},
		HTTPS: &proxyconfig.ProxyServerConfig{
			URL:      "https://httpsconfig.com",
			Username: "user",
			Password: "pass",
		},
		NoProxy: []string{
			"http://noproxy.com",
		},
	}

	tt := map[string]*Config{
		"with jwt": {
			TenantID:     tenantID,
			ClientID:     clientID,
			URL:          url,
			CertPath:     "",
			CertPassword: "",
			Proxy:        proxy,
		},
		"with cert": {
			TenantID:     tenantID,
			ClientID:     clientID,
			URL:          url,
			CertPath:     certPath,
			CertPassword: "",
			Proxy:        proxy,
		},
		"without proxy": {
			TenantID:     tenantID,
			ClientID:     clientID,
			URL:          url,
			CertPath:     certPath,
			CertPassword: "",
		},
	}

	for name, cfg := range tt {
		t.Run(name, func(t *testing.T) {
			err := cfg.Validate()
			require.NoError(t, err)
		})
	}
}
