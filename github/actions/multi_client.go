package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-logr/logr"
	jwt "github.com/golang-jwt/jwt/v4"
)

type MultiClient interface {
	GetClientFor(ctx context.Context, githubConfigURL string, creds ActionsAuth, namespace string, options ...ClientOption) (ActionsService, error)
	GetClientFromSecret(ctx context.Context, githubConfigURL, namespace string, secretData KubernetesSecretData, options ...ClientOption) (ActionsService, error)
}

type multiClient struct {
	// To lock adding and removing of individual clients.
	mu      sync.Mutex
	clients map[ActionsClientKey]*Client

	logger    logr.Logger
	userAgent string
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

type AppInstallationAccount struct {
	Login string `json:"login"`
}

type AppInstallation struct {
	ID      int64                  `json:"id"`
	Account AppInstallationAccount `json:"account"`
}

func NewMultiClient(userAgent string, logger logr.Logger) MultiClient {
	return &multiClient{
		mu:        sync.Mutex{},
		clients:   make(map[ActionsClientKey]*Client),
		logger:    logger,
		userAgent: userAgent,
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
			WithUserAgent(m.userAgent),
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
	hasGitHubAppAuth := len(appID) > 0 && len(appPrivateKey) > 0

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

	var parsedAppInstallationID int64
	if len(appInstallationID) <= 0 {
		config, err := ParseGitHubConfigFromURL(githubConfigURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse githubConfigURL: %w", err)
		}

		appToken, err := m.createAppJWT(appPrivateKey, parsedAppID)
		if err != nil {
			return nil, fmt.Errorf("failed to create app JWT: %w", err)
		}

		installations, err := m.listAppInstallations(config, appToken)
		if err != nil {
			return nil, fmt.Errorf("failed to list app installations: %w", err)
		}

		for _, installation := range installations {
			if installation.Account.Login == config.Organization {
				parsedAppInstallationID = installation.ID
				break
			}
		}

		if parsedAppInstallationID == 0 {
			return nil, fmt.Errorf("app installation id was not provided and can't find it automatically: %w", err)
		}
	} else {
		parsedAppInstallationID, err = strconv.ParseInt(appInstallationID, 10, 64)
		if err != nil {
			return nil, err
		}
	}

	auth.AppCreds = &GitHubAppAuth{AppID: parsedAppID, AppInstallationID: parsedAppInstallationID, AppPrivateKey: appPrivateKey}
	return m.GetClientFor(ctx, githubConfigURL, auth, namespace, options...)
}

func (m *multiClient) createAppJWT(privateKey string, appID int64) (string, error) {
	signKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKey))
	if err != nil {
		return "", err
	}

	t := jwt.New(jwt.GetSigningMethod("RS256"))
	t.Claims = &jwt.RegisteredClaims{
		IssuedAt:  &jwt.NumericDate{Time: time.Now().Add(-time.Second * 60)}, // Allow 1 minute drift
		ExpiresAt: &jwt.NumericDate{Time: time.Now().Add(time.Minute * 9)},   // Max is 10 mins, allow 1 minute drift
		Issuer:    strconv.FormatInt(appID, 10),
	}

	token, err := t.SignedString(signKey)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *multiClient) listAppInstallations(config *GitHubConfig, appToken string) ([]AppInstallation, error) {
	url := config.GitHubAPIURL("/app/installations")

	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header = http.Header{
		"Accept":        []string{"application/vnd.github.v3+json"},
		"Authorization": []string{"Bearer " + appToken},
	}

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	installations := []AppInstallation{}
	if err := json.Unmarshal(body, &installations); err != nil {
		return nil, err
	}

	return installations, nil
}
