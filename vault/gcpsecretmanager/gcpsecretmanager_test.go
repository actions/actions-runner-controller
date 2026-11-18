package gcpsecretmanager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/googleapis/gax-go/v2/apierror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http/httpproxy"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestConfigValidate(t *testing.T) {
	tests := map[string]struct {
		config  Config
		wantErr string
	}{
		"valid":                {config: Config{ProjectID: "my-project"}},
		"valid with version":   {config: Config{ProjectID: "my-project", Version: "3"}},
		"valid with proxy":     {config: Config{ProjectID: "my-project", Proxy: &httpproxy.Config{HTTPSProxy: "http://proxy:3128"}}},
		"missing project id":   {config: Config{}, wantErr: "project_id is not set"},
		"empty proxy settings": {config: Config{ProjectID: "my-project", Proxy: &httpproxy.Config{}}, wantErr: "proxy configuration is empty"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestSecretVersionName(t *testing.T) {
	assert.Equal(t, "projects/p/secrets/s/versions/latest", (&Config{ProjectID: "p"}).secretVersionName("s"))
	assert.Equal(t, "projects/p/secrets/s/versions/4", (&Config{ProjectID: "p", Version: "4"}).secretVersionName("s"))
}

func TestNewFailsWithoutCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	_, err := New(t.Context(), Config{ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "application default credentials")
}

func TestNewValidatesConfig(t *testing.T) {
	_, err := New(t.Context(), Config{})
	assert.ErrorContains(t, err, "project_id is not set")
}

type fakeServer struct {
	gotPath string
	status  int
	body    any
}

func (f *fakeServer) start(t *testing.T) *GCPSecretManager {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		require.NoError(t, json.NewEncoder(w).Encode(f.body))
	}))
	t.Cleanup(srv.Close)

	sm, err := newWithClientOptions(
		t.Context(),
		Config{ProjectID: "my-project"},
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)
	return sm
}

func accessResponse(data string, checksum int64) map[string]any {
	return map[string]any{
		"name": "projects/my-project/secrets/gha-runners-app/versions/1",
		"payload": map[string]any{
			"data":       base64.StdEncoding.EncodeToString([]byte(data)),
			"dataCrc32c": strconv.FormatInt(checksum, 10),
		},
	}
}

func TestGetSecret(t *testing.T) {
	const payload = `{"github_token":"gh_token"}`
	checksum := int64(crc32.Checksum([]byte(payload), crc32.MakeTable(crc32.Castagnoli)))

	t.Run("returns payload", func(t *testing.T) {
		f := &fakeServer{status: http.StatusOK, body: accessResponse(payload, checksum)}
		sm := f.start(t)

		got, err := sm.GetSecret(t.Context(), "gha-runners-app")
		require.NoError(t, err)
		assert.Equal(t, payload, got)
		assert.Equal(t, "/v1/projects/my-project/secrets/gha-runners-app/versions/latest:access", f.gotPath)
	})

	t.Run("rejects checksum mismatch", func(t *testing.T) {
		f := &fakeServer{status: http.StatusOK, body: accessResponse(payload, checksum+1)}
		sm := f.start(t)

		_, err := sm.GetSecret(t.Context(), "gha-runners-app")
		assert.ErrorContains(t, err, "checksum mismatch")
	})

	t.Run("rejects empty name", func(t *testing.T) {
		f := &fakeServer{status: http.StatusOK, body: accessResponse(payload, checksum)}
		sm := f.start(t)

		_, err := sm.GetSecret(t.Context(), "")
		assert.ErrorContains(t, err, "secret name is empty")
	})

	errorBody := func(code int, status string) map[string]any {
		return map[string]any{"error": map[string]any{"code": code, "message": status, "status": status}}
	}

	t.Run("not found", func(t *testing.T) {
		f := &fakeServer{status: http.StatusNotFound, body: errorBody(http.StatusNotFound, "NOT_FOUND")}
		sm := f.start(t)

		_, err := sm.GetSecret(t.Context(), "gha-runners-app")
		require.Error(t, err)
		assert.True(t, IsNotFound(err), "expected not found: %v", err)
	})

	t.Run("permission denied is not reported as not found", func(t *testing.T) {
		f := &fakeServer{status: http.StatusForbidden, body: errorBody(http.StatusForbidden, "PERMISSION_DENIED")}
		sm := f.start(t)

		_, err := sm.GetSecret(t.Context(), "gha-runners-app")
		require.Error(t, err)
		assert.False(t, IsNotFound(err), "expected not to be not found: %v", err)
	})
}

func TestIsNotFound(t *testing.T) {
	notFound, ok := apierror.FromError(&googleapi.Error{Code: http.StatusNotFound})
	require.True(t, ok)
	forbidden, ok := apierror.FromError(&googleapi.Error{Code: http.StatusForbidden})
	require.True(t, ok)

	assert.True(t, IsNotFound(notFound))
	assert.True(t, IsNotFound(errors.Join(errors.New("wrapped"), notFound)))
	assert.False(t, IsNotFound(forbidden))
	assert.False(t, IsNotFound(errors.New("dial tcp: i/o timeout")))
	assert.False(t, IsNotFound(nil))
}
