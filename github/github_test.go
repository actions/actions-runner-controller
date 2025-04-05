package github

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/github/fake"
	"github.com/google/go-github/v52/github"
)

var server *httptest.Server

func newTestClient() *Client {
	c := Config{
		Token: "token",
	}
	client, err := c.NewClient()
	if err != nil {
		panic(err)
	}

	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		panic(err)
	}
	client.BaseURL = baseURL

	return client
}

func TestMain(m *testing.M) {
	res := &fake.FixedResponses{
		ListRunners: fake.DefaultListRunnersHandler(),
	}
	server = fake.NewServer(fake.WithFixedResponses(res))
	defer server.Close()
	m.Run()
}

func TestGetRegistrationToken(t *testing.T) {
	tests := []struct {
		enterprise string
		org        string
		repo       string
		token      string
		err        bool
	}{
		{enterprise: "", org: "", repo: "test/valid", token: fake.RegistrationToken, err: false},
		{enterprise: "", org: "", repo: "test/invalid", token: "", err: true},
		{enterprise: "", org: "", repo: "test/error", token: "", err: true},
		{enterprise: "", org: "test", repo: "", token: fake.RegistrationToken, err: false},
		{enterprise: "", org: "invalid", repo: "", token: "", err: true},
		{enterprise: "", org: "error", repo: "", token: "", err: true},
		{enterprise: "test", org: "", repo: "", token: fake.RegistrationToken, err: false},
		{enterprise: "invalid", org: "", repo: "", token: "", err: true},
		{enterprise: "error", org: "", repo: "", token: "", err: true},
	}

	client := newTestClient()
	for i, tt := range tests {
		rt, err := client.GetRegistrationToken(context.Background(), tt.enterprise, tt.org, tt.repo, "test")
		if !tt.err && err != nil {
			t.Errorf("[%d] unexpected error: %v", i, err)
		}
		if tt.token != rt.GetToken() {
			t.Errorf("[%d] unexpected token: %v", i, rt.GetToken())
		}
	}
}

func TestListRunners(t *testing.T) {
	tests := []struct {
		enterprise string
		org        string
		repo       string
		length     int
		err        bool
	}{
		{enterprise: "", org: "", repo: "test/valid", length: 2, err: false},
		{enterprise: "", org: "", repo: "test/invalid", length: 0, err: true},
		{enterprise: "", org: "", repo: "test/error", length: 0, err: true},
		{enterprise: "", org: "test", repo: "", length: 2, err: false},
		{enterprise: "", org: "invalid", repo: "", length: 0, err: true},
		{enterprise: "", org: "error", repo: "", length: 0, err: true},
		{enterprise: "test", org: "", repo: "", length: 2, err: false},
		{enterprise: "invalid", org: "", repo: "", length: 0, err: true},
		{enterprise: "error", org: "", repo: "", length: 0, err: true},
	}

	client := newTestClient()
	for i, tt := range tests {
		runners, err := client.ListRunners(context.Background(), tt.enterprise, tt.org, tt.repo)
		if !tt.err && err != nil {
			t.Errorf("[%d] unexpected error: %v", i, err)
		}
		if tt.length != len(runners) {
			t.Errorf("[%d] unexpected runners list: %v", i, runners)
		}
	}
}

func TestRemoveRunner(t *testing.T) {
	tests := []struct {
		enterprise string
		org        string
		repo       string
		err        bool
	}{
		{enterprise: "", org: "", repo: "test/valid", err: false},
		{enterprise: "", org: "", repo: "test/invalid", err: true},
		{enterprise: "", org: "", repo: "test/error", err: true},
		{enterprise: "", org: "test", repo: "", err: false},
		{enterprise: "", org: "invalid", repo: "", err: true},
		{enterprise: "", org: "error", repo: "", err: true},
		{enterprise: "test", org: "", repo: "", err: false},
		{enterprise: "invalid", org: "", repo: "", err: true},
		{enterprise: "error", org: "", repo: "", err: true},
	}

	client := newTestClient()
	for i, tt := range tests {
		err := client.RemoveRunner(context.Background(), tt.enterprise, tt.org, tt.repo, int64(1))
		if !tt.err && err != nil {
			t.Errorf("[%d] unexpected error: %v", i, err)
		}
	}
}

func TestCleanup(t *testing.T) {
	token := "token"

	client := newTestClient()
	client.regTokens = map[string]*github.RegistrationToken{
		"active": &github.RegistrationToken{
			Token:     &token,
			ExpiresAt: &github.Timestamp{Time: time.Now().Add(time.Hour * 1)},
		},
		"expired": &github.RegistrationToken{
			Token:     &token,
			ExpiresAt: &github.Timestamp{Time: time.Now().Add(-time.Hour * 1)},
		},
	}

	client.cleanup()
	if _, ok := client.regTokens["active"]; !ok {
		t.Errorf("active token was accidentally removed")
	}
	if _, ok := client.regTokens["expired"]; ok {
		t.Errorf("expired token still exists")
	}
}

func TestUserAgent(t *testing.T) {
	client := newTestClient()
	if client.UserAgent != "actions-runner-controller/NA" {
		t.Errorf("UserAgent should be set to actions-runner-controller/NA")
	}
}
