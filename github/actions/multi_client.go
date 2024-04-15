package actions

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/go-logr/logr"
)

type MultiClient interface {
	GetClientFor(ctx context.Context, githubConfigURL string, creds ActionsAuth, namespace string, options ...ClientOption) (ActionsService, error)
	GetClientFromSecret(ctx context.Context, githubConfigURL, namespace string, secretData KubernetesSecretData, options ...ClientOption) (ActionsService, error)
}

type multiClient struct {
	// To lock adding and removing of individual clients.
	mu      sync.Mutex
	clients map[ActionsClientKey]*Client

	logger logr.Logger
}

type GitHubAppAuth struct {
	AppID             int64
	AppInstallationID int64
	AppPrivateKey     string
}

type ActionsAuth struct {
	// GitHub App
	AppCreds *GitHubAppAuth

	// GitHub PAT
	Token string
}

type ActionsClientKey struct {
	Identifier string
	Namespace  string
}

func NewMultiClient(logger logr.Logger) MultiClient {
	return &multiClient{
		mu:      sync.Mutex{},
		clients: make(map[ActionsClientKey]*Client),
		logger:  logger,
	}
}

func (m *multiClient) GetClientFor(ctx context.Context, githubConfigURL string, creds ActionsAuth, namespace string, options ...ClientOption) (ActionsService, error) {
	m.logger.Info("retrieve actions client", "githubConfigURL", githubConfigURL, "namespace", namespace)

	if creds.Token == "" && creds.AppCreds == nil {
		return nil, fmt.Errorf("no credentials provided. either a PAT or GitHub App credentials should be provided")
	}

	if creds.Token != "" && creds.AppCreds != nil {
		return nil, fmt.Errorf("both PAT and GitHub App credentials provided. should only provide one")
	}

	client, err := NewClient(
		githubConfigURL,
		&creds,
		append([]ClientOption{
			WithLogger(m.logger),
		}, options...)...,
	)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := ActionsClientKey{
		Identifier: client.Identifier(),
		Namespace:  namespace,
	}

	cachedClient, has := m.clients[key]
	if has && cachedClient.rootCAs.Equal(client.rootCAs) {
		m.logger.Info("using cache client", "githubConfigURL", githubConfigURL, "namespace", namespace)
		return cachedClient, nil
	}

	m.logger.Info("creating new client", "githubConfigURL", githubConfigURL, "namespace", namespace)

	m.clients[key] = client

	m.logger.Info("successfully created new client", "githubConfigURL", githubConfigURL, "namespace", namespace)

	return client, nil
}

type KubernetesSecretData map[string][]byte

func (m *multiClient) GetClientFromSecret(ctx context.Context, githubConfigURL, namespace string, secretData KubernetesSecretData, options ...ClientOption) (ActionsService, error) {
	if len(secretData) == 0 {
		return nil, fmt.Errorf("must provide secret data with either PAT or GitHub App Auth")
	}

	token := string(secretData["github_token"])
	hasToken := len(token) > 0

	appID := string(secretData["github_app_id"])
	appInstallationID := string(secretData["github_app_installation_id"])
	appPrivateKey := string(secretData["github_app_private_key"])
	hasGitHubAppAuth := len(appID) > 0 && len(appInstallationID) > 0 && len(appPrivateKey) > 0

	if hasToken && hasGitHubAppAuth {
		return nil, fmt.Errorf("must provide secret with only PAT or GitHub App Auth to avoid ambiguity in client behavior")
	}

	if !hasToken && !hasGitHubAppAuth {
		return nil, fmt.Errorf("neither PAT nor GitHub App Auth credentials provided in secret")
	}

	auth := ActionsAuth{}

	if hasToken {
		auth.Token = token
		return m.GetClientFor(ctx, githubConfigURL, auth, namespace, options...)
	}

	parsedAppID, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return nil, err
	}

	parsedAppInstallationID, err := strconv.ParseInt(appInstallationID, 10, 64)
	if err != nil {
		return nil, err
	}

	auth.AppCreds = &GitHubAppAuth{AppID: parsedAppID, AppInstallationID: parsedAppInstallationID, AppPrivateKey: appPrivateKey}
	return m.GetClientFor(ctx, githubConfigURL, auth, namespace, options...)
}
