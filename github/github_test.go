package github

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v33/github"
	"github.com/summerwind/actions-runner-controller/github/fake"
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
	client.Client.BaseURL = baseURL

	return client
}

func TestMain(m *testing.M) {
	server = fake.NewServer()
	defer server.Close()
	m.Run()
}

func TestGetRegistrationToken(t *testing.T) {
	tests := []struct {
		org   string
		repo  string
		token string
		err   bool
	}{
		{org: "", repo: "test/valid", token: fake.RegistrationToken, err: false},
		{org: "", repo: "test/invalid", token: "", err: true},
		{org: "", repo: "test/error", token: "", err: true},
		{org: "test", repo: "", token: fake.RegistrationToken, err: false},
		{org: "invalid", repo: "", token: "", err: true},
		{org: "error", repo: "", token: "", err: true},
	}

	client := newTestClient()
	for i, tt := range tests {
		rt, err := client.GetRegistrationToken(context.Background(), tt.org, tt.repo, "test")
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
		org    string
		repo   string
		length int
		err    bool
	}{
		{org: "", repo: "test/valid", length: 2, err: false},
		{org: "", repo: "test/invalid", length: 0, err: true},
		{org: "", repo: "test/error", length: 0, err: true},
		{org: "test", repo: "", length: 2, err: false},
		{org: "invalid", repo: "", length: 0, err: true},
		{org: "error", repo: "", length: 0, err: true},
	}

	client := newTestClient()
	for i, tt := range tests {
		runners, err := client.ListRunners(context.Background(), tt.org, tt.repo)
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
		org  string
		repo string
		err  bool
	}{
		{org: "", repo: "test/valid", err: false},
		{org: "", repo: "test/invalid", err: true},
		{org: "", repo: "test/error", err: true},
		{org: "test", repo: "", err: false},
		{org: "invalid", repo: "", err: true},
		{org: "error", repo: "", err: true},
	}

	client := newTestClient()
	for i, tt := range tests {
		err := client.RemoveRunner(context.Background(), tt.org, tt.repo, int64(1))
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
