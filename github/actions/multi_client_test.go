package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/logging"
)

func TestAddClient(t *testing.T) {
	logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating logger: %v\n", err)
		os.Exit(1)
	}
	multiClient := NewMultiClient("test-user-agent", logger).(*multiClient)

	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "actions/runners/registration-token") {
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")

			token := "abc-123"
			rt := &registrationToken{Token: &token}

			if err := json.NewEncoder(w).Encode(rt); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "actions/runner-registration") {
			w.Header().Set("Content-Type", "application/json")

			url := "actions.github.com/abc"
			jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiaWF0IjoxNTE2MjM5MDIyLCJleHAiOjI1MTYyMzkwMjJ9.tlrHslTmDkoqnc4Kk9ISoKoUNDfHo-kjlH-ByISBqzE"
			adminConnInfo := &ActionsServiceAdminConnection{ActionsServiceUrl: &url, AdminToken: &jwt}

			if err := json.NewEncoder(w).Encode(adminConnInfo); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			w.Header().Set("Content-Type", "application/vnd.github+json")

			t, _ := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z07:00")
			accessToken := &accessToken{
				Token:     "abc-123",
				ExpiresAt: t,
			}

			if err := json.NewEncoder(w).Encode(accessToken); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}))
	defer srv.Close()

	want := 1
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/github", srv.URL), ActionsAuth{Token: "PAT"}, "namespace"); err != nil {
		t.Fatal(err)
	}

	want++ // New repo
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/actions", srv.URL), ActionsAuth{Token: "PAT"}, "namespace"); err != nil {
		t.Fatal(err)
	}

	// Repeat
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/github", srv.URL), ActionsAuth{Token: "PAT"}, "namespace"); err != nil {
		t.Fatal(err)
	}

	want++ // New namespace
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/github", srv.URL), ActionsAuth{Token: "PAT"}, "other"); err != nil {
		t.Fatal(err)
	}

	want++ // New pat
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/github", srv.URL), ActionsAuth{Token: "other"}, "other"); err != nil {
		t.Fatal(err)
	}

	want++ // New org
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github", srv.URL), ActionsAuth{Token: "PAT"}, "other"); err != nil {
		t.Fatal(err)
	}

	// No org, repo, enterprise
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v", srv.URL), ActionsAuth{Token: "PAT"}, "other"); err == nil {
		t.Fatal(err)
	}

	want++ // Test keying on GitHub App
	appAuth := &GitHubAppAuth{
		AppID: 1,
		AppPrivateKey: `-----BEGIN RSA PRIVATE KEY-----
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
-----END RSA PRIVATE KEY-----`,
	}
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/github", srv.URL), ActionsAuth{AppCreds: appAuth}, "other"); err != nil {
		t.Fatal(err)
	}

	// Repeat last to verify GitHub App keys are mapped together
	if _, err := multiClient.GetClientFor(ctx, fmt.Sprintf("%v/github/github", srv.URL), ActionsAuth{AppCreds: appAuth}, "other"); err != nil {
		t.Fatal(err)
	}

	if len(multiClient.clients) != want {
		t.Fatalf("GetClientFor: unexpected number of clients: got=%v want=%v", len(multiClient.clients), want)
	}
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
