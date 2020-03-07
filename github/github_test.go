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
	client.github.BaseURL = baseURL

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
		{repo: "test/ok", token: fake.RegistrationToken, err: false},
		{repo: "test/ok", token: fake.RegistrationToken, err: false},
		{repo: "test/invalid", token: "", err: true},
		{repo: "test/error", token: "", err: true},
	}

	for _, tt := range tests {
		token, err := client.GetRegistrationToken(context.Background(), tt.repo, "test")
		if !tt.err && err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if tt.token != token {
			t.Errorf("unexpected token: %v", token)
		}
	}
}

func TestListRunners(t *testing.T) {
	tests := []struct {
		repo   string
		length int
		err    bool
	}{
		{repo: "test/ok", length: 2, err: false},
		{repo: "test/invalid", length: 0, err: true},
		{repo: "test/error", length: 0, err: true},
	}

	for _, tt := range tests {
		runners, err := client.ListRunners(context.Background(), tt.repo)
		if !tt.err && err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if tt.length != len(runners) {
			t.Errorf("unexpected runners list: %v", runners)
		}
	}
}

func TestRemoveRunner(t *testing.T) {
	tests := []struct {
		repo string
		name string
		ok   bool
		err  bool
	}{
		{repo: "test/ok", name: "test1", ok: true, err: false},
		{repo: "test/ok", name: "test3", ok: false, err: false},
		{repo: "test/invalid", name: "test1", ok: false, err: true},
		{repo: "test/remove-invalid", name: "test1", ok: false, err: true},
		{repo: "test/remove-error", name: "test1", ok: false, err: true},
	}

	for _, tt := range tests {
		ok, err := client.RemoveRunner(context.Background(), tt.repo, tt.name)
		if !tt.err && err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !tt.ok && ok {
			t.Errorf("unexpected result: %v", ok)
		}
	}
}

func TestCleanup(t *testing.T) {
	client := newTestClient()
	client.tokens = map[string]*RegistrationToken{
		"active": &RegistrationToken{
			Token:     "active-token",
			ExpiresAt: github.Timestamp{time.Now().Add(time.Hour * 1)},
		},
		"expired": &RegistrationToken{
			Token:     "expired-token",
			ExpiresAt: github.Timestamp{time.Now().Add(-time.Hour * 1)},
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
