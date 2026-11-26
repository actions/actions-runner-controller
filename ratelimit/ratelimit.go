// Package ratelimit reads the status and rate limit state of the responses
// the scale set listener and controller receive from GitHub.
package ratelimit

import (
	"bytes"
	"context"
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
func readResponse(resp *http.Response, message []byte) Response {
	r := Response{
		Backend:    backendOf(resp.Request),
		StatusCode: resp.StatusCode,
		Resource:   resp.Header.Get(headerRateLimitResource),
	}

	primary := resp.Header.Get(headerRateLimitRemaining) == "0"
	secondary := secondaryRateLimitPattern.Match(message)
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

// peekMessage returns the start of a 403 or 429 body and leaves the body
// readable from the beginning for the caller.
func peekMessage(resp *http.Response) []byte {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests || resp.Body == nil {
		return nil
	}
	message, _ := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(message), resp.Body), Closer: resp.Body}
	return message
}

type readCloser struct {
	io.Reader
	io.Closer
}
