package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
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
	defaultCreds := &appconfig.AppConfig{
		Token: "token",
	}
	defaultAuth := ActionsAuth{
		Token: defaultCreds.Token,
	}
	client, err := NewClient(defaultConfigURL, &defaultAuth)
	require.NoError(t, err)

	multiClient.clients[ActionsClientKey{client.Identifier(), defaultNamespace}] = client

	// Verify that the client is cached
	cachedClient, err := multiClient.GetClientFor(
		ctx,
		defaultConfigURL,
		defaultCreds,
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
		defaultCreds,
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
		defaultCreds := &appconfig.AppConfig{
			Token: "token",
		}

		multiClient := NewMultiClient(logger)
		service, err := multiClient.GetClientFor(
			ctx,
			defaultConfigURL,
			defaultCreds,
			defaultNamespace,
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
	key := `-----BEGIN PRIVATE KEY-----
MIIEugIBADANBgkqhkiG9w0BAQEFAASCBKQwggSgAgEAAoIBAQC7tgquvNIp+Ik3
rRVZ9r0zJLsSzTHqr2dA6EUUmpRiQ25MzjMqKqu0OBwvh/pZyfjSIkKrhIridNK4
DWnPfPWHE2K3Muh0X2sClxtqiiFmXsvbiTzhUm5a+zCcv0pJCWYnKi0HmyXpAXjJ
iN8mWliZN896verVYXWrod7EaAnuST4TiJeqZYW4bBBG81fPNc/UP4j6CKAW8nx9
HtcX6ApvlHeCLZUTW/qhGLO0nLKoEOr3tXCPW5VjKzlm134Dl+8PN6f1wv6wMAoA
lo7Ha5+c74jhPL6gHXg7cRaHQmuJCJrtl8qbLkFAulfkBixBw/6i11xoM/MOC64l
TWmXqrxTAgMBAAECgf9zYlxfL+rdHRXCoOm7pUeSPL0dWaPFP12d/Z9LSlDAt/h6
Pd+eqYEwhf795SAbJuzNp51Ls6LUGnzmLOdojKwfqJ51ahT1qbcBcMZNOcvtGqZ9
xwLG993oyR49C361Lf2r8mKrdrR5/fW0B1+1s6A+eRFivqFOtsOc4V4iMeHYsCVJ
hM7yMu0UfpolDJA/CzopsoGq3UuQlibUEUxKULza06aDjg/gBH3PnP+fQ1m0ovDY
h0pX6SCq5fXVJFS+Pbpu7j2ePNm3mr0qQhrUONZq0qhGN/piCbBZe1CqWApyO7nA
B95VChhL1eYs1BKvQePh12ap83woIUcW2mJF2F0CgYEA+aERTuKWEm+zVNKS9t3V
qNhecCOpayKM9OlALIK/9W6KBS+pDsjQQteQAUAItjvLiDjd5KsrtSgjbSgr66IP
b615Pakywe5sdnVGzSv+07KMzuFob9Hj6Xv9als9Y2geVhUZB2Frqve/UCjmC56i
zuQTSele5QKCSSTFBV3423cCgYEAwIBv9ChsI+mse6vPaqSPpZ2n237anThMcP33
aS0luYXqMWXZ0TQ/uSmCElY4G3xqNo8szzfy6u0HpldeUsEUsIcBNUV5kIIb8wKu
Zmgcc8gBIjJkyUJI4wuz9G/fegEUj3u6Cttmmj4iWLzCRscRJdfGpqwRIhOGyXb9
2Rur5QUCgYAGWIPaH4R1H4XNiDTYNbdyvV1ZOG7cHFq89xj8iK5cjNzRWO7RQ2WX
7WbpwTj3ePmpktiBMaDA0C5mXfkP2mTOD/jfCmgR6f+z2zNbj9zAgO93at9+yDUl
AFPm2j7rQgBTa+HhACb+h6HDZebDMNsuqzmaTWZuJ+wr89VWV5c17QKBgH3jwNNQ
mCAIUidynaulQNfTOZIe7IMC7WK7g9CBmPkx7Y0uiXr6C25hCdJKFllLTP6vNWOy
uCcQqf8LhgDiilBDifO3op9xpyuOJlWMYocJVkxx3l2L/rSU07PYcbKNAFAxXuJ4
xym51qZnkznMN5ei/CPFxVKeqHgaXDpekVStAoGAV3pSWAKDXY/42XEHixrCTqLW
kBxfaf3g7iFnl3u8+7Z/7Cb4ZqFcw0bRJseKuR9mFvBhcZxSErbMDEYrevefU9aM
APeCxEyw6hJXgbWKoG7Fw2g2HP3ytCJ4YzH0zNitHjk/1h4BG7z8cEQILCSv5mN2
etFcaQuTHEZyRhhJ4BU=
-----END PRIVATE KEY-----`

	auth := &GitHubAppAuth{
		AppID:         "123",
		AppPrivateKey: key,
	}
	jwt, err := createJWTForGitHubApp(auth)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(jwt)
}
