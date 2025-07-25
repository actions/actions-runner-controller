package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/build"
	"github.com/go-logr/logr"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
)

const (
	runnerEndpoint       = "_apis/distributedtask/pools/0/agents"
	scaleSetEndpoint     = "_apis/runtime/runnerscalesets"
	apiVersionQueryParam = "api-version=6.0-preview"
)

// Header used to propagate capacity information to the back-end
const HeaderScaleSetMaxCapacity = "X-ScaleSetMaxCapacity"

//go:generate mockery --inpackage --name=ActionsService
type ActionsService interface {
	GetRunnerScaleSet(ctx context.Context, runnerGroupId int, runnerScaleSetName string) (*RunnerScaleSet, error)
	GetRunnerScaleSetById(ctx context.Context, runnerScaleSetId int) (*RunnerScaleSet, error)
	GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*RunnerGroup, error)
	CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error)
	UpdateRunnerScaleSet(ctx context.Context, runnerScaleSetId int, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error)
	DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetId int) error

	CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*RunnerScaleSetSession, error)
	DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error
	RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*RunnerScaleSetSession, error)

	AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error)
	GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*AcquirableJobList, error)

	GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64, maxCapacity int) (*RunnerScaleSetMessage, error)
	DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error

	GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *RunnerScaleSetJitRunnerSetting, scaleSetId int) (*RunnerScaleSetJitRunnerConfig, error)

	GetRunner(ctx context.Context, runnerId int64) (*RunnerReference, error)
	GetRunnerByName(ctx context.Context, runnerName string) (*RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerId int64) error

	SetUserAgent(info UserAgentInfo)
}

type clientLogger struct {
	logr.Logger
}

func (l *clientLogger) Info(msg string, keysAndValues ...interface{}) {
	l.Logger.Info(msg, keysAndValues...)
}

func (l *clientLogger) Debug(msg string, keysAndValues ...interface{}) {
	// discard debug log
}

func (l *clientLogger) Error(msg string, keysAndValues ...interface{}) {
	l.Logger.Error(errors.New(msg), "Retryable client error", keysAndValues...)
}

func (l *clientLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.Logger.Info(msg, keysAndValues...)
}

var _ retryablehttp.LeveledLogger = &clientLogger{}

type Client struct {
	*http.Client

	// lock for refreshing the ActionsServiceAdminToken and ActionsServiceAdminTokenExpiresAt
	mu sync.Mutex

	// TODO: Convert to unexported fields once refactor of Listener is complete
	ActionsServiceAdminToken          string
	ActionsServiceAdminTokenExpiresAt time.Time
	ActionsServiceURL                 string

	retryMax     int
	retryWaitMax time.Duration

	creds     *ActionsAuth
	config    *GitHubConfig
	logger    logr.Logger
	userAgent UserAgentInfo

	rootCAs               *x509.CertPool
	tlsInsecureSkipVerify bool

	proxyFunc ProxyFunc
}

var _ ActionsService = &Client{}

type ProxyFunc func(req *http.Request) (*url.URL, error)

type ClientOption func(*Client)

type UserAgentInfo struct {
	// Version is the version of the controller
	Version string
	// CommitSHA is the git commit SHA of the controller
	CommitSHA string
	// ScaleSetID is the ID of the scale set
	ScaleSetID int
	// HasProxy is true if the controller is running behind a proxy
	HasProxy bool
	// Subsystem is the subsystem such as listener, controller, etc.
	// Each system may pick its own subsystem name.
	Subsystem string
}

func (u UserAgentInfo) String() string {
	scaleSetID := "NA"
	if u.ScaleSetID > 0 {
		scaleSetID = strconv.Itoa(u.ScaleSetID)
	}

	proxy := "Proxy/disabled"
	if u.HasProxy {
		proxy = "Proxy/enabled"
	}

	return fmt.Sprintf("actions-runner-controller/%s (%s; %s) ScaleSetID/%s (%s)", u.Version, u.CommitSHA, u.Subsystem, scaleSetID, proxy)
}

