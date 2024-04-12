package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Header names for request IDs
const (
	HeaderActionsActivityID = "ActivityId"
	HeaderGitHubRequestID   = "X-GitHub-Request-Id"
)

type GitHubAPIError struct {
	StatusCode int
	RequestID  string
	Err        error
}

func (e *GitHubAPIError) Error() string {
	return fmt.Sprintf("github api error: StatusCode %d, RequestID %q: %v", e.StatusCode, e.RequestID, e.Err)
}

func (e *GitHubAPIError) Unwrap() error {
	return e.Err
}

type ActionsError struct {
	ActivityID string
	StatusCode int
	Err        error
}

func (e *ActionsError) Error() string {
	return fmt.Sprintf("actions error: StatusCode %d, AcivityId %q: %v", e.StatusCode, e.ActivityID, e.Err)
}

func (e *ActionsError) Unwrap() error {
	return e.Err
}

func (e *ActionsError) IsException(target string) bool {
	if exception, ok := e.Err.(*ActionsExceptionError); ok {
		return exception.ExceptionName == target
	}

	return false
}

type ActionsExceptionError struct {
	ExceptionName string `json:"typeName,omitempty"`
	Message       string `json:"message,omitempty"`
}

func (e *ActionsExceptionError) Error() string {
	return fmt.Sprintf("%s: %s", e.ExceptionName, e.Message)
}

func ParseActionsErrorFromResponse(response *http.Response) error {
	if response.ContentLength == 0 {
		return &ActionsError{
			ActivityID: response.Header.Get(HeaderActionsActivityID),
			StatusCode: response.StatusCode,
			Err:        errors.New("unknown exception"),
		}
	}

	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return &ActionsError{
			ActivityID: response.Header.Get(HeaderActionsActivityID),
			StatusCode: response.StatusCode,
			Err:        err,
		}
	}

	body = trimByteOrderMark(body)
	contentType, ok := response.Header["Content-Type"]
	if ok && len(contentType) > 0 && strings.Contains(contentType[0], "text/plain") {
		message := string(body)
		return &ActionsError{
			ActivityID: response.Header.Get(HeaderActionsActivityID),
			StatusCode: response.StatusCode,
			Err:        errors.New(message),
		}
	}

	var exception ActionsExceptionError
	if err := json.Unmarshal(body, &exception); err != nil {
		return &ActionsError{
			ActivityID: response.Header.Get(HeaderActionsActivityID),
			StatusCode: response.StatusCode,
			Err:        err,
		}
	}

	return &ActionsError{
		ActivityID: response.Header.Get(HeaderActionsActivityID),
		StatusCode: response.StatusCode,
		Err:        &exception,
	}
}

type MessageQueueTokenExpiredError struct {
	activityID string
	statusCode int
	msg        string
}

func (e *MessageQueueTokenExpiredError) Error() string {
	return fmt.Sprintf("MessageQueueTokenExpiredError: AcivityId %q, StatusCode %d: %s", e.activityID, e.statusCode, e.msg)
}

type HttpClientSideError struct {
	msg  string
	Code int
}

func (e *HttpClientSideError) Error() string {
	return e.msg
}
