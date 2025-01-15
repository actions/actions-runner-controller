package proxyconfig

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type kv struct {
	key   string
	value string
}

func TestReadFromEnvNoPrefix(t *testing.T) {
	var (
		url      = "example.com"
		username = "user"
		password = "password"

		noProxy = "test.com,other.com"
	)
	tt := map[string]struct {
		envs []*kv
		want *ProxyConfig
	}{
		"no envs": {},
		"http only": {
			envs: []*kv{
				{"HTTP_URL", url},
				{"HTTP_USERNAME", username},
				{"HTTP_PASSWORD", password},
			},
			want: &ProxyConfig{
				HTTP: &ProxyServerConfig{
					URL:      url,
					Username: username,
					Password: password,
				},
			},
		},
		"https only": {
			envs: []*kv{
				{"HTTPS_URL", url},
				{"HTTPS_USERNAME", username},
				{"HTTPS_PASSWORD", password},
			},
			want: &ProxyConfig{
				HTTPS: &ProxyServerConfig{
					URL:      url,
					Username: username,
					Password: password,
				},
			},
		},
		"no proxy only": {
			envs: []*kv{
				{"NO_PROXY", noProxy},
			},
			want: &ProxyConfig{
				NoProxy: strings.Split(noProxy, ","),
			},
		},
		"all set": {
			envs: []*kv{
				{"HTTP_URL", url},
				{"HTTP_USERNAME", username},
				{"HTTP_PASSWORD", password},
				{"HTTPS_URL", url},
				{"HTTPS_USERNAME", username},
				{"HTTPS_PASSWORD", password},
				{"NO_PROXY", noProxy},
			},
			want: &ProxyConfig{
				HTTP: &ProxyServerConfig{
					URL:      url,
					Username: username,
					Password: password,
				},
				HTTPS: &ProxyServerConfig{
					URL:      url,
					Username: username,
					Password: password,
				},
				NoProxy: strings.Split(noProxy, ","),
			},
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			os.Clearenv()
			for _, kv := range tc.envs {
				os.Setenv(kv.key, kv.value)
			}

			got, err := ReadFromEnv("")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
