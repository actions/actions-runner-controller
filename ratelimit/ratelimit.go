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

// Operation is the scale set API call a request made.
type Operation string

const (
	OperationAccessToken        Operation = "access_token"
	OperationRegistrationToken  Operation = "registration_token"
	OperationRunnerRegistration Operation = "runner_registration"
	OperationScaleSet           Operation = "scale_set"
	OperationRunnerGroup        Operation = "runner_group"
	OperationSession            Operation = "session"
	OperationMessage            Operation = "message"
	OperationAcquireJobs        Operation = "acquire_jobs"
	OperationJITConfig          Operation = "jit_config"
	OperationRunner             Operation = "runner"
	OperationOther              Operation = "other"
)

// operations maps the URL paths scaleset calls to operations, most specific
// first.
var operations = []struct {
	path      *regexp.Regexp
	operation Operation
}{
	{regexp.MustCompile(`/app/installations/[^/]+/access_tokens$`), OperationAccessToken},
	{regexp.MustCompile(`/actions/runners/registration-token$`), OperationRegistrationToken},
	{regexp.MustCompile(`/actions/runner-registration$`), OperationRunnerRegistration},
	{regexp.MustCompile(`/_apis/runtime/runnerscalesets/[^/]+/generatejitconfig$`), OperationJITConfig},
	{regexp.MustCompile(`/_apis/runtime/runnerscalesets/[^/]+/acquirejobs$`), OperationAcquireJobs},
	{regexp.MustCompile(`/_apis/runtime/runnerscalesets/[^/]+/sessions(/[^/]+)?$`), OperationSession},
	{regexp.MustCompile(`/_apis/runtime/runnerscalesets(/[^/]+)?/?$`), OperationScaleSet},
	{regexp.MustCompile(`/_apis/runtime/runnergroups/?$`), OperationRunnerGroup},
	{regexp.MustCompile(`/_apis/distributedtask/pools/[^/]+/agents(/[^/]+)?/?$`), OperationRunner},
	{regexp.MustCompile(`/message(/[^/]+)?$`), OperationMessage},
}

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
	Operation   Operation
	StatusCode  int
	RateLimited Kind
	// Resource is the rate limit resource the request counted against, from
	// X-RateLimit-Resource. It is empty when the response has no such header.
	Resource string
	// Reason is the Actions service exception of a 403 or 429 response, such
	// as ConfigureTooManyRunnersException. It is empty for other responses.
	Reason string
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
func readResponse(resp *http.Response, e exception) Response {
	r := requestOf(resp.Request)
	r.StatusCode = resp.StatusCode
	r.Resource = resp.Header.Get(headerRateLimitResource)
	r.Reason = e.reason()

	primary := resp.Header.Get(headerRateLimitRemaining) == "0"
	secondary := secondaryRateLimitPattern.MatchString(e.Message)
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

// requestOf tells the backend and operation apart by URL. The path decides
// before the host because the broker host also serves the Actions service.
func requestOf(req *http.Request) Response {
	r := Response{Backend: BackendREST, Operation: OperationOther}
	if req == nil {
		return r
	}
	switch {
	case strings.Contains(req.URL.Path, "/_apis/"):
		r.Backend = BackendActions
	case strings.HasPrefix(req.URL.Hostname(), "broker."):
		r.Backend = BackendBroker
	}
	for _, o := range operations {
		if o.path.MatchString(req.URL.Path) {
			r.Operation = o.operation
			break
		}
	}
	return r
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
		t.Observe(readResponse(resp, peekException(resp)))
	case !errors.Is(req.Context().Err(), context.Canceled):
		t.Observe(requestOf(req))
	}
	return resp, err
}

// peekException returns the error of a 403 or 429 response and leaves the
// body readable from the beginning for the caller, including any error the
// read hit.
func peekException(resp *http.Response) exception {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests || resp.Body == nil {
		return exception{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes))
	var rest io.Reader = resp.Body
	if err != nil {
		rest = errReader{err: err}
	}
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(body), rest), Closer: resp.Body}
	return exceptionOf(resp.Header.Get("Content-Type"), body)
}

// exception is the error body of a GitHub or Actions service response.
type exception struct {
	TypeName string `json:"typeName"`
	Message  string `json:"message"`
}

// reason is the exception class name without its namespace and assembly.
func (e exception) reason() string {
	name, _, _ := strings.Cut(e.TypeName, ",")
	return name[strings.LastIndex(name, ".")+1:]
}

// exceptionOf reads the body the way scaleset does: the whole body is the
// message when it is plain text, otherwise the body is a JSON exception.
func exceptionOf(contentType string, body []byte) exception {
	if strings.Contains(contentType, "text/plain") {
		return exception{Message: string(body)}
	}
	var e exception
	if json.Unmarshal(body, &e) != nil {
		return exception{}
	}
	return e
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