func WithLogger(logger logr.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

func WithRetryMax(retryMax int) ClientOption {
	return func(c *Client) {
		c.retryMax = retryMax
	}
}

func WithRetryWaitMax(retryWaitMax time.Duration) ClientOption {
	return func(c *Client) {
		c.retryWaitMax = retryWaitMax
	}
}

func WithRootCAs(rootCAs *x509.CertPool) ClientOption {
	return func(c *Client) {
		c.rootCAs = rootCAs
	}
}

func WithoutTLSVerify() ClientOption {
	return func(c *Client) {
		c.tlsInsecureSkipVerify = true
	}
}

func WithProxy(proxyFunc ProxyFunc) ClientOption {
	return func(c *Client) {
		c.proxyFunc = proxyFunc
	}
}

func NewClient(githubConfigURL string, creds *ActionsAuth, options ...ClientOption) (*Client, error) {
	config, err := ParseGitHubConfigFromURL(githubConfigURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse githubConfigURL: %w", err)
	}

	ac := &Client{
		creds:  creds,
		config: config,
		logger: logr.Discard(),

		// retryablehttp defaults
		retryMax:     4,
		retryWaitMax: 30 * time.Second,
		userAgent: UserAgentInfo{
			Version:    build.Version,
			CommitSHA:  build.CommitSHA,
			ScaleSetID: 0,
		},
	}

	for _, option := range options {
		option(ac)
	}

	retryClient := retryablehttp.NewClient()
	retryClient.Logger = &clientLogger{Logger: ac.logger}

	retryClient.RetryMax = ac.retryMax
	retryClient.RetryWaitMax = ac.retryWaitMax

	retryClient.HTTPClient.Timeout = 5 * time.Minute // timeout must be > 1m to accomodate long polling

	transport, ok := retryClient.HTTPClient.Transport.(*http.Transport)
	if !ok {
		// this should always be true, because retryablehttp.NewClient() uses
		// cleanhttp.DefaultPooledTransport()
		return nil, fmt.Errorf("failed to get http transport from retryablehttp client")
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}

	if ac.rootCAs != nil {
		transport.TLSClientConfig.RootCAs = ac.rootCAs
	}

	if ac.tlsInsecureSkipVerify {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	transport.Proxy = ac.proxyFunc

	retryClient.HTTPClient.Transport = transport
	ac.Client = retryClient.StandardClient()

	return ac, nil
}

func (c *Client) SetUserAgent(info UserAgentInfo) {
	c.userAgent = info
}

// Identifier returns a string to help identify a client uniquely.
// This is used for caching client instances and understanding when a config
// change warrants creating a new client. Any changes to Client that would
// require a new client should be reflected here.
func (c *Client) Identifier() string {
	identifier := fmt.Sprintf("configURL:%q,", c.config.ConfigURL.String())

	if c.creds.Token != "" {
		identifier += fmt.Sprintf("token:%q,", c.creds.Token)
	}

	if c.creds.AppCreds != nil {
		identifier += fmt.Sprintf(
			"appID:%q,installationID:%q,key:%q",
			c.creds.AppCreds.AppID,
			c.creds.AppCreds.AppInstallationID,
			c.creds.AppCreds.AppPrivateKey,
		)
	}

	if c.rootCAs != nil {
		// ignoring because this cert pool is intended not to come from SystemCertPool
		// nolint:staticcheck
		identifier += fmt.Sprintf("rootCAs:%q", c.rootCAs.Subjects())
	}

	return uuid.NewHash(sha256.New(), uuid.NameSpaceOID, []byte(identifier), 6).String()
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client request failed: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read the response body: %w", err)
	}
	err = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close the response body: %w", err)
	}

	body = trimByteOrderMark(body)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (c *Client) NewGitHubAPIRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := c.config.GitHubAPIURL(path)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create new GitHub API request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent.String())

	return req, nil
}

func (c *Client) NewActionsServiceRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	err := c.updateTokenIfNeeded(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to issue update token if needed: %w", err)
	}

	parsedPath, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse path %q: %w", path, err)
	}

	urlString, err := url.JoinPath(c.ActionsServiceURL, parsedPath.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to join path (actions_service_url=%q, parsedPath=%q): %w", c.ActionsServiceURL, parsedPath.Path, err)
	}

	u, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse url string %q: %w", urlString, err)
	}

	q := u.Query()
	maps.Copy(q, parsedPath.Query())

	if q.Get("api-version") == "" {
		q.Set("api-version", "6.0-preview")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create new request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.ActionsServiceAdminToken))
	req.Header.Set("User-Agent", c.userAgent.String())

	return req, nil
}

