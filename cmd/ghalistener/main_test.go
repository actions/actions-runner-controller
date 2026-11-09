package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCode(t *testing.T) {
	tests := map[string]struct {
		err  error
		want int
	}{
		"session creation failure other than not found": {
			err:  sessionClientError(fmt.Errorf("failed to create message session: %w", scaleset.UnauthorizedError)),
			want: 1,
		},
		"not found outside session creation": {
			err:  fmt.Errorf("handling initial message failed: %w: RunnerScaleSetNotFoundException", scaleset.NotFoundError),
			want: 1,
		},
		"generic error": {
			err:  errors.New("boom"),
			want: 1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, exitCode(tt.err))
		})
	}
}

// TestExitCodeFromSessionCreation drives the real scaleset client, so the
// classification is checked against the errors it actually produces rather
// than against hand-built ones.
func TestExitCodeFromSessionCreation(t *testing.T) {
	const runnerScaleSetID = 22

	tests := map[string]struct {
		registrationTokenStatus int
		want                    int
	}{
		// The Actions service no longer knows the scale set.
		"session endpoint reports the scale set is gone": {
			registrationTokenStatus: http.StatusCreated,
			want:                    config.ExitCodeRunnerScaleSetNotFound,
		},
		// The admin token is refreshed inside session creation. GitHub answers
		// 404 there when the credentials lost access to the repository or the
		// repository was renamed, which says nothing about the scale set.
		"registration token endpoint returns not found": {
			registrationTokenStatus: http.StatusNotFound,
			want:                    1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := newActionsServer(t, tt.registrationTokenStatus, runnerScaleSetID)

			client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL:     server.URL + "/owner/repo",
				PersonalAccessToken: "token",
				SystemInfo:          scaleset.SystemInfo{System: "test"},
			})
			require.NoError(t, err)

			_, err = client.MessageSessionClient(context.Background(), runnerScaleSetID, "owner")
			require.Error(t, err)
			require.ErrorIs(t, err, scaleset.NotFoundError, "both cases must reach the classification as a not-found")

			assert.Equal(t, tt.want, exitCode(sessionClientError(err)))
		})
	}
}

// newActionsServer serves the GitHub API calls behind the admin token refresh
// and a message session endpoint that does not know the scale set.
func newActionsServer(t *testing.T, registrationTokenStatus, runnerScaleSetID int) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("POST /api/v3/repos/owner/repo/actions/runners/registration-token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(registrationTokenStatus)
		if registrationTokenStatus != http.StatusCreated {
			_, _ = w.Write([]byte(`{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "registration-token",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	})

	mux.HandleFunc("POST /api/v3/actions/runner-registration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":   server.URL + "/tenant",
			"token": unsignedJWT(time.Now().Add(time.Hour)),
		})
	})

	mux.HandleFunc(fmt.Sprintf("POST /tenant/_apis/runtime/runnerscalesets/%d/sessions", runnerScaleSetID), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"typeName": "GitHub.Actions.Runtime.WebApi.RunnerScaleSetNotFoundException, GitHub.Actions.Runtime.WebApi",
			"message":  fmt.Sprintf("No runner scale set found with identifier %d.", runnerScaleSetID),
		})
	})

	return server
}

// unsignedJWT returns a token the client can read the expiry from. The client
// parses the admin token without verifying it.
func unsignedJWT(expiresAt time.Time) string {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := enc.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, expiresAt.Unix()))
	return header + "." + claims + "."
}
