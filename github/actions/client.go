package actions

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

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

//go:generate mockery --inpackage --name=ActionsService
type ActionsService interface {
	GetRunnerScaleSet(ctx context.Context, runnerScaleSetName string) (*RunnerScaleSet, error)
	GetRunnerScaleSetById(ctx context.Context, runnerScaleSetId int) (*RunnerScaleSet, error)
	GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*RunnerGroup, error)
	CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error)

	CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*RunnerScaleSetSession, error)
	DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error
	RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*RunnerScaleSetSession, error)

	AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error)
	GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*AcquirableJobList, error)

	GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64) (*RunnerScaleSetMessage, error)
	DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error

	GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *RunnerScaleSetJitRunnerSetting, scaleSetId int) (*RunnerScaleSetJitRunnerConfig, error)

	GetRunner(ctx context.Context, runnerId int64) (*RunnerReference, error)
	GetRunnerByName(ctx context.Context, runnerName string) (*RunnerReference, error)
	RemoveRunner(ctx context.Context, runnerId int64) error
}

type Client struct {
	*http.Client

	// lock for refreshing the ActionsServiceAdminToken and ActionsServiceAdminTokenExpiresAt
	mu sync.Mutex

	// TODO: Convert to unexported fields once refactor of Listener is complete
	ActionsServiceAdminToken          *string
	ActionsServiceAdminTokenExpiresAt *time.Time
	ActionsServiceURL                 *string

	retryMax     int
	retryWaitMax time.Duration

	creds           *ActionsAuth
	githubConfigURL string
	logger          logr.Logger
	userAgent       string

	rootCAs               *x509.CertPool
	tlsInsecureSkipVerify bool
}

type ClientOption func(*Client)

func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		c.userAgent = userAgent
	}
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

func NewClient(ctx context.Context, githubConfigURL string, creds *ActionsAuth, options ...ClientOption) (ActionsService, error) {
	ac := &Client{
		creds:           creds,
		githubConfigURL: githubConfigURL,
		logger:          logr.Discard(),

		// retryablehttp defaults
		retryMax:     4,
		retryWaitMax: 30 * time.Second,
	}

	for _, option := range options {
		option(ac)
	}

	retryClient := retryablehttp.NewClient()

	// TODO: this silences retryclient default logger, do we want to provide one
	// instead? by default retryablehttp logs all requests to stderr
	retryClient.Logger = log.New(io.Discard, "", log.LstdFlags)

	retryClient.RetryMax = ac.retryMax
	retryClient.RetryWaitMax = ac.retryWaitMax

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

	retryClient.HTTPClient.Transport = transport
	ac.Client = retryClient.StandardClient()

	rt, err := ac.getRunnerRegistrationToken(ctx, githubConfigURL, *creds)
	if err != nil {
		return nil, fmt.Errorf("failed to get runner registration token: %w", err)
	}

	adminConnInfo, err := ac.getActionsServiceAdminConnection(ctx, rt, githubConfigURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions service admin connection: %w", err)
	}

	ac.ActionsServiceURL = adminConnInfo.ActionsServiceUrl

	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.ActionsServiceAdminToken = adminConnInfo.AdminToken
	ac.ActionsServiceAdminTokenExpiresAt, err = actionsServiceAdminTokenExpiresAt(*adminConnInfo.AdminToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get admin token expire at: %w", err)
	}

	return ac, nil
}