func (c *Client) GetRunnerScaleSet(ctx context.Context, runnerGroupId int, runnerScaleSetName string) (*RunnerScaleSet, error) {
	path := fmt.Sprintf("/%s?runnerGroupId=%d&name=%s", scaleSetEndpoint, runnerGroupId, runnerScaleSetName)
	req, err := c.NewActionsServiceRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerScaleSetList *runnerScaleSetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&runnerScaleSetList); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}
	if runnerScaleSetList.Count == 0 {
		return nil, nil
	}
	if runnerScaleSetList.Count > 1 {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        fmt.Errorf("multiple runner scale sets found with name %q", runnerScaleSetName),
		}
	}

	return &runnerScaleSetList.RunnerScaleSets[0], nil
}

func (c *Client) GetRunnerScaleSetById(ctx context.Context, runnerScaleSetId int) (*RunnerScaleSet, error) {
	path := fmt.Sprintf("/%s/%d", scaleSetEndpoint, runnerScaleSetId)
	req, err := c.NewActionsServiceRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerScaleSet *RunnerScaleSet
	if err := json.NewDecoder(resp.Body).Decode(&runnerScaleSet); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}
	return runnerScaleSet, nil
}

func (c *Client) GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*RunnerGroup, error) {
	path := fmt.Sprintf("/_apis/runtime/runnergroups/?groupName=%s", runnerGroup)
	req, err := c.NewActionsServiceRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, &ActionsError{
				StatusCode: resp.StatusCode,
				ActivityID: resp.Header.Get(HeaderActionsActivityID),
				Err:        err,
			}
		}
		return nil, fmt.Errorf("unexpected status code: %w", &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        errors.New(string(body)),
		})
	}

	var runnerGroupList *RunnerGroupList
	err = json.NewDecoder(resp.Body).Decode(&runnerGroupList)
	if err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}

	if runnerGroupList.Count == 0 {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        fmt.Errorf("no runner group found with name %q", runnerGroup),
		}
	}

	if runnerGroupList.Count > 1 {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        fmt.Errorf("multiple runner group found with name %q", runnerGroup),
		}
	}

	return &runnerGroupList.RunnerGroups[0], nil
}

func (c *Client) CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error) {
	body, err := json.Marshal(runnerScaleSet)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal runner scale set: %w", err)
	}

	req, err := c.NewActionsServiceRequest(ctx, http.MethodPost, scaleSetEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}
	var createdRunnerScaleSet *RunnerScaleSet
	if err := json.NewDecoder(resp.Body).Decode(&createdRunnerScaleSet); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}
	return createdRunnerScaleSet, nil
}

func (c *Client) UpdateRunnerScaleSet(ctx context.Context, runnerScaleSetId int, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error) {
	path := fmt.Sprintf("%s/%d", scaleSetEndpoint, runnerScaleSetId)

	body, err := json.Marshal(runnerScaleSet)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal runner scale set: %w", err)
	}

	req, err := c.NewActionsServiceRequest(ctx, http.MethodPatch, path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var updatedRunnerScaleSet *RunnerScaleSet
	if err := json.NewDecoder(resp.Body).Decode(&updatedRunnerScaleSet); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}
	return updatedRunnerScaleSet, nil
}

func (c *Client) DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetId int) error {
	path := fmt.Sprintf("/%s/%d", scaleSetEndpoint, runnerScaleSetId)
	req, err := c.NewActionsServiceRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return ParseActionsErrorFromResponse(resp)
	}

	defer resp.Body.Close()
	return nil
}

