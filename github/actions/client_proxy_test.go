package actions_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http/httpproxy"
)

func TestClientProxy(t *testing.T) {
	serverCalled := false

	proxy := testserver.New(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
	}))

	proxyConfig := &httpproxy.Config{
		HTTPProxy: proxy.URL,
	}
	proxyFunc := func(req *http.Request) (*url.URL, error) {
		return proxyConfig.ProxyFunc()(req.URL)
	}

	c, err := actions.NewClient("http://github.com/org/repo", nil, actions.WithProxy(proxyFunc))
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	_, err = c.Do(req)
	require.NoError(t, err)

	assert.True(t, serverCalled)
}
