package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	actionsv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v37/github"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	sc = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(sc)
	_ = actionsv1alpha1.AddToScheme(sc)
}

func TestOrgWebhookCheckRun(t *testing.T) {
	f, err := os.Open("testdata/org_webhook_check_run_payload.json")
	if err != nil {
		t.Fatalf("could not open the fixture: %s", err)
	}
	defer f.Close()
	var e github.CheckRunEvent
	if err := json.NewDecoder(f).Decode(&e); err != nil {
		t.Fatalf("invalid json: %s", err)
	}
	testServer(t,
		"check_run",
		&e,
		200,
		"no horizontalrunnerautoscaler to scale for this github event",
	)
}

func TestRepoWebhookCheckRun(t *testing.T) {
	f, err := os.Open("testdata/repo_webhook_check_run_payload.json")
	if err != nil {
		t.Fatalf("could not open the fixture: %s", err)
	}
	defer f.Close()
	var e github.CheckRunEvent
	if err := json.NewDecoder(f).Decode(&e); err != nil {
		t.Fatalf("invalid json: %s", err)
	}
	testServer(t,
		"check_run",
		&e,
		200,
		"no horizontalrunnerautoscaler to scale for this github event",
	)
}

func TestWebhookPullRequest(t *testing.T) {
	testServer(t,
		"pull_request",
		&github.PullRequestEvent{
			PullRequest: &github.PullRequest{
				Base: &github.PullRequestBranch{
					Ref: github.String("main"),
				},
			},
			Repo: &github.Repository{
				Name: github.String("myorg/myrepo"),
				Organization: &github.Organization{
					Name: github.String("myorg"),
				},
			},
			Action: github.String("created"),
		},
		200,
		"no horizontalrunnerautoscaler to scale for this github event",
	)
}

func TestWebhookPush(t *testing.T) {
	testServer(t,
		"push",
		&github.PushEvent{
			Repo: &github.PushEventRepository{
				Name:         github.String("myrepo"),
				Organization: github.String("myorg"),
			},
		},
		200,
		"no horizontalrunnerautoscaler to scale for this github event",
	)
}

func TestWebhookPing(t *testing.T) {
	testServer(t,
		"ping",
		&github.PingEvent{
			Zen: github.String("zen"),
		},
		200,
		"pong",
	)
}

func TestGetRequest(t *testing.T) {
	hra := HorizontalRunnerAutoscalerGitHubWebhook{}
	request, _ := http.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.ResponseRecorder{}

	hra.Handle(&recorder, request)
	response := recorder.Result()

	if response.StatusCode != http.StatusOK {
		t.Errorf("want %d, got %d", http.StatusOK, response.StatusCode)
	}
}

func TestGetValidCapacityReservations(t *testing.T) {
	now := time.Now()

	hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
		Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
			CapacityReservations: []actionsv1alpha1.CapacityReservation{
				{
					ExpirationTime: metav1.Time{Time: now.Add(-time.Second)},
					Replicas:       1,
				},
				{
					ExpirationTime: metav1.Time{Time: now},
					Replicas:       2,
				},
				{
					ExpirationTime: metav1.Time{Time: now.Add(time.Second)},
					Replicas:       3,
				},
			},
		},
	}

	revs := getValidCapacityReservations(hra)

	var count int

	for _, r := range revs {
		count += r.Replicas
	}

	want := 3

	if count != want {
		t.Errorf("want %d, got %d", want, count)
	}
}

func installTestLogger(webhook *HorizontalRunnerAutoscalerGitHubWebhook) *bytes.Buffer {
	logs := &bytes.Buffer{}

	log := testLogger{
		name:   "testlog",
		writer: logs,
	}

	webhook.Log = &log

	return logs
}

func testServer(t *testing.T, eventType string, event interface{}, wantCode int, wantBody string) {
	t.Helper()

	hraWebhook := &HorizontalRunnerAutoscalerGitHubWebhook{}

	var initObjs []runtime.Object

	client := fake.NewFakeClientWithScheme(sc, initObjs...)

	logs := installTestLogger(hraWebhook)

	defer func() {
		if t.Failed() {
			t.Logf("diagnostics: %s", logs.String())
		}
	}()

	hraWebhook.Client = client

	mux := http.NewServeMux()
	mux.HandleFunc("/", hraWebhook.Handle)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := sendWebhook(server, eventType, event)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if resp != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode != wantCode {
		t.Error("status:", resp.StatusCode)
	}

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if string(respBody) != wantBody {
		t.Fatal("body:", string(respBody))
	}
}

func sendWebhook(server *httptest.Server, eventType string, event interface{}) (*http.Response, error) {
	jsonBuf := &bytes.Buffer{}
	enc := json.NewEncoder(jsonBuf)
	enc.SetIndent("  ", "")
	err := enc.Encode(event)
	if err != nil {
		return nil, fmt.Errorf("[bug in test] encoding event to json: %+v", err)
	}

	reqBody := jsonBuf.Bytes()

	u, err := url.Parse(server.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing server url: %v", err)
	}

	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: map[string][]string{
			"X-GitHub-Event": {eventType},
			"Content-Type":   {"application/json"},
		},
		Body: ioutil.NopCloser(bytes.NewBuffer(reqBody)),
	}

	return http.DefaultClient.Do(req)
}

// testLogger is a sample logr.Logger that logs in-memory.
// It's only for testing log outputs.
type testLogger struct {
	name      string
	keyValues map[string]interface{}

	writer io.Writer
}

var _ logr.Logger = &testLogger{}

func (l *testLogger) Info(msg string, kvs ...interface{}) {
	fmt.Fprintf(l.writer, "%s] %s\t", l.name, msg)
	for k, v := range l.keyValues {
		fmt.Fprintf(l.writer, "%s=%+v ", k, v)
	}
	for i := 0; i < len(kvs); i += 2 {
		fmt.Fprintf(l.writer, "%s=%+v ", kvs[i], kvs[i+1])
	}
	fmt.Fprintf(l.writer, "\n")
}

func (_ *testLogger) Enabled() bool {
	return true
}

func (l *testLogger) Error(err error, msg string, kvs ...interface{}) {
	kvs = append(kvs, "error", err)
	l.Info(msg, kvs...)
}

func (l *testLogger) V(_ int) logr.InfoLogger {
	return l
}

func (l *testLogger) WithName(name string) logr.Logger {
	return &testLogger{
		name:      l.name + "." + name,
		keyValues: l.keyValues,
		writer:    l.writer,
	}
}

func (l *testLogger) WithValues(kvs ...interface{}) logr.Logger {
	newMap := make(map[string]interface{}, len(l.keyValues)+len(kvs)/2)
	for k, v := range l.keyValues {
		newMap[k] = v
	}
	for i := 0; i < len(kvs); i += 2 {
		newMap[kvs[i].(string)] = kvs[i+1]
	}
	return &testLogger{
		name:      l.name,
		keyValues: newMap,
		writer:    l.writer,
	}
}
