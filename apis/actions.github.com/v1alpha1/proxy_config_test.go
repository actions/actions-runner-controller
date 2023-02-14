package v1alpha1_test

import (
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

			if test.out == "" {
				assert.Nil(t, u)
				return
			}

			assert.Equal(t, test.out, u.String())
		})
	}
}

func TestProxyConfig_EnvVars(t *testing.T) {
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

	vars, err := config.EnvVars(secretFetcher)
	require.NoError(t, err)
	require.Len(t, vars, 3)

	assert.Contains(t, vars, corev1.EnvVar{
		Name:  "http_proxy",
		Value: "http://username:password@proxy.example.com:8080",
	})

	assert.Contains(t, vars, corev1.EnvVar{
		Name:  "https_proxy",
		Value: "https://username:password@proxy.example.com:8080",
	})

	assert.Contains(t, vars, corev1.EnvVar{
		Name:  "no_proxy",
		Value: "noproxy.example.com,noproxy2.example.com",
	})
}
