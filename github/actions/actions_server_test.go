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
func newActionsServer(t *testing.T, handler http.Handler, options ...actionsServerOption) *actionsServer {
	s := httptest.NewServer(nil)
	server := &actionsServer{
		Server: s,
	}
	t.Cleanup(func() {
		server.Close()
	})

	for _, option := range options {
		option(server)
	}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// handle getRunnerRegistrationToken
		if strings.HasSuffix(r.URL.Path, "/runners/registration-token") {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"token":"token"}`))
			return
		}

		// handle getActionsServiceAdminConnection
		if strings.HasSuffix(r.URL.Path, "/actions/runner-registration") {
			if server.token == "" {
				server.token = defaultActionsToken(t)
			}

			w.Write([]byte(`{"url":"` + s.URL + `/tenant/123/","token":"` + server.token + `"}`))
			return
		}

		handler.ServeHTTP(w, r)
	})

	server.Config.Handler = h

	return server
}

type actionsServerOption func(*actionsServer)

type actionsServer struct {
	*httptest.Server

	token string
}

func (s *actionsServer) configURLForOrg(org string) string {
	return s.URL + "/" + org
}

func defaultActionsToken(t *testing.T) string {
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-10 * time.Minute)),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		Issuer:    "123",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(samplePrivateKey))
	require.NoError(t, err)
	tokenString, err := token.SignedString(privateKey)
	require.NoError(t, err)
	return tokenString
}

const samplePrivateKey = `-----BEGIN PRIVATE KEY-----
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