func (c *Client) GetRunnerScaleSet(ctx context.Context, runnerScaleSetName string) (*RunnerScaleSet, error) {
	u := fmt.Sprintf("%s/%s?name=%s&api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetName)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}
	var runnerScaleSetList *runnerScaleSetsResponse
	err = unmarshalBody(resp, &runnerScaleSetList)
	if err != nil {
		return nil, err
	}
	if runnerScaleSetList.Count == 0 {
		return nil, nil
	}
	if runnerScaleSetList.Count > 1 {
		return nil, fmt.Errorf("multiple runner scale sets found with name %s", runnerScaleSetName)
	}

	return &runnerScaleSetList.RunnerScaleSets[0], nil
}

func (c *Client) GetRunnerScaleSetById(ctx context.Context, runnerScaleSetId int) (*RunnerScaleSet, error) {
	u := fmt.Sprintf("%s/%s/%d?api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerScaleSet *RunnerScaleSet
	err = unmarshalBody(resp, &runnerScaleSet)
	if err != nil {
		return nil, err
	}
	return runnerScaleSet, nil
}

func (c *Client) GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*RunnerGroup, error) {
	u := fmt.Sprintf("%s/_apis/runtime/runnergroups/?groupName=%s&api-version=6.0-preview", *c.ActionsServiceURL, runnerGroup)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}

	var runnerGroupList *RunnerGroupList
	err = unmarshalBody(resp, &runnerGroupList)
	if err != nil {
		return nil, err
	}

	if runnerGroupList.Count == 0 {
		return nil, nil
	}

	if runnerGroupList.Count > 1 {
		return nil, fmt.Errorf("multiple runner group found with name %s", runnerGroup)
	}

	return &runnerGroupList.RunnerGroups[0], nil
}

func (c *Client) CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error) {
	u := fmt.Sprintf("%s/%s?api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	body, err := json.Marshal(runnerScaleSet)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}
	var createdRunnerScaleSet *RunnerScaleSet
	err = unmarshalBody(resp, &createdRunnerScaleSet)
	if err != nil {
		return nil, err
	}
	return createdRunnerScaleSet, nil
}

func (c *Client) DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetId int) error {
	u := fmt.Sprintf("%s/%s/%d?api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusNoContent {
		return ParseActionsErrorFromResponse(resp)
	}

	defer resp.Body.Close()
	return nil
}

func (c *Client) GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64) (*RunnerScaleSetMessage, error) {
	u := messageQueueUrl
	if lastMessageId > 0 {
		u = fmt.Sprintf("%s&lassMessageId=%d", messageQueueUrl, lastMessageId)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json; api-version=6.0-preview")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		return nil, &MessageQueueTokenExpiredError{msg: string(body)}
	}

	var message *RunnerScaleSetMessage
	err = unmarshalBody(resp, &message)
	if err != nil {
		return nil, err
	}
	return message, nil
}

func (c *Client) DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error {
	u, err := url.Parse(messageQueueUrl)
	if err != nil {
		return err
	}

	u.Path = fmt.Sprintf("%s/%d", u.Path, messageId)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusNoContent {
		if resp.StatusCode != http.StatusUnauthorized {
			return ParseActionsErrorFromResponse(resp)
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		body = trimByteOrderMark(body)
		if err != nil {
			return err
		}
		return &MessageQueueTokenExpiredError{msg: string(body)}
	}
	return nil
}

func (c *Client) CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*RunnerScaleSetSession, error) {
	u := fmt.Sprintf("%v/%v/%v/sessions?%v", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId, apiVersionQueryParam)

	newSession := &RunnerScaleSetSession{
		OwnerName: owner,
	}

	requestData, err := json.Marshal(newSession)
	if err != nil {
		return nil, err
	}

	createdSession := &RunnerScaleSetSession{}

	err = c.doSessionRequest(ctx, http.MethodPost, u, bytes.NewBuffer(requestData), http.StatusOK, createdSession)

	return createdSession, err
}

func (c *Client) DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error {
	u := fmt.Sprintf("%v/%v/%v/sessions/%v?%v", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId, sessionId.String(), apiVersionQueryParam)

	return c.doSessionRequest(ctx, http.MethodDelete, u, nil, http.StatusNoContent, nil)
}

func (c *Client) RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*RunnerScaleSetSession, error) {
	u := fmt.Sprintf("%v/%v/%v/sessions/%v?%v", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId, sessionId.String(), apiVersionQueryParam)
	refreshedSession := &RunnerScaleSetSession{}
	err := c.doSessionRequest(ctx, http.MethodPatch, u, nil, http.StatusOK, refreshedSession)
	return refreshedSession, err
}

