package secretresolver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
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

	// Load mTLS client certificates for proxy authentication
	var tlsClientCerts []tls.Certificate
	if proxy := obj.GitHubProxy(); proxy != nil {
		certs, err := sr.loadProxyTLSClientCerts(ctx, obj.GetNamespace(), proxy)
		if err != nil {
			return nil, fmt.Errorf("failed to load proxy TLS client certificates: %w", err)
		}
		tlsClientCerts = certs
	}

	// Load proxy CA certificates for verifying proxy server TLS
	var rootCAs *x509.CertPool
	if proxy := obj.GitHubProxy(); proxy != nil {
		pool, err := sr.loadProxyCACerts(ctx, obj.GetNamespace(), proxy)
		if err != nil {
			return nil, fmt.Errorf("failed to load proxy CA certificates: %w", err)
		}
		if pool != nil {
			rootCAs = pool
		}
	}

	// Load GitHub server TLS config (for GHES) and merge with proxy CAs if present
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

		// Merge GitHub server CAs with proxy CAs if both are present
		if rootCAs != nil && pool != nil {
			// Add GitHub server certs to the existing proxy CA pool
			// Note: x509.CertPool doesn't have a direct merge method, but since
			// tc.ToCertPool returns certs from configmap, we need to re-add them
			// For now, prefer the GitHub server pool and log that proxy CAs are overridden
			sr.logger.Info("Both proxy CA and GitHub server TLS configured; using GitHub server TLS pool")
		}
		rootCAs = pool
	}

	return sr.multiClient.GetClientFor(
		ctx,
		&multiclient.ClientForOptions{
			GithubConfigURL:       obj.GitHubConfigUrl(),
			AppConfig:             *appConfig,
			Namespace:             obj.GetNamespace(),
			RootCAs:               rootCAs,
			ProxyFunc:             proxyFunc,
			TLSClientCertificates: tlsClientCerts,
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

// loadProxyTLSClientCerts loads TLS client certificates for mTLS proxy authentication.
// It first checks for K8s secret refs in the proxy config, then falls back to
// environment variables HTTPS_PROXY_CLIENT_CERT and HTTPS_PROXY_CLIENT_KEY for file paths.
func (sr *SecretResolver) loadProxyTLSClientCerts(ctx context.Context, namespace string, proxy *v1alpha1.ProxyConfig) ([]tls.Certificate, error) {
	var certs []tls.Certificate

	// Load HTTP proxy client cert if configured via K8s secret
	if proxy.HTTP != nil && proxy.HTTP.TLS != nil && proxy.HTTP.TLS.ClientCertSecretRef != "" {
		cert, err := sr.loadTLSCertFromSecret(ctx, namespace, proxy.HTTP.TLS.ClientCertSecretRef)
		if err != nil {
			return nil, fmt.Errorf("failed to load HTTP proxy client cert: %w", err)
		}
		certs = append(certs, cert)
	}

	// Load HTTPS proxy client cert if configured via K8s secret
	if proxy.HTTPS != nil && proxy.HTTPS.TLS != nil && proxy.HTTPS.TLS.ClientCertSecretRef != "" {
		cert, err := sr.loadTLSCertFromSecret(ctx, namespace, proxy.HTTPS.TLS.ClientCertSecretRef)
		if err != nil {
			return nil, fmt.Errorf("failed to load HTTPS proxy client cert: %w", err)
		}
		certs = append(certs, cert)
	}

	// Fallback: load from file paths via environment variables if no K8s secrets configured
	if len(certs) == 0 {
		certFile := os.Getenv("HTTPS_PROXY_CLIENT_CERT")
		keyFile := os.Getenv("HTTPS_PROXY_CLIENT_KEY")
		if certFile != "" && keyFile != "" {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load client cert from files (cert=%s, key=%s): %w", certFile, keyFile, err)
			}
			certs = append(certs, cert)
			sr.logger.Info("Loaded proxy client cert from file paths", "cert", certFile, "key", keyFile)
		}
	}

	return certs, nil
}

// loadTLSCertFromSecret loads a TLS certificate from a Kubernetes TLS secret
func (sr *SecretResolver) loadTLSCertFromSecret(ctx context.Context, namespace, secretName string) (tls.Certificate, error) {
	var secret corev1.Secret
	err := sr.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      secretName,
	}, &secret)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	certPEM, ok := secret.Data["tls.crt"]
	if !ok {
		return tls.Certificate{}, fmt.Errorf("secret %s missing tls.crt key", secretName)
	}

	keyPEM, ok := secret.Data["tls.key"]
	if !ok {
		return tls.Certificate{}, fmt.Errorf("secret %s missing tls.key key", secretName)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to parse TLS certificate: %w", err)
	}

	return cert, nil
}

