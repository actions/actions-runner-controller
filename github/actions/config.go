package actions

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

var ErrInvalidGitHubConfigURL = fmt.Errorf("invalid config URL, should point to an enterprise, org, or repository")

type GitHubScope int

const (
	GitHubScopeUnknown GitHubScope = iota
	GitHubScopeEnterprise
	GitHubScopeOrganization
	GitHubScopeRepository
)

type GitHubConfig struct {
	ConfigURL *url.URL
	Scope     GitHubScope

	Enterprise   string
	Organization string
	Repository   string

	IsHosted bool
}

func ParseGitHubConfigFromURL(in string) (*GitHubConfig, error) {
	u, err := url.Parse(strings.Trim(in, "/"))
	if err != nil {
		return nil, err
	}

	isHosted := isHostedGitHubURL(u)

	configURL := &GitHubConfig{
		ConfigURL: u,
		IsHosted:  isHosted,
	}

	invalidURLError := fmt.Errorf("%q: %w", u.String(), ErrInvalidGitHubConfigURL)

	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")

	switch len(pathParts) {
	case 1: // Organization
		if pathParts[0] == "" {
			return nil, invalidURLError
		}

		configURL.Scope = GitHubScopeOrganization
		configURL.Organization = pathParts[0]

	case 2: // Repository or enterprise
		if strings.ToLower(pathParts[0]) == "enterprises" {
			configURL.Scope = GitHubScopeEnterprise
			configURL.Enterprise = pathParts[1]
			break
		}

		configURL.Scope = GitHubScopeRepository
		configURL.Organization = pathParts[0]
		configURL.Repository = pathParts[1]
	default:
		return nil, invalidURLError
	}

	return configURL, nil
}

func (c *GitHubConfig) GitHubAPIURL(path string) *url.URL {
	result := &url.URL{
		Scheme: c.ConfigURL.Scheme,
	}

	isHosted := isHostedGitHubURL(c.ConfigURL)

	if isHosted {
		// Hosted
		if strings.EqualFold("github.com", c.ConfigURL.Host) ||
			strings.EqualFold("github.localhost", c.ConfigURL.Host) ||
			strings.HasSuffix(c.ConfigURL.Host, ".ghe.com") {
			result.Host = fmt.Sprintf("api.%s", c.ConfigURL.Host)
		} else if strings.EqualFold("www.github.com", c.ConfigURL.Host) {
			// re-routing www.github.com to api.github.com
			result.Host = "api.github.com"
		}
	} else {
		// Enterprise
		result.Host = c.ConfigURL.Host
		result.Path = "/api/v3"
	}

	result.Path += path

	return result
}

func isHostedGitHubURL(u *url.URL) bool {
	forceGhes := os.Getenv("GITHUB_ACTIONS_FORCE_GHES")

	if len(forceGhes) > 0 {
		return false
	}

	return strings.EqualFold(u.Host, "github.com") ||
		strings.EqualFold(u.Host, "www.github.com") ||
		strings.EqualFold(u.Host, "github.localhost") ||
		strings.HasSuffix(u.Host, ".ghe.com")
}
