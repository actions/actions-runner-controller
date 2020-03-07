package github

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/go-github/v29/github"
	"golang.org/x/oauth2"
)

// Runner represents a runner in the response of GitHub API.
type Runner struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	OS     string `json:"os"`
	Status string `json:"status"`
}

// RegistrationToken represents a registration token in the response
// of GitHub API.
type RegistrationToken struct {
	Token     string           `json:"token"`
	ExpiresAt github.Timestamp `json:"expires_at"`
}

// Client is a client for GitHub Actions API.
type Client struct {
	github *github.Client
	tokens map[string]*RegistrationToken
	mu     sync.Mutex
}

// NewClient returns a client that uses the access token specified.
func NewClient(token string) *Client {
	tc := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	))
	client := github.NewClient(tc)

	return &Client{
		github: client,
		tokens: map[string]*RegistrationToken{},
		mu:     sync.Mutex{},
	}
}

// GetRegistrationToken returns a registration token tied with the name of repository and runner.
func (c *Client) GetRegistrationToken(ctx context.Context, repo, name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%s/%s", repo, name)
	rt, ok := c.tokens[key]
	if ok && rt.ExpiresAt.After(time.Now().Add(-10*time.Minute)) {
		return rt.Token, nil
	}

	req, err := c.github.NewRequest("POST", fmt.Sprintf("/repos/%s/actions/runners/registration-token", repo), nil)
	if err != nil {
		return "", err
	}

	var regToken *RegistrationToken
	res, err := c.github.Do(ctx, req, &regToken)
	if err != nil {
		return "", err
	}

	if res.StatusCode != 201 {
		return "", fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	c.tokens[key] = regToken

	go func() {
		c.cleanup()
	}()

	return regToken.Token, nil
}

// RemoveRunner removes a runner with specified name from repocitory.
func (c *Client) RemoveRunner(ctx context.Context, repo, name string) (bool, error) {
	runners, err := c.ListRunners(ctx, repo)
	if err != nil {
		return false, err
	}

	id := 0
	for _, runner := range runners {
		if runner.Name == name {
			id = runner.ID
			break
		}
	}

	if id == 0 {
		return false, nil
	}

	req, err := c.github.NewRequest("DELETE", fmt.Sprintf("/repos/%s/actions/runners/%d", repo, id), nil)
	if err != nil {
		return false, err
	}

	res, err := c.github.Do(ctx, req, nil)
	if err != nil {
		return false, err
	}

	if res.StatusCode != 204 {
		return false, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return true, nil
}

// ListRunners returns a list of runners of specified repository name.
func (c *Client) ListRunners(ctx context.Context, repo string) ([]Runner, error) {
	runners := []Runner{}

	req, err := c.github.NewRequest("GET", fmt.Sprintf("/repos/%s/actions/runners", repo), nil)
	if err != nil {
		return runners, err
	}

	res, err := c.github.Do(ctx, req, &runners)
	if err != nil {
		return runners, err
	}

	if res.StatusCode != 200 {
		return runners, fmt.Errorf("unexpected status: %d", res.StatusCode)
	}

	return runners, nil
}

// cleanup removes expired registration tokens.
func (c *Client) cleanup() {
	for key, rt := range c.tokens {
		if rt.ExpiresAt.Before(time.Now()) {
			delete(c.tokens, key)
		}
	}
}