func (c *Client) doSessionRequest(ctx context.Context, method, url string, requestData io.Reader, expectedResponseStatusCode int, responseUnmarshalTarget any) error {
	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, requestData)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode == expectedResponseStatusCode && responseUnmarshalTarget != nil {
		err = unmarshalBody(resp, &responseUnmarshalTarget)
		return err
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return ParseActionsErrorFromResponse(resp)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	body = trimByteOrderMark(body)
	if err != nil {
		return err
	}

	return fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
}

func (c *Client) AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error) {
	u := fmt.Sprintf("%s/%s/%d/acquirejobs?api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId)

	body, err := json.Marshal(requestIds)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var acquiredJobs Int64List
	err = unmarshalBody(resp, &acquiredJobs)
	if err != nil {
		return nil, err
	}

	return acquiredJobs.Value, nil
}

func (c *Client) GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*AcquirableJobList, error) {
	u := fmt.Sprintf("%s/%s/%d/acquirablejobs?api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint, runnerScaleSetId)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNoContent {
		defer resp.Body.Close()
		return &AcquirableJobList{Count: 0, Jobs: []AcquirableJob{}}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var acquirableJobList *AcquirableJobList
	err = unmarshalBody(resp, &acquirableJobList)
	if err != nil {
		return nil, err
	}

	return acquirableJobList, nil
}

func (c *Client) GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *RunnerScaleSetJitRunnerSetting, scaleSetId int) (*RunnerScaleSetJitRunnerConfig, error) {
	runnerJitConfigUrl := fmt.Sprintf("%s/%s/%d/generatejitconfig?api-version=6.0-preview", *c.ActionsServiceURL, scaleSetEndpoint, scaleSetId)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	body, err := json.Marshal(jitRunnerSetting)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, runnerJitConfigUrl, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerJitConfig *RunnerScaleSetJitRunnerConfig
	err = unmarshalBody(resp, &runnerJitConfig)
	if err != nil {
		return nil, err
	}
	return runnerJitConfig, nil
}

func (c *Client) GetRunner(ctx context.Context, runnerId int64) (*RunnerReference, error) {
	url := fmt.Sprintf("%v/%v/%v?%v", *c.ActionsServiceURL, runnerEndpoint, runnerId, apiVersionQueryParam)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerReference *RunnerReference
	if err := unmarshalBody(resp, &runnerReference); err != nil {
		return nil, err
	}

	return runnerReference, nil
}

func (c *Client) GetRunnerByName(ctx context.Context, runnerName string) (*RunnerReference, error) {
	url := fmt.Sprintf("%v/%v?agentName=%v&%v", *c.ActionsServiceURL, runnerEndpoint, runnerName, apiVersionQueryParam)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ParseActionsErrorFromResponse(resp)
	}

	var runnerList *RunnerReferenceList
	err = unmarshalBody(resp, &runnerList)
	if err != nil {
		return nil, err
	}

	if runnerList.Count == 0 {
		return nil, nil
	}

	if runnerList.Count > 1 {
		return nil, fmt.Errorf("multiple runner found with name %s", runnerName)
	}

	return &runnerList.RunnerReferences[0], nil
}

func (c *Client) RemoveRunner(ctx context.Context, runnerId int64) error {
	url := fmt.Sprintf("%v/%v/%v?%v", *c.ActionsServiceURL, runnerEndpoint, runnerId, apiVersionQueryParam)

	if err := c.refreshTokenIfNeeded(ctx); err != nil {
		return fmt.Errorf("failed to refresh admin token if needed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
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

func (c *Client) getRunnerRegistrationToken(ctx context.Context, githubConfigUrl string, creds ActionsAuth) (*registrationToken, error) {
	registrationTokenURL, err := createRegistrationTokenURL(githubConfigUrl)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationTokenURL, &buf)
	if err != nil {
		return nil, err
	}

	bearerToken := ""

	if creds.Token != "" {
		encodedToken := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("github:%v", creds.Token)))
		bearerToken = fmt.Sprintf("Basic %v", encodedToken)
	} else {
		accessToken, err := c.fetchAccessToken(ctx, githubConfigUrl, creds.AppCreds)
		if err != nil {
			return nil, err
		}

		bearerToken = fmt.Sprintf("Bearer %v", accessToken.Token)
	}

	req.Header.Set("Content-Type", "application/vnd.github.v3+json")
	req.Header.Set("Authorization", bearerToken)
	req.Header.Set("User-Agent", c.userAgent)

	c.logger.Info("getting runner registration token", "registrationTokenURL", registrationTokenURL)

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected response from Actions service during registration token call: %v - %v", resp.StatusCode, string(body))
	}

	registrationToken := &registrationToken{}
	if err := json.NewDecoder(resp.Body).Decode(registrationToken); err != nil {
		return nil, err
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
		return nil, err
	}

	u, err := githubAPIURL(gitHubConfigURL, fmt.Sprintf("/app/installations/%v/access_tokens", creds.AppInstallationID))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/vnd.github+json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessTokenJWT))
	req.Header.Add("User-Agent", c.userAgent)

	c.logger.Info("getting access token for GitHub App auth", "accessTokenURL", u)

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Format: https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app
	accessToken := &accessToken{}
	err = json.NewDecoder(resp.Body).Decode(accessToken)
	return accessToken, err
}