func (c *Client) GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64, maxCapacity int) (*RunnerScaleSetMessage, error) {
	u, err := url.Parse(messageQueueUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse message queue url: %w", err)
	}

	if lastMessageId > 0 {
		q := u.Query()
		q.Set("lastMessageId", strconv.FormatInt(lastMessageId, 10))
		u.RawQuery = q.Encode()
	}

	if maxCapacity < 0 {
		return nil, fmt.Errorf("maxCapacity must be greater than or equal to 0")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new request with context: %w", err)
	}

	req.Header.Set("Accept", "application/json; api-version=6.0-preview")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	req.Header.Set("User-Agent", c.userAgent.String())
	req.Header.Set(HeaderScaleSetMaxCapacity, strconv.Itoa(maxCapacity))

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode == http.StatusAccepted {
		defer resp.Body.Close()
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode != http.StatusUnauthorized {
			return nil, ParseActionsErrorFromResponse(resp)
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		body = trimByteOrderMark(body)
		if err != nil {
			return nil, &ActionsError{
				ActivityID: resp.Header.Get(HeaderActionsActivityID),
				StatusCode: resp.StatusCode,
				Err:        err,
			}
		}
		return nil, &MessageQueueTokenExpiredError{
			activityID: resp.Header.Get(HeaderActionsActivityID),
			statusCode: resp.StatusCode,
			msg:        string(body),
		}
	}

	var message *RunnerScaleSetMessage
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}
	return message, nil
}

func (c *Client) DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error {
	u, err := url.Parse(messageQueueUrl)
	if err != nil {
		return fmt.Errorf("failed to parse message queue url: %w", err)
	}

	u.Path = fmt.Sprintf("%s/%d", u.Path, messageId)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create new request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	req.Header.Set("User-Agent", c.userAgent.String())

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		if resp.StatusCode != http.StatusUnauthorized {
			return ParseActionsErrorFromResponse(resp)
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		body = trimByteOrderMark(body)
		if err != nil {
			return &ActionsError{
				ActivityID: resp.Header.Get(HeaderActionsActivityID),
				StatusCode: resp.StatusCode,
				Err:        err,
			}
		}
		return &MessageQueueTokenExpiredError{
			activityID: resp.Header.Get(HeaderActionsActivityID),
			statusCode: resp.StatusCode,
			msg:        string(body),
		}
	}
	return nil
}

func (c *Client) CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*RunnerScaleSetSession, error) {
	path := fmt.Sprintf("/%s/%d/sessions", scaleSetEndpoint, runnerScaleSetId)

	newSession := &RunnerScaleSetSession{
		OwnerName: owner,
	}

	requestData, err := json.Marshal(newSession)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal new session: %w", err)
	}

	createdSession := &RunnerScaleSetSession{}

	if err = c.doSessionRequest(ctx, http.MethodPost, path, bytes.NewBuffer(requestData), http.StatusOK, createdSession); err != nil {
		return nil, fmt.Errorf("failed to do the session request: %w", err)
	}

	return createdSession, nil
}

func (c *Client) DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error {
	path := fmt.Sprintf("/%s/%d/sessions/%s", scaleSetEndpoint, runnerScaleSetId, sessionId.String())
	return c.doSessionRequest(ctx, http.MethodDelete, path, nil, http.StatusNoContent, nil)
}

func (c *Client) RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*RunnerScaleSetSession, error) {
	path := fmt.Sprintf("/%s/%d/sessions/%s", scaleSetEndpoint, runnerScaleSetId, sessionId.String())
	refreshedSession := &RunnerScaleSetSession{}
	if err := c.doSessionRequest(ctx, http.MethodPatch, path, nil, http.StatusOK, refreshedSession); err != nil {
		return nil, fmt.Errorf("failed to do the session request: %w", err)
	}
	return refreshedSession, nil
}

