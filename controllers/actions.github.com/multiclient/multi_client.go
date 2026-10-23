package multiclient

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
	"github.com/google/uuid"
)

type MultiClient interface {
	GetClientFor(ctx context.Context, opts *ClientForOptions) (Client, error)
}

type Scaleset struct {
	mu      sync.Mutex
	clients map[string]*multiClientEntry
}

type multiClientEntry struct {
	client  *scaleset.Client
	rootCAs *x509.CertPool
}

func NewScaleset() *Scaleset {
	return &Scaleset{
		clients: make(map[string]*multiClientEntry),
	}
}

type Client interface {
	SetSystemInfo(info scaleset.SystemInfo)
	SystemInfo() scaleset.SystemInfo

	MessageSessionClient(ctx context.Context, runnerScaleSetID int, owner string, options ...scaleset.HTTPOption) (*scaleset.MessageSessionClient, error)

	GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error)

	GetRunner(ctx context.Context, runnerID int) (*scaleset.RunnerReference, error)
	GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerID int64) error

	GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*scaleset.RunnerGroup, error)

	GetRunnerScaleSet(ctx context.Context, runnerGroupID int, runnerScaleSetName string) (*scaleset.RunnerScaleSet, error)
	GetRunnerScaleSetByID(ctx context.Context, runnerScaleSetID int) (*scaleset.RunnerScaleSet, error)

	CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error)
	UpdateRunnerScaleSet(ctx context.Context, runnerScaleSetID int, runnerScaleSet *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error)
	DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetID int) error
}

func (m *Scaleset) GetClientFor(ctx context.Context, opts *ClientForOptions) (Client, error) {
	identifier, err := opts.identifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate client identifier: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.clients[identifier]
	if ok && entry.rootCAs.Equal(opts.RootCAs) {
		return entry.client, nil
	}

	client, err := opts.newClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create new client: %w", err)
	}

	m.clients[identifier] = &multiClientEntry{
		client:  client,
		rootCAs: opts.RootCAs,
	}

	return client, nil
}

type ClientForOptions struct {
	GithubConfigURL string
	AppConfig       appconfig.AppConfig
	Namespace       string
	RootCAs         *x509.CertPool
	ProxyFunc       func(*http.Request) (*url.URL, error)
}

func (o *ClientForOptions) identifier() (string, error) {
	if err := o.AppConfig.Validate(); err != nil {
		return "", fmt.Errorf("failed to validate app config: %w", err)
	}
	if _, err := actions.ParseGitHubConfigFromURL(o.GithubConfigURL); err != nil {
		return "", fmt.Errorf("failed to parse GitHub config URL: %w", err)
	}
	if o.Namespace == "" {
		return "", fmt.Errorf("namespace is required to generate client identifier")
	}
	identifier := fmt.Sprintf("configURL:%q,namespace:%q,proxy:%t", o.GithubConfigURL, o.Namespace, o.ProxyFunc != nil)

	if o.AppConfig.Token != "" {
		identifier += fmt.Sprintf(",token:%q,", o.AppConfig.Token)
	} else {
		identifier += fmt.Sprintf(
			",appID:%q,installationID:%q,key:%q",
			o.AppConfig.AppID,
			strconv.FormatInt(o.AppConfig.AppInstallationID, 10),
			o.AppConfig.AppPrivateKey,
		)
	}

	if o.RootCAs != nil {
		// ignoring because this cert pool is intended not to come from SystemCertPool
		// nolint:staticcheck
		identifier += fmt.Sprintf(",rootCAs:%q", o.RootCAs.Subjects())
	}

	return uuid.NewHash(sha256.New(), uuid.NameSpaceOID, []byte(identifier), 6).String(), nil
}

func (o *ClientForOptions) newClient() (*scaleset.Client, error) {
	systemInfo := scaleset.SystemInfo{
		System:     "actions-runner-controller",
		Version:    build.Version,
		CommitSHA:  build.CommitSHA,
		ScaleSetID: 0, // by default, scale set is 0 (not created yet)
		Subsystem:  "gha-scale-set-controller",
	}

	options := []scaleset.HTTPOption{
		scaleset.WithTimeout(30 * time.Second),
		scaleset.WithRetryMax(2),
	}
	if o.RootCAs != nil {
		options = append(options, scaleset.WithRootCAs(o.RootCAs))
	}
	if o.ProxyFunc != nil {
		options = append(options, scaleset.WithProxy(o.ProxyFunc))
	}

	if o.AppConfig.Token != "" {
		c, err := scaleset.NewClientWithPersonalAccessToken(
			scaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL:     o.GithubConfigURL,
				PersonalAccessToken: o.AppConfig.Token,
				SystemInfo:          systemInfo,
			},
			options...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate client with personal access token auth: %w", err)
		}
		return c, nil
	}

	c, err := scaleset.NewClientWithGitHubApp(
		scaleset.ClientWithGitHubAppConfig{
			GitHubConfigURL: o.GithubConfigURL,
			GitHubAppAuth: scaleset.GitHubAppAuth{
				ClientID:       o.AppConfig.AppID,
				InstallationID: o.AppConfig.AppInstallationID,
				PrivateKey:     o.AppConfig.AppPrivateKey,
			},
			SystemInfo: systemInfo,
		},
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate client with GitHub App auth: %w", err)
	}
	return c, nil
}
