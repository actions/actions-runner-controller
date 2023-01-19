package actionsgithubcom

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func TestProxyEnvVars(t *testing.T) {
	t.Run("proxy http only without credentials", func(t *testing.T) {
		tt := map[string]struct {
			proxy  *v1alpha1.ProxyConfig
			expect []corev1.EnvVar
		}{
			"http": {
				proxy: &v1alpha1.ProxyConfig{
					HTTP: &v1alpha1.ProxyServerConfig{
						Url: "http://127.0.0.1:8080",
					},
				},
				expect: []corev1.EnvVar{
					{
						Name:  EnvVarHTTPProxy,
						Value: "http://127.0.0.1:8080",
					},
				},
			},
			"https": {
				proxy: &v1alpha1.ProxyConfig{
					HTTPS: &v1alpha1.ProxyServerConfig{
						Url: "https://127.0.0.1:8443",
					},
				},
				expect: []corev1.EnvVar{
					{
						Name:  EnvVarHTTPSProxy,
						Value: "https://127.0.0.1:8443",
					},
				},
			},
			"no_proxy": {
				proxy: &v1alpha1.ProxyConfig{
					NoProxy: []string{"http://127.0.0.1:8080", "https://127.0.0.1:8443"},
				},
				expect: []corev1.EnvVar{
					{
						Name:  EnvVarNoProxy,
						Value: "http://127.0.0.1:8080,https://127.0.0.1:8443",
					},
				},
			},
		}

		for name, tc := range tt {
			t.Run(name, func(t *testing.T) {
				envs, err := proxyEnvVars(tc.proxy, nil, nil)
				if err != nil {
					t.Fatalf("unexpected error after calling proxyEnvVars: %v", err)
				}

				if !reflect.DeepEqual(envs, tc.expect) {
					t.Fatalf("expected env value to be %q, got %q", tc.expect, envs)
				}
			})
		}
	})

	t.Run("proxy http only with credentials", func(t *testing.T) {
		httpUserInfoFunc := func() (*url.Userinfo, error) {
			return url.UserPassword("example", "httptest"), nil
		}
		httpsUserInfoFunc := func() (*url.Userinfo, error) {
			return url.UserPassword("example", "httpstest"), nil
		}
		tt := map[string]struct {
			proxy  *v1alpha1.ProxyConfig
			expect []corev1.EnvVar
		}{
			"http": {
				proxy: &v1alpha1.ProxyConfig{
					HTTP: &v1alpha1.ProxyServerConfig{
						Url:                 "http://127.0.0.1:8080",
						CredentialSecretRef: "test", // to enforce func call
					},
				},
				expect: []corev1.EnvVar{
					{
						Name:  EnvVarHTTPProxy,
						Value: "http://example:httptest@127.0.0.1:8080",
					},
				},
			},
			"https": {
				proxy: &v1alpha1.ProxyConfig{
					HTTPS: &v1alpha1.ProxyServerConfig{
						Url:                 "https://127.0.0.1:8443",
						CredentialSecretRef: "test", // to enforce func call
					},
				},
				expect: []corev1.EnvVar{
					{
						Name:  EnvVarHTTPSProxy,
						Value: "https://example:httpstest@127.0.0.1:8443",
					},
				},
			},
			"no_proxy": {
				proxy: &v1alpha1.ProxyConfig{
					NoProxy: []string{"http://127.0.0.1:8080", "https://127.0.0.1:8443"},
				},
				expect: []corev1.EnvVar{
					{
						Name:  EnvVarNoProxy,
						Value: "http://127.0.0.1:8080,https://127.0.0.1:8443",
					},
				},
			},
		}

		for name, tc := range tt {
			t.Run(name, func(t *testing.T) {
				envs, err := proxyEnvVars(tc.proxy, httpUserInfoFunc, httpsUserInfoFunc)
				if err != nil {
					t.Fatalf("unexpected error after calling proxyEnvVars: %v", err)
				}

				if !reflect.DeepEqual(envs, tc.expect) {
					t.Fatalf("expected env value to be %q, got %q", tc.expect, envs)
				}
			})
		}
	})

	t.Run("proxy without credentials", func(t *testing.T) {
		const (
			httpUrl  = "http://127.0.0.1:8080"
			httpsUrl = "https://127.0.0.1:8081"
		)
		noProxyUrls := []string{"http://127.0.01:8082"}

		proxyConfig := &v1alpha1.ProxyConfig{
			HTTP: &v1alpha1.ProxyServerConfig{
				Url: httpUrl,
			},
			HTTPS: &v1alpha1.ProxyServerConfig{
				Url: httpsUrl,
			},
			NoProxy: noProxyUrls,
		}

		envs, err := proxyEnvVars(proxyConfig, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error after calling proxyEnvVars: %v", err)
		}

		envFrequency := make(map[string]int)
		for _, env := range envs {
			envFrequency[env.Name]++
		}

		for _, env := range []string{EnvVarHTTPProxy, EnvVarHTTPSProxy, EnvVarNoProxy} {
			if envFrequency[env] != 1 {
				t.Errorf("expected env %s to be set once, got %d: %v", env, envFrequency[env], envs)
			}
			delete(envFrequency, env)
		}

		if len(envFrequency) != 0 {
			keys := make([]string, 0, len(envFrequency))
			for key := range envFrequency {
				keys = append(keys, key)
			}
			t.Errorf("unexpected environment variables set: %s", strings.Join(keys, ","))
		}

		for _, env := range envs {
			switch {
			case env.Name == EnvVarHTTPProxy:
				if env.Value != httpUrl {
					t.Errorf("expected %s value to be %q, got %q", EnvVarHTTPProxy, httpUrl, env.Value)
				}
			case env.Name == EnvVarHTTPSProxy:
				if env.Value != httpsUrl {
					t.Errorf("expected %s value to be %q, got %q", EnvVarHTTPSProxy, httpsUrl, env.Value)
				}
			case env.Name == EnvVarNoProxy:
				expect := strings.Join(noProxyUrls, ",")
				if env.Value != expect {
					t.Errorf("expected %s value to be %q, got %q", EnvVarNoProxy, expect, env.Value)
				}
			}
		}
	})
}