func (c *Client) doSessionRequest(ctx context.Context, method, path string, requestData io.Reader, expectedResponseStatusCode int, responseUnmarshalTarget any) error {
	req, err := c.NewActionsServiceRequest(ctx, method, path, requestData)
	if err != nil {
		return fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode == expectedResponseStatusCode {
		if responseUnmarshalTarget == nil {
			return nil
		}

		if err := json.NewDecoder(resp.Body).Decode(responseUnmarshalTarget); err != nil {
			return &ActionsError{
				StatusCode: resp.StatusCode,
				ActivityID: resp.Header.Get(HeaderActionsActivityID),
				Err:        err,
			}
		}

		return nil
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return ParseActionsErrorFromResponse(resp)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	body = trimByteOrderMark(body)
	if err != nil {
		return &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}

	return fmt.Errorf("unexpected status code: %w", &ActionsError{
		StatusCode: resp.StatusCode,
		ActivityID: resp.Header.Get(HeaderActionsActivityID),
		Err:        errors.New(string(body)),
	})
}

func (c *Client) AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error) {
	u := fmt.Sprintf("%s/%s/%d/acquirejobs?api-version=6.0-preview", c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId)

	body, err := json.Marshal(requestIds)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request ids: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create new request with context: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	req.Header.Set("User-Agent", c.userAgent.String())

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode != http.StatusUnauthorized {
			return nil, ParseActionsErrorFromResponse(resp)
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		body = trimByteOrderMark(body)
		if err != nil {
			return nil, &ActionsError{
				ActivityID: resp.Header.Get(HeaderActionsActivityID),
				StatusCode: resp.StatusCode,
				Err:        err,
			}
		}

		return nil, &MessageQueueTokenExpiredError{
			activityID: resp.Header.Get(HeaderActionsActivityID),
			statusCode: resp.StatusCode,
			msg:        string(body),
		}
	}

	var acquiredJobs *Int64List
	err = json.NewDecoder(resp.Body).Decode(&acquiredJobs)
	if err != nil {
		return nil, &ActionsError{
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			StatusCode: resp.StatusCode,
			Err:        err,
		}
	}

	return acquiredJobs.Value, nil
}

func (c *Client) GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*AcquirableJobList, error) {
	path := fmt.Sprintf("/%s/%d/acquirablejobs", scaleSetEndpoint, runnerScaleSetId)

	req, err := c.NewActionsServiceRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode == http.StatusNoContent {
		defer resp.Body.Close()
		return &AcquirableJobList{Count: 0, Jobs: []AcquirableJob{}}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var acquirableJobList *AcquirableJobList
	err = json.NewDecoder(resp.Body).Decode(&acquirableJobList)
	if err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}

	return acquirableJobList, nil
}

func (c *Client) GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *RunnerScaleSetJitRunnerSetting, scaleSetId int) (*RunnerScaleSetJitRunnerConfig, error) {
	path := fmt.Sprintf("/%s/%d/generatejitconfig", scaleSetEndpoint, scaleSetId)

	body, err := json.Marshal(jitRunnerSetting)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal runner settings: %w", err)
	}

	req, err := c.NewActionsServiceRequest(ctx, http.MethodPost, path, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerJitConfig *RunnerScaleSetJitRunnerConfig
	if err := json.NewDecoder(resp.Body).Decode(&runnerJitConfig); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}
	return runnerJitConfig, nil
}

func (c *Client) GetRunner(ctx context.Context, runnerId int64) (*RunnerReference, error) {
	path := fmt.Sprintf("/%s/%d", runnerEndpoint, runnerId)

	req, err := c.NewActionsServiceRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerReference *RunnerReference
	if err := json.NewDecoder(resp.Body).Decode(&runnerReference); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}

	return runnerReference, nil
}

func (c *Client) GetRunnerByName(ctx context.Context, runnerName string) (*RunnerReference, error) {
	path := fmt.Sprintf("/%s?agentName=%s", runnerEndpoint, runnerName)

	req, err := c.NewActionsServiceRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerList *RunnerReferenceList
	if err := json.NewDecoder(resp.Body).Decode(&runnerList); err != nil {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        err,
		}
	}

	if runnerList.Count == 0 {
		return nil, nil
	}

	if runnerList.Count > 1 {
		return nil, &ActionsError{
			StatusCode: resp.StatusCode,
			ActivityID: resp.Header.Get(HeaderActionsActivityID),
			Err:        fmt.Errorf("multiple runner found with name %s", runnerName),
		}
	}

	return &runnerList.RunnerReferences[0], nil
}

func (c *Client) RemoveRunner(ctx context.Context, runnerId int64) error {
	path := fmt.Sprintf("/%s/%d", runnerEndpoint, runnerId)

	req, err := c.NewActionsServiceRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("failed to create new actions service request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("failed to issue the request: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return ParseActionsErrorFromResponse(resp)
	}

	defer resp.Body.Close()
	return nil
}