type ActionsServiceAdminConnection struct {
	ActionsServiceUrl *string `json:"url,omitempty"`
	AdminToken        *string `json:"token,omitempty"`
}

func (c *Client) getActionsServiceAdminConnection(ctx context.Context, rt *registrationToken, githubConfigUrl string) (*ActionsServiceAdminConnection, error) {
	parsedGitHubConfigURL, err := url.Parse(githubConfigUrl)
	if err != nil {
		return nil, err
	}

	if isHostedServer(*parsedGitHubConfigURL) {
		parsedGitHubConfigURL.Host = fmt.Sprintf("api.%v", parsedGitHubConfigURL.Host)
	}

	ru := fmt.Sprintf("%v://%v/actions/runner-registration", parsedGitHubConfigURL.Scheme, parsedGitHubConfigURL.Host)
	registrationURL, err := url.Parse(ru)
	if err != nil {
		return nil, err
	}

	body := struct {
		Url         string `json:"url"`
		RunnerEvent string `json:"runner_event"`
	}{
		Url:         githubConfigUrl,
		RunnerEvent: "register",
	}

	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(body); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationURL.String(), buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("RemoteAuth %s", *rt.Token))
	req.Header.Set("User-Agent", c.userAgent)

	c.logger.Info("getting Actions tenant URL and JWT", "registrationURL", registrationURL.String())

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	actionsServiceAdminConnection := &ActionsServiceAdminConnection{}
	if err := json.NewDecoder(resp.Body).Decode(actionsServiceAdminConnection); err != nil {
		return nil, err
	}

	return actionsServiceAdminConnection, nil
}

func isHostedServer(gitHubURL url.URL) bool {
	return gitHubURL.Host == "github.com" ||
		gitHubURL.Host == "www.github.com" ||
		gitHubURL.Host == "github.localhost"
}

