package actionsgithubcom

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http/httpproxy"
)

func TestProxy(t *testing.T) {
	server := testserver.New(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%v, %q\n", r.Header, r.URL.String())

		// Expect(u.User.Username()).To(Equal("test"))
		// password, _ := u.User.Password()
		// Expect(password).To(Equal("password"))
		w.WriteHeader(http.StatusOK)
	}))

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	u.User = url.UserPassword("username", "password")

	fmt.Println(u.String())

	req, err := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	require.NoError(t, err)

	req.Header.Add("Authorization", "Basic AAAAA==")

	fmt.Printf("Request headers=%v\n", req.Header)

	c, err := actions.NewClient("http://github.com/org/repo", nil, actions.WithProxy(func(req *http.Request) (*url.URL, error) {
		conf := httpproxy.Config{
			HTTPProxy: u.String(),
		}
		return conf.ProxyFunc()(req.URL)
	}))
	require.NoError(t, err)
	c.Do(req)
}