type registrationToken struct {
	Token     *string    `json:"token,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (c *Client) getRunnerRegistrationToken(ctx context.Context) (*registrationToken, error) {
	path, err := createRegistrationTokenPath(c.config)
	if err != nil {
		return nil, fmt.Errorf("failed to create registration token path: %w", err)
	}

	var buf bytes.Buffer
	req, err := c.NewGitHubAPIRequest(ctx, http.MethodPost, path, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create new GitHub API request: %w", err)
	}

	bearerToken := ""

	if c.creds.Token != "" {
		bearerToken = fmt.Sprintf("Bearer %v", c.creds.Token)
	} else {
		accessToken, err := c.fetchAccessToken(ctx, c.config.ConfigURL.String(), c.creds.AppCreds)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch access token: %w", err)
		}

		bearerToken = fmt.Sprintf("Bearer %v", accessToken.Token)
	}

	req.Header.Set("Content-Type", "application/vnd.github.v3+json")
	req.Header.Set("Authorization", bearerToken)

	c.logger.Info("getting runner registration token", "registrationTokenURL", req.URL.String())

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read the body: %w", err)
		}
		return nil, &GitHubAPIError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get(HeaderGitHubRequestID),
			Err:        errors.New(string(body)),
		}
	}

	var registrationToken *registrationToken
	if err := json.NewDecoder(resp.Body).Decode(&registrationToken); err != nil {
		return nil, &GitHubAPIError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get(HeaderGitHubRequestID),
			Err:        err,
		}
	}

	return registrationToken, nil
}

// Format: https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app
type accessToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) fetchAccessToken(ctx context.Context, gitHubConfigURL string, creds *GitHubAppAuth) (*accessToken, error) {
	accessTokenJWT, err := createJWTForGitHubApp(creds)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT for GitHub app: %w", err)
	}

	path := fmt.Sprintf("/app/installations/%v/access_tokens", creds.AppInstallationID)
	req, err := c.NewGitHubAPIRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new GitHub API request: %w", err)
	}

	req.Header.Set("Content-Type", "application/vnd.github+json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessTokenJWT))

	c.logger.Info("getting access token for GitHub App auth", "accessTokenURL", req.URL.String())

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to issue the request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		errMsg := fmt.Sprintf("failed to get access token for GitHub App auth (%v)", resp.Status)
		if body, err := io.ReadAll(resp.Body); err == nil {
			errMsg = fmt.Sprintf("%s: %s", errMsg, string(body))
		}

		return nil, &GitHubAPIError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get(HeaderGitHubRequestID),
			Err:        errors.New(errMsg),
		}
	}

	// Format: https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app
	var accessToken *accessToken
	if err = json.NewDecoder(resp.Body).Decode(&accessToken); err != nil {
		return nil, &GitHubAPIError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get(HeaderGitHubRequestID),
			Err:        err,
		}
	}
	return accessToken, nil
}

type ActionsServiceAdminConnection struct {
	ActionsServiceUrl *string `json:"url,omitempty"`
	AdminToken        *string `json:"token,omitempty"`
}

func (c *Client) getActionsServiceAdminConnection(ctx context.Context, rt *registrationToken) (*ActionsServiceAdminConnection, error) {
	path := "/actions/runner-registration"

	body := struct {
		Url         string `json:"url"`
		RunnerEvent string `json:"runner_event"`
	}{
		Url:         c.config.ConfigURL.String(),
		RunnerEvent: "register",
	}

	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(body); err != nil {
		return nil, fmt.Errorf("failed to encode body: %w", err)
	}

	req, err := c.NewGitHubAPIRequest(ctx, http.MethodPost, path, buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create new GitHub API request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("RemoteAuth %s", *rt.Token))

	c.logger.Info("getting Actions tenant URL and JWT", "registrationURL", req.URL.String())

	var resp *http.Response
	retry := 0
	for {
		var err error
		resp, err = c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to issue the request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			break
		}

		var innerErr error
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			innerErr = err
		} else {
			innerErr = errors.New(string(body))
		}

		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			return nil, &GitHubAPIError{
				StatusCode: resp.StatusCode,
				RequestID:  resp.Header.Get(HeaderGitHubRequestID),
				Err:        innerErr,
			}
		}

		retry++
		if retry > 5 {
			return nil, fmt.Errorf("unable to register runner after 3 retries: %w", &GitHubAPIError{
				StatusCode: resp.StatusCode,
				RequestID:  resp.Header.Get(HeaderGitHubRequestID),
				Err:        innerErr,
			})
		}
		// Add exponential backoff + jitter to avoid thundering herd
		// This will generate a backoff schedule:
		// 1: 1s
		// 2: 3s
		// 3: 4s
		// 4: 8s
		// 5: 17s
		baseDelay := 500 * time.Millisecond
		jitter := time.Duration(rand.Intn(1000))
		maxDelay := 20 * time.Second
		delay := baseDelay*(1<<retry) + jitter

		if delay > maxDelay {
			delay = maxDelay
		}

		time.Sleep(delay)
	}

	var actionsServiceAdminConnection *ActionsServiceAdminConnection
	if err := json.NewDecoder(resp.Body).Decode(&actionsServiceAdminConnection); err != nil {
		return nil, &GitHubAPIError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get(HeaderGitHubRequestID),
			Err:        err,
		}
	}

	return actionsServiceAdminConnection, nil
}

func createRegistrationTokenPath(config *GitHubConfig) (string, error) {
	switch config.Scope {
	case GitHubScopeOrganization:
		path := fmt.Sprintf("/orgs/%s/actions/runners/registration-token", config.Organization)
		return path, nil

	case GitHubScopeEnterprise:
		path := fmt.Sprintf("/enterprises/%s/actions/runners/registration-token", config.Enterprise)
		return path, nil

	case GitHubScopeRepository:
		path := fmt.Sprintf("/repos/%s/%s/actions/runners/registration-token", config.Organization, config.Repository)
		return path, nil

	default:
		return "", fmt.Errorf("unknown scope for config url: %s", config.ConfigURL)
	}
}

func createJWTForGitHubApp(appAuth *GitHubAppAuth) (string, error) {
	// Encode as JWT
	// See https://docs.github.com/en/developers/apps/building-github-apps/authenticating-with-github-apps#authenticating-as-a-github-app

	// Going back in time a bit helps with clock skew.
	issuedAt := time.Now().Add(-60 * time.Second)
	// Max expiration date is 10 minutes.
	expiresAt := issuedAt.Add(9 * time.Minute)
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(issuedAt),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		Issuer:    appAuth.AppID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(appAuth.AppPrivateKey))
	if err != nil {
		return "", fmt.Errorf("failed to parse RSA private key from PEM: %w", err)
	}

	return token.SignedString(privateKey)
}

// Returns slice of body without utf-8 byte order mark.
// If BOM does not exist body is returned unchanged.
func trimByteOrderMark(body []byte) []byte {
	return bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
}

func actionsServiceAdminTokenExpiresAt(jwtToken string) (time.Time, error) {
	type JwtClaims struct {
		jwt.RegisteredClaims
	}
	token, _, err := jwt.NewParser().ParseUnverified(jwtToken, &JwtClaims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse jwt token: %w", err)
	}

	if claims, ok := token.Claims.(*JwtClaims); ok {
		return claims.ExpiresAt.Time, nil
	}

	return time.Time{}, fmt.Errorf("failed to parse token claims to get expire at")
}

func (c *Client) updateTokenIfNeeded(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	aboutToExpire := time.Now().Add(60 * time.Second).After(c.ActionsServiceAdminTokenExpiresAt)
	if !aboutToExpire && !c.ActionsServiceAdminTokenExpiresAt.IsZero() {
		return nil
	}

	c.logger.Info("refreshing token", "githubConfigUrl", c.config.ConfigURL.String())
	rt, err := c.getRunnerRegistrationToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get runner registration token on refresh: %w", err)
	}

	adminConnInfo, err := c.getActionsServiceAdminConnection(ctx, rt)
	if err != nil {
		return fmt.Errorf("failed to get actions service admin connection on refresh: %w", err)
	}

	c.ActionsServiceURL = *adminConnInfo.ActionsServiceUrl
	c.ActionsServiceAdminToken = *adminConnInfo.AdminToken
	c.ActionsServiceAdminTokenExpiresAt, err = actionsServiceAdminTokenExpiresAt(*adminConnInfo.AdminToken)
	if err != nil {
		return fmt.Errorf("failed to get admin token expire at on refresh: %w", err)
	}

	return nil
}
