package secretresolver

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/object"
	"github.com/actions/actions-runner-controller/vault"
	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
	"golang.org/x/net/http/httpproxy"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SecretResolver struct {
	k8sClient   client.Client
	multiClient multiclient.MultiClient
	logger      *slog.Logger
}

type Option func(*SecretResolver)

func WithLogger(logger *slog.Logger) Option {
	return func(sr *SecretResolver) {
		sr.logger = logger
	}
}

func New(k8sClient client.Client, scalesetMultiClient multiclient.MultiClient, opts ...Option) *SecretResolver {
	if k8sClient == nil {
		panic("k8sClient must not be nil")
	}

	secretResolver := &SecretResolver{
		k8sClient:   k8sClient,
		multiClient: scalesetMultiClient,
		logger:      slog.New(slog.DiscardHandler),
	}

	for _, opt := range opts {
		opt(secretResolver)
	}

	return secretResolver
}

func (sr *SecretResolver) GetAppConfig(ctx context.Context, obj object.ActionsGitHubObject) (*appconfig.AppConfig, error) {
	resolver, err := sr.resolverForObject(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get resolver for object: %v", err)
	}

	appConfig, err := resolver.appConfig(ctx, obj.GitHubConfigSecret())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve app config: %v", err)
	}

	return appConfig, nil
}

func (sr *SecretResolver) GetActionsService(ctx context.Context, obj object.ActionsGitHubObject) (multiclient.Client, error) {
	resolver, err := sr.resolverForObject(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get resolver for object: %v", err)
	}

	appConfig, err := resolver.appConfig(ctx, obj.GitHubConfigSecret())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve app config: %v", err)
	}

	var proxyFunc func(req *http.Request) (*url.URL, error)
	if proxy := obj.GitHubProxy(); proxy != nil {
		config := &httpproxy.Config{
			NoProxy: strings.Join(proxy.NoProxy, ","),
		}

		if proxy.HTTP != nil {
			u, err := url.Parse(proxy.HTTP.Url)
			if err != nil {
				return nil, fmt.Errorf("failed to parse proxy http url %q: %w", proxy.HTTP.Url, err)
			}

			if ref := proxy.HTTP.CredentialSecretRef; ref != "" {
				u.User, err = resolver.proxyCredentials(ctx, ref)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve proxy credentials: %v", err)
				}
			}

			config.HTTPProxy = u.String()
		}

		if proxy.HTTPS != nil {
			u, err := url.Parse(proxy.HTTPS.Url)
			if err != nil {
				return nil, fmt.Errorf("failed to parse proxy https url %q: %w", proxy.HTTPS.Url, err)
			}

			if ref := proxy.HTTPS.CredentialSecretRef; ref != "" {
				u.User, err = resolver.proxyCredentials(ctx, ref)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve proxy credentials: %v", err)
				}
			}

			config.HTTPSProxy = u.String()
		}

		proxyFunc = func(req *http.Request) (*url.URL, error) {
			return config.ProxyFunc()(req.URL)
		}
	}

	var rootCAs *x509.CertPool
	if tc := obj.GitHubServerTLS(); tc != nil {
		pool, err := tc.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := sr.k8sClient.Get(
				ctx,
				types.NamespacedName{
					Namespace: obj.GetNamespace(),
					Name:      name,
				},
				&configmap,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get configmap %s: %w", name, err)
			}

			return []byte(configmap.Data[key]), nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tls config: %w", err)
		}

		rootCAs = pool
	}

	return sr.multiClient.GetClientFor(
		ctx,
		&multiclient.ClientForOptions{
			GithubConfigURL: obj.GitHubConfigUrl(),
			AppConfig:       appConfig,
			Namespace:       obj.GetNamespace(),
			RootCAs:         rootCAs,
			ProxyFunc:       proxyFunc,
		},
	)
}

func (sr *SecretResolver) resolverForObject(ctx context.Context, obj object.ActionsGitHubObject) (resolver, error) {
	vaultConfig := obj.VaultConfig()
	if vaultConfig == nil || vaultConfig.Type == "" {
		return &k8sResolver{
			namespace: obj.GetNamespace(),
			client:    sr.k8sClient,
		}, nil
	}

	var proxy *httpproxy.Config
	if vaultProxy := obj.VaultProxy(); vaultProxy != nil {
		p, err := vaultProxy.ToHTTPProxyConfig(func(s string) (*corev1.Secret, error) {
			var secret corev1.Secret
			err := sr.k8sClient.Get(ctx, types.NamespacedName{Name: s, Namespace: obj.GetNamespace()}, &secret)
			if err != nil {
				return nil, fmt.Errorf("failed to get secret %s: %w", s, err)
			}
			return &secret, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy config: %v", err)
		}
		proxy = p
	}

	switch vaultConfig.Type {
	case vault.VaultTypeAzureKeyVault:
		akv, err := azurekeyvault.New(azurekeyvault.Config{
			TenantID:        vaultConfig.AzureKeyVault.TenantID,
			ClientID:        vaultConfig.AzureKeyVault.ClientID,
			URL:             vaultConfig.AzureKeyVault.URL,
			CertificatePath: vaultConfig.AzureKeyVault.CertificatePath,
			Proxy:           proxy,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure Key Vault client: %v", err)
		}
		return &vaultResolver{
			vault: akv,
		}, nil

	default:
		return nil, fmt.Errorf("unknown vault type %q", vaultConfig.Type)
	}
}

type resolver interface {
	appConfig(ctx context.Context, key string) (*appconfig.AppConfig, error)
	proxyCredentials(ctx context.Context, key string) (*url.Userinfo, error)
}

type k8sResolver struct {
	namespace string
	client    client.Client
}

func (r *k8sResolver) appConfig(ctx context.Context, key string) (*appconfig.AppConfig, error) {
	nsName := types.NamespacedName{
		Namespace: r.namespace,
		Name:      key,
	}
	secret := new(corev1.Secret)
	if err := r.client.Get(
		ctx,
		nsName,
		secret,
	); err != nil {
		return nil, fmt.Errorf("failed to get kubernetes secret: %q", nsName.String())
	}

	return appconfig.FromSecret(secret)
}

func (r *k8sResolver) proxyCredentials(ctx context.Context, key string) (*url.Userinfo, error) {
	nsName := types.NamespacedName{Namespace: r.namespace, Name: key}
	secret := new(corev1.Secret)
	if err := r.client.Get(
		ctx,
		nsName,
		secret,
	); err != nil {
		return nil, fmt.Errorf("failed to get kubernetes secret: %q", nsName.String())
	}

	return url.UserPassword(
		string(secret.Data["username"]),
		string(secret.Data["password"]),
	), nil
}

type vaultResolver struct {
	vault vault.Vault
}

func (r *vaultResolver) appConfig(ctx context.Context, key string) (*appconfig.AppConfig, error) {
	val, err := r.vault.GetSecret(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve secret: %v", err)
	}

	return appconfig.FromJSONString(val)
}

func (r *vaultResolver) proxyCredentials(ctx context.Context, key string) (*url.Userinfo, error) {
	val, err := r.vault.GetSecret(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve secret: %v", err)
	}

	type info struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	var i info
	if err := json.Unmarshal([]byte(val), &i); err != nil {
		return nil, fmt.Errorf("failed to unmarshal info: %v", err)
	}

	return url.UserPassword(i.Username, i.Password), nil
}
