package actionsgithubcom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/vault"
	"golang.org/x/net/http/httpproxy"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ActionsClientPool struct {
	k8sClient      client.Client
	vaultResolvers map[string]resolver
	multiClient    actions.MultiClient
}

type ActionsClientPoolOption func(*ActionsClientPool)

func WithVault(ty string, vault vault.Vault) ActionsClientPoolOption {
	return func(pool *ActionsClientPool) {
		pool.vaultResolvers[ty] = &vaultResolver{vault}
	}
}

func NewActionsClientPool(k8sClient client.Client, multiClient actions.MultiClient, opts ...ActionsClientPoolOption) *ActionsClientPool {
	if k8sClient == nil {
		panic("k8sClient must not be nil")
	}

	pool := &ActionsClientPool{
		k8sClient:      k8sClient,
		multiClient:    multiClient,
		vaultResolvers: make(map[string]resolver),
	}

	for _, opt := range opts {
		opt(pool)
	}

	return pool
}

type ActionsGitHubObject interface {
	client.Object
	GitHubConfigUrl() string
	GitHubConfigSecret() string
	Proxy() *v1alpha1.ProxyConfig
	GitHubServerTLS() *v1alpha1.GitHubServerTLSConfig
}

func (p *ActionsClientPool) Get(ctx context.Context, obj ActionsGitHubObject) (actions.ActionsService, error) {
	resolver, err := p.resolverForObject(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get resolver for object: %v", err)
	}

	appConfig, err := resolver.appConfig(ctx, obj.GitHubConfigSecret())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve app config: %v", err)
	}

	var clientOptions []actions.ClientOption
	if proxy := obj.Proxy(); proxy != nil {
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

		proxyFunc := func(req *http.Request) (*url.URL, error) {
			return config.ProxyFunc()(req.URL)
		}

		clientOptions = append(clientOptions, actions.WithProxy(proxyFunc))
	}

	tlsConfig := obj.GitHubServerTLS()
	if tlsConfig != nil {
		pool, err := tlsConfig.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := p.k8sClient.Get(
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

		clientOptions = append(clientOptions, actions.WithRootCAs(pool))
	}

	return p.multiClient.GetClientFor(
		ctx,
		obj.GitHubConfigUrl(),
		appConfig,
		obj.GetNamespace(),
		clientOptions...,
	)
}

func (p *ActionsClientPool) resolverForObject(obj ActionsGitHubObject) (resolver, error) {
	ty, ok := obj.GetAnnotations()[AnnotationKeyGitHubVaultType]
	if !ok {
		return &k8sResolver{
			namespace: obj.GetNamespace(),
			client:    p.k8sClient,
		}, nil
	}

	vault, ok := p.vaultResolvers[ty]
	if !ok {
		return nil, fmt.Errorf("unknown vault resolver %q", ty)
	}

	return vault, nil
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

	return appconfig.FromString(val)
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
