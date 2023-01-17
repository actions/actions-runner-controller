package actions

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ActionsError struct {
	ExceptionName string `json:"typeName,omitempty"`
	Message       string `json:"message,omitempty"`
	StatusCode    int
}

func (e *ActionsError) Error() string {
	return fmt.Sprintf("%v - had issue communicating with Actions backend: %v", e.StatusCode, e.Message)
}

func ParseActionsErrorFromResponse(response *http.Response) error {
	if response.ContentLength == 0 {
		message := "Request returned status: " + response.Status
		return &ActionsError{
			ExceptionName: "unknown",
			Message:       message,
			StatusCode:    response.StatusCode,
		}
	}

	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	body = trimByteOrderMark(body)
	contentType, ok := response.Header["Content-Type"]
	if ok && len(contentType) > 0 && strings.Contains(contentType[0], "text/plain") {
		message := string(body)
		statusCode := response.StatusCode
		return &ActionsError{
			Message:    message,
			StatusCode: statusCode,
		}
	}

	actionsError := &ActionsError{StatusCode: response.StatusCode}
	if err := json.Unmarshal(body, &actionsError); err != nil {
		return err
	}

	return actionsError
}

type MessageQueueTokenExpiredError struct {
	msg string
}

func (e *MessageQueueTokenExpiredError) Error() string {
	return e.msg
}

type HttpClientSideError struct {
	msg  string
	Code int
}

func (e *HttpClientSideError) Error() string {
	return e.msg
}
