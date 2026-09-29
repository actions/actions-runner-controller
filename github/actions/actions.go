package actions

import (
	"fmt"
	"net/url"
	"strings"
)

var ErrInvalidGitHubConfigURL = fmt.Errorf("invalid config URL, should point to an enterprise, org, or repository")

type GitHubConfig struct {
	ConfigURL *url.URL

	Enterprise   string
	Organization string
	Repository   string
}

func ParseGitHubConfigFromURL(in string) (*GitHubConfig, error) {
	u, err := url.Parse(strings.Trim(in, "/"))
	if err != nil {
		return nil, err
	}

	configURL := &GitHubConfig{
		ConfigURL: u,
	}

	invalidURLError := fmt.Errorf("%q: %w", u.String(), ErrInvalidGitHubConfigURL)

	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")

	switch len(pathParts) {
	case 1: // Organization
		if pathParts[0] == "" {
			return nil, invalidURLError
		}

		configURL.Organization = pathParts[0]

	case 2: // Repository or enterprise
		if strings.ToLower(pathParts[0]) == "enterprises" {
			configURL.Enterprise = pathParts[1]
			break
		}

		configURL.Organization = pathParts[0]
		configURL.Repository = pathParts[1]
	default:
		return nil, invalidURLError
	}

	return configURL, nil
}
