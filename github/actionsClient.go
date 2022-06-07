package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type Label struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type RunnerSetting struct {
	Ephemeral     bool `json:"ephemeral,omitempty"`
	IsElastic     bool `json:"isElastic,omitempty"`
	DisableUpdate bool `json:"disableUpdate,omitempty"`
}

type RunnerScaleSetList struct {
	Count           int              `json:"count"`
	RunnerScaleSets []RunnerScaleSet `json:"value"`
}

type RunnerScaleSet struct {
	Id              int           `json:"id,omitempty"`
	Name            string        `json:"name,omitempty"`
	RunnerGroupId   int           `json:"runnerGroupId,omitempty"`
	RunnerGroupName string        `json:"runnerGroupName,omitempty"`
	Labels          []Label       `json:"labels,omitempty"`
	RunnerSetting   RunnerSetting `json:"RunnerSetting,omitempty"`
	CreatedOn       time.Time     `json:"createdOn,omitempty"`
}

type RunnerScaleSetSession struct {
	SessionId               *uuid.UUID      `json:"sessionId,omitempty"`
	OwnerName               string          `json:"ownerName,omitempty"`
	RunnerScaleSet          *RunnerScaleSet `json:"runnerScaleSet,omitempty"`
	MessageQueueUrl         string          `json:"messageQueueUrl,omitempty"`
	MessageQueueAccessToken string          `json:"messageQueueAccessToken,omitempty"`
}

type RunnerScaleSetMessage struct {
	MessageId   int64  `json:"messageId"`
	MessageType string `json:"messageType"`
	Body        string `json:"body"`
}

type ActionsClient struct {
	*http.Client
	ActionsServiceAdminToken *string
	ActionsServiceURL        *string
	UserAgent                string
}

type MessageQueueTokenExpiredError struct {
	msg string
}

func (e *MessageQueueTokenExpiredError) Error() string {
	return "Message queue token expired" + e.msg
}

func (c *ActionsClient) GetRunnerScaleSet(ctx context.Context, runnerScaleSetName string) (*RunnerScaleSet, error) {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets?api-version=6.0-preview", *c.ActionsServiceURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var runnerScaleSetList *RunnerScaleSetList
		err = json.NewDecoder(resp.Body).Decode(&runnerScaleSetList)
		if err != nil {
			return nil, err
		}

		if runnerScaleSetList.Count == 0 {
			return nil, nil
		}

		for _, runnerScaleSet := range runnerScaleSetList.RunnerScaleSets {
			if runnerScaleSet.Name == runnerScaleSetName {
				return &runnerScaleSet, nil
			}
		}

		return nil, nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error) {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets?api-version=6.0-preview", *c.ActionsServiceURL)

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

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var runnerScaleSet RunnerScaleSet
		err = json.NewDecoder(resp.Body).Decode(&runnerScaleSet)
		if err != nil {
			return nil, err
		}
		return &runnerScaleSet, nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) ReplaceRunnerScaleSet(ctx context.Context, runnerScaleSetId int, runnerScaleSet *RunnerScaleSet) (*RunnerScaleSet, error) {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets/%d?api-version=6.0-preview", *c.ActionsServiceURL, runnerScaleSetId)

	body, err := json.Marshal(runnerScaleSet)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var runnerScaleSet RunnerScaleSet
		err = json.NewDecoder(resp.Body).Decode(&runnerScaleSet)
		if err != nil {
			return nil, err
		}
		return &runnerScaleSet, nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetId int) error {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets/%d/?api-version=6.0-preview", *c.ActionsServiceURL, runnerScaleSetId)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*RunnerScaleSetSession, error) {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets/%d/sessions?api-version=6.0-preview", *c.ActionsServiceURL, runnerScaleSetId)

	newSession := &RunnerScaleSetSession{
		OwnerName: owner,
	}

	body, err := json.Marshal(newSession)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var createdSession RunnerScaleSetSession
		err = json.NewDecoder(resp.Body).Decode(&createdSession)
		if err != nil {
			return nil, err
		}
		return &createdSession, nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*RunnerScaleSetSession, error) {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets/%d/sessions/%s?api-version=6.0-preview", *c.ActionsServiceURL, runnerScaleSetId, sessionId.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var refreshedSession RunnerScaleSetSession
		err = json.NewDecoder(resp.Body).Decode(&refreshedSession)
		if err != nil {
			return nil, err
		}
		return &refreshedSession, nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error {
	u := fmt.Sprintf("%s/_apis/runtime/runnerscalesets/%d/sessions/%s?api-version=6.0-preview", *c.ActionsServiceURL, runnerScaleSetId, sessionId)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *c.ActionsServiceAdminToken))

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64) (*RunnerScaleSetMessage, error) {
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
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil, nil
	} else if resp.StatusCode == http.StatusOK {
		var message RunnerScaleSetMessage
		err = json.NewDecoder(resp.Body).Decode(&message)
		if err != nil {
			return nil, err
		}
		return &message, nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &MessageQueueTokenExpiredError{msg: string(body)}
		} else {
			return nil, fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
		}
	}
}

func (c *ActionsClient) DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error {
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
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}

func (c *ActionsClient) AcquireJob(ctx context.Context, acquireJobUrl, messageQueueAccessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, acquireJobUrl, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", messageQueueAccessToken))
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	newClient := &http.Client{}

	resp, err := newClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected status code: %d - body: %s", resp.StatusCode, string(body))
	}
}
