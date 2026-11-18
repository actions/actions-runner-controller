package gcpsecretmanager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"golang.org/x/net/http/httpproxy"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// DefaultVersion is the secret version accessed when Config.Version is empty.
const DefaultVersion = "latest"

// Config holds the configuration for the Google Cloud Secret Manager vault.
//
// Authentication uses Google Application Default Credentials (ADC). On GKE this
// is typically Workload Identity Federation for GKE: the Kubernetes service
// account running the controller or listener is granted
// roles/secretmanager.secretAccessor on the secret. Outside GKE, ADC can be
// provided with a mounted credentials file referenced by
// GOOGLE_APPLICATION_CREDENTIALS.
type Config struct {
	// ProjectID is the ID (or number) of the Google Cloud project that owns the secrets.
	ProjectID string `json:"project_id"`
	// Version is the secret version to access. Defaults to "latest".
	Version string            `json:"version,omitempty"`
	Proxy   *httpproxy.Config `json:"proxy,omitempty"`
}

func (c *Config) Validate() error {
	if c.ProjectID == "" {
		return errors.New("project_id is not set")
	}

	if c.Proxy != nil {
		if c.Proxy.HTTPProxy == "" && c.Proxy.HTTPSProxy == "" && c.Proxy.NoProxy == "" {
			return errors.New("proxy configuration is empty, at least one proxy must be set")
		}
	}

	return nil
}

func (c *Config) version() string {
	if c.Version == "" {
		return DefaultVersion
	}
	return c.Version
}

// secretVersionName returns the fully qualified secret version resource name for the secret ID.
func (c *Config) secretVersionName(secretID string) string {
	return fmt.Sprintf("projects/%s/secrets/%s/versions/%s", c.ProjectID, secretID, c.version())
}

// httpClient returns an HTTP client authenticated with Application Default
// Credentials that routes Secret Manager traffic through the configured proxy.
// Token acquisition (e.g. from the GKE metadata server) is not proxied.
func (c *Config) httpClient(ctx context.Context) (*http.Client, error) {
	creds, err := google.FindDefaultCredentials(ctx, secretmanager.DefaultAuthScopes()...)
	if err != nil {
		return nil, fmt.Errorf("failed to find Google application default credentials: %w", err)
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("failed to get http transport")
	}
	transport = transport.Clone()
	if c.Proxy != nil {
		proxyFunc := c.Proxy.ProxyFunc()
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			return proxyFunc(req.URL)
		}
	}

	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &oauth2.Transport{
			Source: creds.TokenSource,
			Base:   transport,
		},
	}, nil
}
