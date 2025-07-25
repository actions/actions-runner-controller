package actions

import (
	"context"
	"fmt"
	"sync"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/go-logr/logr"
)

type MultiClient interface {
	GetClientFor(ctx context.Context, githubConfigURL string, appConfig *appconfig.AppConfig, namespace string, options ...ClientOption) (ActionsService, error)
}

type multiClient struct {
	// To lock adding and removing of individual clients.
	mu      sync.Mutex
	clients map[ActionsClientKey]*Client

	logger logr.Logger
}

type GitHubAppAuth struct {
	// AppID is the ID or the Client ID of the application
	AppID             string
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

func (m *multiClient) GetClientFor(ctx context.Context, githubConfigURL string, appConfig *appconfig.AppConfig, namespace string, options ...ClientOption) (ActionsService, error) {
	m.logger.Info("retrieve actions client", "githubConfigURL", githubConfigURL, "namespace", namespace)

	if err := appConfig.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate app config: %w", err)
	}

	var creds ActionsAuth
	if len(appConfig.Token) > 0 {
		creds.Token = appConfig.Token
	} else {
		creds.AppCreds = &GitHubAppAuth{
			AppID:             appConfig.AppID,
			AppInstallationID: appConfig.AppInstallationID,
			AppPrivateKey:     appConfig.AppPrivateKey,
		}
	}

	client, err := NewClient(
		githubConfigURL,
		&creds,
		append([]ClientOption{
			WithLogger(m.logger),
		}, options...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate new client: %w", err)
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
