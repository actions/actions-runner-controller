package github

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v29/github"
	"github.com/summerwind/actions-runner-controller/github/fake"
)

var (
	server *httptest.Server
	client *Client
)

func newTestClient() *Client {
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		panic(err)
	}

	client := NewClient("token")
	client.SetBaseURL(baseURL)

	return client
}

func TestMain(m *testing.M) {
	server = fake.NewServer()
	defer server.Close()

	client = newTestClient()

	m.Run()
}

func TestGetRegistrationToken(t *testing.T) {
	tests := []struct {
		repo  string
		token string
		err   bool
	}{
		{repo: "test/valid", token: fake.RegistrationToken, err: false},
		{repo: "test/invalid", token: "", err: true},
		{repo: "test/error", token: "", err: true},
	}

	for i, tt := range tests {
		token, err := client.GetRegistrationToken(context.Background(), tt.repo, "test")
		if !tt.err && err != nil {
			t.Errorf("[%d] unexpected error: %v", i, err)
		}
		if tt.token != token {
			t.Errorf("[%d] unexpected token: %v", i, token)
		}
	}
}

func TestListRunners(t *testing.T) {
	tests := []struct {
		repo   string
		length int
		err    bool
	}{
		{repo: "test/valid", length: 2, err: false},
		{repo: "test/invalid", length: 0, err: true},
		{repo: "test/error", length: 0, err: true},
	}

	for i, tt := range tests {
		runners, err := client.ListRunners(context.Background(), tt.repo)
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
		repo string
		err  bool
	}{
		{repo: "test/valid", err: false},
		{repo: "test/invalid", err: true},
		{repo: "test/error", err: true},
	}

	for i, tt := range tests {
		err := client.RemoveRunner(context.Background(), tt.repo, 1)
		if !tt.err && err != nil {
			t.Errorf("[%d] unexpected error: %v", i, err)
		}
	}
}

func TestCleanup(t *testing.T) {
	client := newTestClient()
	client.tokens = map[string]*RegistrationToken{
		"active": &RegistrationToken{
			Token:     "active-token",
			ExpiresAt: github.Timestamp{Time: time.Now().Add(time.Hour * 1)},
		},
		"expired": &RegistrationToken{
			Token:     "expired-token",
			ExpiresAt: github.Timestamp{Time: time.Now().Add(-time.Hour * 1)},
		},
	}

	client.cleanup()
	if _, ok := client.tokens["active"]; !ok {
		t.Errorf("active token was accidentally removed")
	}
	if _, ok := client.tokens["expired"]; ok {
		t.Errorf("expired token still exists")
	}
}
