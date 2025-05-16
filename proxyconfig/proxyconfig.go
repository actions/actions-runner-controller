package proxyconfig

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

type ProxyConfig struct {
	HTTP    *ProxyServerConfig `json:"http,omitempty"`
	HTTPS   *ProxyServerConfig `json:"https,omitempty"`
	NoProxy []string           `json:"no_proxy,omitempty"`
}

func (pc *ProxyConfig) Validate() error {
	if pc == nil {
		return nil
	}

	if pc.HTTP != nil {
		_, err := url.ParseRequestURI(pc.HTTP.URL)
		if err != nil {
			return fmt.Errorf("proxy http set with invalid url: %v", err)
		}
	}
	if pc.HTTPS != nil {
		_, err := url.ParseRequestURI(pc.HTTPS.URL)
		if err != nil {
			return fmt.Errorf("proxy https set with invalid url: %v", err)
		}
	}

	for _, u := range pc.NoProxy {
		if _, err := url.ParseRequestURI(u); err != nil {
			return fmt.Errorf("proxy no_proxy set with invalid url: %v", err)
		}
	}
	return nil
}

func (c *ProxyConfig) ProxyConfig() (*httpproxy.Config, error) {
	if c == nil {
		return nil, nil
	}
	config := &httpproxy.Config{
		NoProxy: strings.Join(c.NoProxy, ","),
	}
	if c.HTTP != nil {
		u, err := c.HTTP.proxyURL()
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy http url: %w", err)
		}
		config.HTTPProxy = u.String()
	}

	if c.HTTPS != nil {
		u, err := c.HTTPS.proxyURL()
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy https url: %w", err)
		}
		config.HTTPSProxy = u.String()
	}

	return config, nil
}

type ProxyServerConfig struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *ProxyServerConfig) proxyURL() (*url.URL, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy url %q: %w", c.URL, err)
	}

	u.User = url.UserPassword(
		c.Username,
		c.Password,
	)

	return u, nil
}

func ReadFromEnv(prefix string) (*ProxyConfig, error) {
	url := os.Getenv(prefix + "HTTP_URL")
	username := os.Getenv(prefix + "HTTP_USERNAME")
	password := os.Getenv(prefix + "HTTP_PASSWORD")

	var http *ProxyServerConfig
	if url != "" || username != "" || password != "" {
		http = &ProxyServerConfig{
			URL:      url,
			Username: username,
			Password: password,
		}
	}

	url = os.Getenv(prefix + "HTTPS_URL")
	username = os.Getenv(prefix + "HTTPS_USERNAME")
	password = os.Getenv(prefix + "HTTPS_PASSWORD")

	var https *ProxyServerConfig
	if url != "" || username != "" || password != "" {
		https = &ProxyServerConfig{
			URL:      url,
			Username: username,
			Password: password,
		}
	}

	noProxyRaw := os.Getenv(prefix + "NO_PROXY")

	if http == nil && https == nil && noProxyRaw == "" {
		return nil, nil
	}

	var noProxy []string
	if noProxyRaw != "" {
		noProxy = strings.Split(noProxyRaw, ",")
	}

	return &ProxyConfig{
		HTTP:    http,
		HTTPS:   https,
		NoProxy: noProxy,
	}, nil
}