func createRegistrationTokenURL(githubConfigUrl string) (string, error) {
	parsedGitHubConfigURL, err := url.Parse(githubConfigUrl)
	if err != nil {
		return "", err
	}

	// Check for empty path before split, because strings.Split will return a slice of length 1
	// when the split delimiter is not present.
	trimmedPath := strings.TrimLeft(parsedGitHubConfigURL.Path, "/")
	if len(trimmedPath) == 0 {
		return "", fmt.Errorf("%q should point to an enterprise, org, or repository", parsedGitHubConfigURL.String())
	}

	pathParts := strings.Split(path.Clean(strings.TrimLeft(parsedGitHubConfigURL.Path, "/")), "/")

	switch len(pathParts) {
	case 1: // Organization
		registrationTokenURL := fmt.Sprintf(
			"%v://%v/api/v3/orgs/%v/actions/runners/registration-token",
			parsedGitHubConfigURL.Scheme, parsedGitHubConfigURL.Host, pathParts[0])

		if isHostedServer(*parsedGitHubConfigURL) {
			registrationTokenURL = fmt.Sprintf(
				"%v://api.%v/orgs/%v/actions/runners/registration-token",
				parsedGitHubConfigURL.Scheme, parsedGitHubConfigURL.Host, pathParts[0])
		}

		return registrationTokenURL, nil
	case 2: // Repository or enterprise
		repoScope := "repos/"
		if strings.ToLower(pathParts[0]) == "enterprises" {
			repoScope = ""
		}

		registrationTokenURL := fmt.Sprintf("%v://%v/api/v3/%v%v/%v/actions/runners/registration-token",
			parsedGitHubConfigURL.Scheme, parsedGitHubConfigURL.Host, repoScope, pathParts[0], pathParts[1])

		if isHostedServer(*parsedGitHubConfigURL) {
			registrationTokenURL = fmt.Sprintf("%v://api.%v/%v%v/%v/actions/runners/registration-token",
				parsedGitHubConfigURL.Scheme, parsedGitHubConfigURL.Host, repoScope, pathParts[0], pathParts[1])
		}

		return registrationTokenURL, nil
	default:
		return "", fmt.Errorf("%q should point to an enterprise, org, or repository", parsedGitHubConfigURL.String())
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
		Issuer:    strconv.FormatInt(appAuth.AppID, 10),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(appAuth.AppPrivateKey))
	if err != nil {
		return "", err
	}

	return token.SignedString(privateKey)
}

func unmarshalBody(response *http.Response, v interface{}) (err error) {
	if response != nil && response.Body != nil {
		var err error
		defer func() {
			if closeError := response.Body.Close(); closeError != nil {
				err = closeError
			}
		}()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		body = trimByteOrderMark(body)
		return json.Unmarshal(body, &v)
	}
	return nil
}

// Returns slice of body without utf-8 byte order mark.
// If BOM does not exist body is returned unchanged.
func trimByteOrderMark(body []byte) []byte {
	return bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
}

func actionsServiceAdminTokenExpiresAt(jwtToken string) (*time.Time, error) {
	type JwtClaims struct {
		jwt.RegisteredClaims
	}
	token, _, err := jwt.NewParser().ParseUnverified(jwtToken, &JwtClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse jwt token: %w", err)
	}

	if claims, ok := token.Claims.(*JwtClaims); ok {
		return &claims.ExpiresAt.Time, nil
	}

	return nil, fmt.Errorf("failed to parse token claims to get expire at")
}

func (c *Client) refreshTokenIfNeeded(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	aboutToExpire := time.Now().Add(60 * time.Second).After(*c.ActionsServiceAdminTokenExpiresAt)
	if !aboutToExpire {
		return nil
	}

	c.logger.Info("Admin token is about to expire, refreshing it", "githubConfigUrl", c.githubConfigURL)
	rt, err := c.getRunnerRegistrationToken(ctx, c.githubConfigURL, *c.creds)
	if err != nil {
		return fmt.Errorf("failed to get runner registration token on fresh: %w", err)
	}

	adminConnInfo, err := c.getActionsServiceAdminConnection(ctx, rt, c.githubConfigURL)
	if err != nil {
		return fmt.Errorf("failed to get actions service admin connection on fresh: %w", err)
	}

	c.ActionsServiceURL = adminConnInfo.ActionsServiceUrl
	c.ActionsServiceAdminToken = adminConnInfo.AdminToken
	c.ActionsServiceAdminTokenExpiresAt, err = actionsServiceAdminTokenExpiresAt(*adminConnInfo.AdminToken)
	if err != nil {
		return fmt.Errorf("failed to get admin token expire at on refresh: %w", err)
	}

	return nil
}

func githubAPIURL(configURL, path string) (string, error) {
	u, err := url.Parse(configURL)
	if err != nil {
		return "", err
	}

	result := &url.URL{
		Scheme: u.Scheme,
	}

	switch u.Host {
	// Hosted
	case "github.com", "github.localhost":
		result.Host = fmt.Sprintf("api.%s", u.Host)
	// re-routing www.github.com to api.github.com
	case "www.github.com":
		result.Host = "api.github.com"

	// Enterprise
	default:
		result.Host = u.Host
		result.Path = "/api/v3"
	}

	result.Path += path

	return result.String(), nil
}
