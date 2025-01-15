package azurekeyvault

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/actions/actions-runner-controller/proxyconfig"
	"github.com/hashicorp/go-retryablehttp"
)

// AzureKeyVault is a struct that holds the Azure Key Vault client.
type Config struct {
	TenantID     string                   `json:"tenant_id"`
	ClientID     string                   `json:"client_id"`
	URL          string                   `json:"url"`
	CertPath     string                   `json:"cert_path"`
	CertPassword string                   `json:"cert_password"` // optional
	Proxy        *proxyconfig.ProxyConfig `json:"proxy,omitempty"`
}

func (c *Config) Validate() error {
	if c.TenantID == "" {
		return errors.New("tenant_id is not set")
	}
	if c.ClientID == "" {
		return errors.New("client_id is not set")
	}
	if _, err := url.Parse(c.URL); err != nil {
		return fmt.Errorf("failed to parse url: %v", err)
	}

	if c.CertPath != "" {
		return errors.New("cert path must be provided")
	}

	if err := c.Proxy.Validate(); err != nil {
		return fmt.Errorf("proxy validation failed: %v", err)
	}

	return nil
}

// Client creates a new Azure Key Vault client using the provided configuration.
func (c *Config) Client() (*azsecrets.Client, error) {
	return c.certClient()
}

func (c *Config) certClient() (*azsecrets.Client, error) {
	data, err := os.ReadFile(c.CertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert file from path %q: %v", c.CertPath, err)
	}

	certs, key, err := azidentity.ParseCertificates(data, []byte(c.CertPassword))
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificates: %w", err)
	}

	httpClient, err := c.httpClient()
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate http client: %v", err)
	}

	cred, err := azidentity.NewClientCertificateCredential(
		c.TenantID,
		c.ClientID,
		certs,
		key,
		&azidentity.ClientCertificateCredentialOptions{
			ClientOptions: policy.ClientOptions{
				Transport: httpClient,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate credential: %v", err)
	}

	client, err := azsecrets.NewClient(c.URL, cred, &azsecrets.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: httpClient,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate client for azsecrets: %v", err)
	}

	return client, nil
}

func (c *Config) httpClient() (*http.Client, error) {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 4
	retryClient.RetryWaitMax = 30 * time.Second
	retryClient.HTTPClient.Timeout = 5 * time.Minute

	transport, ok := retryClient.HTTPClient.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("failed to get http transport")
	}
	if c.Proxy != nil {
		pc, err := c.Proxy.ProxyConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy config: %v", err)
		}
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			return pc.ProxyFunc()(req.URL)
		}
	}

	return retryClient.StandardClient(), nil
}
