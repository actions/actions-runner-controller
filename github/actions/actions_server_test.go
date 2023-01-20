package actions_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/require"
)

// newActionsServer returns a new httptest.Server that handles the
// authentication requests neeeded to create a new client. Any requests not
// made to the /actions/runners/registration-token or
// /actions/runner-registration endpoints will be handled by the provided
// handler. The returned server is started and will be automatically closed
// when the test ends.
func newActionsServer(t *testing.T, handler http.Handler) *actionsServer {
	var u string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// handle getRunnerRegistrationToken
		if strings.HasSuffix(r.URL.Path, "/runners/registration-token") {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"token":"token"}`))
			return
		}

		// handle getActionsServiceAdminConnection
		if strings.HasSuffix(r.URL.Path, "/actions/runner-registration") {
			claims := &jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Minute)),
				Issuer:    "123",
			}

			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(samplePrivateKey))
			require.NoError(t, err)
			tokenString, err := token.SignedString(privateKey)
			require.NoError(t, err)
			w.Write([]byte(`{"url":"` + u + `","token":"` + tokenString + `"}`))
			return
		}

		handler.ServeHTTP(w, r)
	}))

	u = server.URL

	t.Cleanup(func() {
		server.Close()
	})

	return &actionsServer{server}
}

type actionsServer struct {
	*httptest.Server
}

func (s *actionsServer) configURLForOrg(org string) string {
	return s.URL + "/" + org
}

const samplePrivateKey = `-----BEGIN RSA PRIVATE KEY-----
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
