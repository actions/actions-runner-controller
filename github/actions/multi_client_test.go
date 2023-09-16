package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testUserAgent = UserAgentInfo{
	Version:    "test",
	CommitSHA:  "test",
	ScaleSetID: 1,
}

func TestMultiClientCaching(t *testing.T) {
	logger := logr.Discard()
	ctx := context.Background()
	multiClient := NewMultiClient(logger).(*multiClient)

	defaultNamespace := "default"
	defaultConfigURL := "https://github.com/org/repo"
	defaultCreds := &ActionsAuth{
		Token: "token",
	}
	client, err := NewClient(defaultConfigURL, defaultCreds)
	require.NoError(t, err)

	multiClient.clients[ActionsClientKey{client.Identifier(), defaultNamespace}] = client

	// Verify that the client is cached
	cachedClient, err := multiClient.GetClientFor(
		ctx,
		defaultConfigURL,
		*defaultCreds,
		defaultNamespace,
	)
	require.NoError(t, err)
	assert.Equal(t, client, cachedClient)
	assert.Len(t, multiClient.clients, 1)

	// Asking for a different client results in creating and caching a new client
	otherNamespace := "other"
	newClient, err := multiClient.GetClientFor(
		ctx,
		defaultConfigURL,
		*defaultCreds,
		otherNamespace,
	)
	require.NoError(t, err)
	assert.NotEqual(t, client, newClient)
	assert.Len(t, multiClient.clients, 2)
}

func TestMultiClientOptions(t *testing.T) {
	logger := logr.Discard()
	ctx := context.Background()

	defaultNamespace := "default"
	defaultConfigURL := "https://github.com/org/repo"

	t.Run("GetClientFor", func(t *testing.T) {
		defaultCreds := &ActionsAuth{
			Token: "token",
		}

		multiClient := NewMultiClient(logger)
		service, err := multiClient.GetClientFor(
			ctx,
			defaultConfigURL,
			*defaultCreds,
			defaultNamespace,
		)
		service.SetUserAgent(testUserAgent)

		require.NoError(t, err)

		client := service.(*Client)
		req, err := client.NewGitHubAPIRequest(ctx, "GET", "/test", nil)
		require.NoError(t, err)
		assert.Equal(t, testUserAgent.String(), req.Header.Get("User-Agent"))
	})

	t.Run("GetClientFromSecret", func(t *testing.T) {
		secret := map[string][]byte{
			"github_token": []byte("token"),
		}

		multiClient := NewMultiClient(logger)
		service, err := multiClient.GetClientFromSecret(
			ctx,
			defaultConfigURL,
			defaultNamespace,
			secret,
		)
		service.SetUserAgent(testUserAgent)
		require.NoError(t, err)

		client := service.(*Client)
		req, err := client.NewGitHubAPIRequest(ctx, "GET", "/test", nil)
		require.NoError(t, err)
		assert.Equal(t, testUserAgent.String(), req.Header.Get("User-Agent"))
	})
}

func TestCreateJWT(t *testing.T) {
	key := `-----BEGIN RSA PRIVATE KEY-----
MIICWgIBAAKBgHXfRT9cv9UY9fAAD4+1RshpfSSZe277urfEmPfX3/Og9zJYRk//
CZrJVD1CaBZDiIyQsNEzjta7r4UsqWdFOggiNN2E7ZTFQjMSaFkVgrzHqWuiaCBf
/BjbKPn4SMDmTzHvIe7Nel76hBdCaVgu6mYCW5jmuSH5qz/yR1U1J/WJAgMBAAEC
gYARWGWsSU3BYgbu5lNj5l0gKMXNmPhdAJYdbMTF0/KUu18k/XB7XSBgsre+vALt
I8r4RGKApoGif8P4aPYUyE8dqA1bh0X3Fj1TCz28qoUL5//dA+pigCRS20H7HM3C
ojoqF7+F+4F2sXmzFNd1NgY5RxFPYosTT7OnUiFuu2IisQJBALnMLe09LBnjuHXR
xxR65DDNxWPQLBjW3dL+ubLcwr7922l6ZIQsVjdeE0ItEUVRjjJ9/B/Jq9VJ/Lw4
g9LCkkMCQQCiaM2f7nYmGivPo9hlAbq5lcGJ5CCYFfeeYzTxMqum7Mbqe4kk5lgb
X6gWd0Izg2nGdAEe/97DClO6VpKcPbpDAkBTR/JOJN1fvXMxXJaf13XxakrQMr+R
Yr6LlSInykyAz8lJvlLP7A+5QbHgN9NF/wh+GXqpxPwA3ukqdSqhjhWBAkBn6mDv
HPgR5xrzL6XM8y9TgaOlJAdK6HtYp6d/UOmN0+Butf6JUq07TphRT5tXNJVgemch
O5x/9UKfbrc+KyzbAkAo97TfFC+mZhU1N5fFelaRu4ikPxlp642KRUSkOh8GEkNf
jQ97eJWiWtDcsMUhcZgoB5ydHcFlrBIn6oBcpge5
-----END RSA PRIVATE KEY-----`

	auth := &GitHubAppAuth{
		AppID:         123,
		AppPrivateKey: key,
	}
	jwt, err := createJWTForGitHubApp(auth)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(jwt)
}
