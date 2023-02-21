package actions_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Do(t *testing.T) {
	t.Run("trims byte order mark from response if present", func(t *testing.T) {
		t.Run("when there is no body", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			}))
			defer server.Close()

			client, err := actions.NewClient("https://localhost/org/repo", &actions.ActionsAuth{Token: "token"})
			require.NoError(t, err)

			req, err := http.NewRequest("GET", server.URL, nil)
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Empty(t, string(body))
		})

		responses := []string{
			"\xef\xbb\xbf{\"foo\":\"bar\"}",
			"{\"foo\":\"bar\"}",
		}

		for _, response := range responses {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(response))
			}))
			defer server.Close()

			client, err := actions.NewClient("https://localhost/org/repo", &actions.ActionsAuth{Token: "token"})
			require.NoError(t, err)

			req, err := http.NewRequest("GET", server.URL, nil)
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, "{\"foo\":\"bar\"}", string(body))
		}
	})
}
