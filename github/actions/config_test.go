package actions_test

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubConfig(t *testing.T) {
	t.Run("when given a valid URL", func(t *testing.T) {
		tests := []struct {
			name      string
			configURL string
			expected  *actions.GitHubConfig
		}{
			{
				name:      "repository URL",
				configURL: "https://github.com/org/repo",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeRepository,
					Enterprise:   "",
					Organization: "org",
					Repository:   "repo",
					IsHosted:     true,
				},
			},
			{
				name:      "repository URL with trailing slash",
				configURL: "https://github.com/org/repo/",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeRepository,
					Enterprise:   "",
					Organization: "org",
					Repository:   "repo",
					IsHosted:     true,
				},
			},
			{
				name:      "organization URL",
				configURL: "https://github.com/org",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     true,
				},
			},
			{
				name:      "enterprise URL",
				configURL: "https://github.com/enterprises/my-enterprise",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeEnterprise,
					Enterprise:   "my-enterprise",
					Organization: "",
					Repository:   "",
					IsHosted:     true,
				},
			},
			{
				name:      "enterprise URL with trailing slash",
				configURL: "https://github.com/enterprises/my-enterprise/",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeEnterprise,
					Enterprise:   "my-enterprise",
					Organization: "",
					Repository:   "",
					IsHosted:     true,
				},
			},
			{
				name:      "organization URL with www",
				configURL: "https://www.github.com/org",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     true,
				},
			},
			{
				name:      "organization URL with www and trailing slash",
				configURL: "https://www.github.com/org/",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     true,
				},
			},
			{
				name:      "github local URL",
				configURL: "https://github.localhost/org",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     true,
				},
			},
			{
				name:      "github local org URL",
				configURL: "https://my-ghes.com/org",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     false,
				},
			},
			{
				name:      "github local URL with trailing slash",
				configURL: "https://my-ghes.com/org/",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     false,
				},
			},
			{
				name:      "github local URL with ghe.com",
				configURL: "https://my-ghes.ghe.com/org/",
				expected: &actions.GitHubConfig{
					Scope:        actions.GitHubScopeOrganization,
					Enterprise:   "",
					Organization: "org",
					Repository:   "",
					IsHosted:     true,
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				parsedURL, err := url.Parse(strings.Trim(test.configURL, "/"))
				require.NoError(t, err)
				test.expected.ConfigURL = parsedURL

				cfg, err := actions.ParseGitHubConfigFromURL(test.configURL)
				require.NoError(t, err)
				assert.Equal(t, test.expected, cfg)
			})
		}
	})

	t.Run("when given an invalid URL", func(t *testing.T) {
		invalidURLs := []string{
			"https://github.com/",
			"https://github.com",
			"https://github.com/some/random/path",
		}

		for _, u := range invalidURLs {
			_, err := actions.ParseGitHubConfigFromURL(u)
			require.Error(t, err)
			assert.True(t, errors.Is(err, actions.ErrInvalidGitHubConfigURL))
		}
	})
}

func TestGitHubConfig_GitHubAPIURL(t *testing.T) {
	t.Run("when hosted", func(t *testing.T) {
		config, err := actions.ParseGitHubConfigFromURL("https://github.com/org/repo")
		require.NoError(t, err)
		assert.True(t, config.IsHosted)

		result := config.GitHubAPIURL("/some/path")
		assert.Equal(t, "https://api.github.com/some/path", result.String())
	})
	t.Run("when hosted with ghe.com", func(t *testing.T) {
		config, err := actions.ParseGitHubConfigFromURL("https://github.ghe.com/org/repo")
		require.NoError(t, err)
		assert.True(t, config.IsHosted)

		result := config.GitHubAPIURL("/some/path")
		assert.Equal(t, "https://api.github.ghe.com/some/path", result.String())
	})
	t.Run("when not hosted", func(t *testing.T) {
		config, err := actions.ParseGitHubConfigFromURL("https://ghes.com/org/repo")
		require.NoError(t, err)
		assert.False(t, config.IsHosted)

		result := config.GitHubAPIURL("/some/path")
		assert.Equal(t, "https://ghes.com/api/v3/some/path", result.String())
	})
	t.Run("when not hosted with ghe.com", func(t *testing.T) {
		os.Setenv("GITHUB_ACTIONS_FORCE_GHES", "1")
		defer os.Unsetenv("GITHUB_ACTIONS_FORCE_GHES")
		config, err := actions.ParseGitHubConfigFromURL("https://test.ghe.com/org/repo")
		require.NoError(t, err)
		assert.False(t, config.IsHosted)

		result := config.GitHubAPIURL("/some/path")
		assert.Equal(t, "https://test.ghe.com/api/v3/some/path", result.String())
	})
}