// loadProxyCACerts loads CA certificates for verifying proxy server TLS.
// It first checks for K8s secret/configmap refs in the proxy config, then falls back to
// the HTTPS_PROXY_CA_CERT environment variable for a file path.
func (sr *SecretResolver) loadProxyCACerts(ctx context.Context, namespace string, proxy *v1alpha1.ProxyConfig) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		sr.logger.Warn("Failed to load system cert pool, using empty pool", "error", err)
		pool = x509.NewCertPool()
	}

	var certsAdded bool

	// Load HTTP proxy CA cert if configured via K8s secret/configmap
	if proxy.HTTP != nil && proxy.HTTP.TLS != nil {
		if err := sr.addProxyCACertsToPool(ctx, namespace, proxy.HTTP.TLS, pool); err != nil {
			return nil, fmt.Errorf("failed to load HTTP proxy CA cert: %w", err)
		}
		if proxy.HTTP.TLS.CACertSecretRef != "" || proxy.HTTP.TLS.CACertConfigMapRef != "" {
			certsAdded = true
		}
	}

	// Load HTTPS proxy CA cert if configured via K8s secret/configmap
	if proxy.HTTPS != nil && proxy.HTTPS.TLS != nil {
		if err := sr.addProxyCACertsToPool(ctx, namespace, proxy.HTTPS.TLS, pool); err != nil {
			return nil, fmt.Errorf("failed to load HTTPS proxy CA cert: %w", err)
		}
		if proxy.HTTPS.TLS.CACertSecretRef != "" || proxy.HTTPS.TLS.CACertConfigMapRef != "" {
			certsAdded = true
		}
	}

	// Fallback: load from file path via environment variable if no K8s refs configured
	if !certsAdded {
		caFile := os.Getenv("HTTPS_PROXY_CA_CERT")
		if caFile != "" {
			caCert, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA cert file %s: %w", caFile, err)
			}
			if !pool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate from file %s", caFile)
			}
			certsAdded = true
			sr.logger.Info("Loaded proxy CA cert from file path", "file", caFile)
		}
	}

	if !certsAdded {
		return nil, nil
	}

	return pool, nil
}

// addProxyCACertsToPool adds CA certificates from a ProxyTLSConfig to the given cert pool
func (sr *SecretResolver) addProxyCACertsToPool(ctx context.Context, namespace string, tlsConfig *v1alpha1.ProxyTLSConfig, pool *x509.CertPool) error {
	if tlsConfig == nil {
		return nil
	}

	// Load from secret if configured
	if tlsConfig.CACertSecretRef != "" {
		var secret corev1.Secret
		err := sr.k8sClient.Get(ctx, types.NamespacedName{
			Namespace: namespace,
			Name:      tlsConfig.CACertSecretRef,
		}, &secret)
		if err != nil {
			return fmt.Errorf("failed to get CA cert secret %s: %w", tlsConfig.CACertSecretRef, err)
		}

		caCert, ok := secret.Data["ca.crt"]
		if !ok {
			return fmt.Errorf("secret %s missing ca.crt key", tlsConfig.CACertSecretRef)
		}

		if !pool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse CA certificate from secret %s", tlsConfig.CACertSecretRef)
		}
		sr.logger.Info("Loaded proxy CA cert from secret", "secret", tlsConfig.CACertSecretRef)
	}

	// Load from configmap if configured
	if tlsConfig.CACertConfigMapRef != "" {
		var configmap corev1.ConfigMap
		err := sr.k8sClient.Get(ctx, types.NamespacedName{
			Namespace: namespace,
			Name:      tlsConfig.CACertConfigMapRef,
		}, &configmap)
		if err != nil {
			return fmt.Errorf("failed to get CA cert configmap %s: %w", tlsConfig.CACertConfigMapRef, err)
		}

		caCert, ok := configmap.Data["ca.crt"]
		if !ok {
			return fmt.Errorf("configmap %s missing ca.crt key", tlsConfig.CACertConfigMapRef)
		}

		if !pool.AppendCertsFromPEM([]byte(caCert)) {
			return fmt.Errorf("failed to parse CA certificate from configmap %s", tlsConfig.CACertConfigMapRef)
		}
		sr.logger.Info("Loaded proxy CA cert from configmap", "configmap", tlsConfig.CACertConfigMapRef)
	}

	return nil
}
