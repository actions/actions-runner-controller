package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGithubAPIURL(t *testing.T) {
	tests := []struct {
		configURL string
		path      string
		expected  string
	}{
		{
			configURL: "https://github.com/org/repo",
			path:      "/app/installations/123/access_tokens",
			expected:  "https://api.github.com/app/installations/123/access_tokens",
		},
		{
			configURL: "https://www.github.com/org/repo",
			path:      "/app/installations/123/access_tokens",
			expected:  "https://api.github.com/app/installations/123/access_tokens",
		},
		{
			configURL: "http://github.localhost/org/repo",
			path:      "/app/installations/123/access_tokens",
			expected:  "http://api.github.localhost/app/installations/123/access_tokens",
		},
		{
			configURL: "https://my-instance.com/org/repo",
			path:      "/app/installations/123/access_tokens",
			expected:  "https://my-instance.com/api/v3/app/installations/123/access_tokens",
		},
		{
			configURL: "http://localhost/org/repo",
			path:      "/app/installations/123/access_tokens",
			expected:  "http://localhost/api/v3/app/installations/123/access_tokens",
		},
	}

	for _, test := range tests {
		actual, err := githubAPIURL(test.configURL, test.path)
		require.NoError(t, err)
		assert.Equal(t, test.expected, actual)
	}
}
