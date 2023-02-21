package v1alpha1_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyConfig_ToSecret(t *testing.T) {
	config := &v1alpha1.ProxyConfig{
		HTTP: &v1alpha1.ProxyServerConfig{
			Url:                 "http://proxy.example.com:8080",
			CredentialSecretRef: "my-secret",
		},
		HTTPS: &v1alpha1.ProxyServerConfig{
			Url:                 "https://proxy.example.com:8080",
			CredentialSecretRef: "my-secret",
		},
		NoProxy: []string{
			"noproxy.example.com",
			"noproxy2.example.com",
		},
	}

	secretFetcher := func(string) (*corev1.Secret, error) {
		return &corev1.Secret{
			Data: map[string][]byte{
				"username": []byte("username"),
				"password": []byte("password"),
			},
		}, nil
	}

	result, err := config.ToSecretData(secretFetcher)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "http://username:password@proxy.example.com:8080", string(result["http_proxy"]))
	assert.Equal(t, "https://username:password@proxy.example.com:8080", string(result["https_proxy"]))
	assert.Equal(t, "noproxy.example.com,noproxy2.example.com", string(result["no_proxy"]))
}

func TestProxyConfig_ProxyFunc(t *testing.T) {
	config := &v1alpha1.ProxyConfig{
		HTTP: &v1alpha1.ProxyServerConfig{
			Url:                 "http://proxy.example.com:8080",
			CredentialSecretRef: "my-secret",
		},
		HTTPS: &v1alpha1.ProxyServerConfig{
			Url:                 "https://proxy.example.com:8080",
			CredentialSecretRef: "my-secret",
		},
		NoProxy: []string{
			"noproxy.example.com",
			"noproxy2.example.com",
		},
	}

	secretFetcher := func(string) (*corev1.Secret, error) {
		return &corev1.Secret{
			Data: map[string][]byte{
				"username": []byte("username"),
				"password": []byte("password"),
			},
		}, nil
	}

	result, err := config.ProxyFunc(secretFetcher)
	require.NoError(t, err)

	tests := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "http target",
			in:   "http://target.com",
			out:  "http://username:password@proxy.example.com:8080",
		},
		{
			name: "https target",
			in:   "https://target.com",
			out:  "https://username:password@proxy.example.com:8080",
		},
		{
			name: "no proxy",
			in:   "https://noproxy.example.com",
			out:  "",
		},
		{
			name: "no proxy 2",
			in:   "https://noproxy2.example.com",
			out:  "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", test.in, nil)
			require.NoError(t, err)
			u, err := result(req)
			require.NoError(t, err)

			if test.out == "" {
				assert.Nil(t, u)
				return
			}

			assert.Equal(t, test.out, u.String())
		})
	}
}
