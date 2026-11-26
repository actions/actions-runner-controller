// Package ratelimit reads the status and rate limit state of the responses
// the scale set listener and controller receive from GitHub.
package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Backend is the GitHub backend a request went to.
type Backend string

const (
	// BackendREST is the GitHub REST API.
	BackendREST Backend = "rest"
	// BackendActions is the Actions service, which serves the _apis paths.
	BackendActions Backend = "actions"
	// BackendBroker is the message broker the listener long-polls.
	BackendBroker Backend = "broker"
)

// Kind tells which limit rejected a request. It is empty when the
// request was not rate limited.
type Kind string

const (
	Primary   Kind = "primary"
	Secondary Kind = "secondary"
)

// Response is what the metrics need from one HTTP response.
type Response struct {
	Backend     Backend
	StatusCode  int
	RateLimited Kind
	// Resource is the rate limit resource the request counted against, from
	// X-RateLimit-Resource. It is empty when the response has no such header.
	Resource string
}

// Status is the status code as a metric label value, or "error" when the
// request got no response.
func (r Response) Status() string {
	if r.StatusCode == 0 {
		return "error"
	}
	return strconv.Itoa(r.StatusCode)
}

const (
	headerRateLimitRemaining = "X-RateLimit-Remaining"
	headerRateLimitResource  = "X-RateLimit-Resource"
	headerRetryAfter         = "Retry-After"
)

// maxMessageBytes bounds how much of a 403 or 429 body is read to find a
// secondary rate limit message.
const maxMessageBytes = 64 << 10

var secondaryRateLimitPattern = regexp.MustCompile(`(?i)\bsecondary rate\b`)

// readResponse classifies a response with the rules of scaleset.RateLimitError.
func readResponse(resp *http.Response, message string) Response {
	r := Response{
		Backend:    backendOf(resp.Request),
		StatusCode: resp.StatusCode,
		Resource:   resp.Header.Get(headerRateLimitResource),
	}

	primary := resp.Header.Get(headerRateLimitRemaining) == "0"
	secondary := secondaryRateLimitPattern.MatchString(message)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && (primary || secondary || resp.Header.Get(headerRetryAfter) != ""):
		r.RateLimited = Secondary
		if primary && !secondary {
			r.RateLimited = Primary
		}
	}
	return r
}

// backendOf tells the services apart by URL. On GitHub Enterprise Server the
// broker can share the Actions service host and paths, so its requests count
// as actions there.
func backendOf(req *http.Request) Backend {
	if req == nil {
		return BackendREST
	}
	switch {
	case strings.HasPrefix(req.URL.Hostname(), "broker."):
		return BackendBroker
	case strings.Contains(req.URL.Path, "/_apis/"):
		return BackendActions
	default:
		return BackendREST
	}
}

// Transport wraps a transport and reports every attempt to Observe, so each
// retry is reported separately. An attempt that fails without a response is
// reported with status code 0, unless its request was canceled.
type Transport struct {
	Transport http.RoundTripper
	Observe   func(Response)
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	switch {
	case resp != nil:
		t.Observe(readResponse(resp, peekMessage(resp)))
	case !errors.Is(req.Context().Err(), context.Canceled):
		t.Observe(Response{Backend: backendOf(req)})
	}
	return resp, err
}

// peekMessage returns the error message of a 403 or 429 response and leaves
// the body readable from the beginning for the caller, including any error
// the read hit.
func peekMessage(resp *http.Response) string {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests || resp.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	var rest io.Reader = resp.Body
	if err != nil {
		rest = errReader{err: err}
	}
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(body), rest), Closer: resp.Body}
	return messageOf(resp.Header.Get("Content-Type"), body)
}

// messageOf reads the message the way scaleset does: the whole body when it
// is plain text, otherwise the message field of a JSON body.
func messageOf(contentType string, body []byte) string {
	if strings.Contains(contentType, "text/plain") {
		return string(body)
	}
	var exception struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &exception) != nil {
		return ""
	}
	return exception.Message
}

type readCloser struct {
	io.Reader
	io.Closer
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}
