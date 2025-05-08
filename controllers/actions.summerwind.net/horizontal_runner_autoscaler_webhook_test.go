package actionssummerwindnet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/google/go-github/v52/github"
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

func TestWebhookWorkflowJob(t *testing.T) {
	setupTest := func() github.WorkflowJobEvent {
		f, err := os.Open("testdata/org_webhook_workflow_job_payload.json")
		if err != nil {
			t.Fatalf("could not open the fixture: %s", err)
		}
		defer f.Close()
		var e github.WorkflowJobEvent
		if err := json.NewDecoder(f).Decode(&e); err != nil {
			t.Fatalf("invalid json: %s", err)
		}

		return e
	}
	t.Run("Successful", func(t *testing.T) {
		e := setupTest()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"label1"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"scaled test-name by 1",
			initObjs,
		)
	})
	t.Run("WrongLabels", func(t *testing.T) {
		e := setupTest()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"bad-label"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"no horizontalrunnerautoscaler to scale for this github event",
			initObjs,
		)
	})
	// This test verifies that the old way of matching labels doesn't work anymore
	t.Run("OldLabels", func(t *testing.T) {
		e := setupTest()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"label1": "label1",
						},
					},
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"bad-label"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"no horizontalrunnerautoscaler to scale for this github event",
			initObjs,
		)
	})
}

func TestWebhookWorkflowJobWithSelfHostedLabel(t *testing.T) {
	setupTest := func() github.WorkflowJobEvent {
		f, err := os.Open("testdata/org_webhook_workflow_job_with_self_hosted_label_payload.json")
		if err != nil {
			t.Fatalf("could not open the fixture: %s", err)
		}
		defer f.Close()
		var e github.WorkflowJobEvent
		if err := json.NewDecoder(f).Decode(&e); err != nil {
			t.Fatalf("invalid json: %s", err)
		}

		return e
	}
	t.Run("Successful", func(t *testing.T) {
		e := setupTest()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"label1"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"scaled test-name by 1",
			initObjs,
		)
	})
	t.Run("WrongLabels", func(t *testing.T) {
		e := setupTest()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"bad-label"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"no horizontalrunnerautoscaler to scale for this github event",
			initObjs,
		)
	})
	// This test verifies that the old way of matching labels doesn't work anymore
	t.Run("OldLabels", func(t *testing.T) {
		e := setupTest()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"label1": "label1",
						},
					},
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"bad-label"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"no horizontalrunnerautoscaler to scale for this github event",
			initObjs,
		)
	})
}

func TestWebhookWorkflowJobWithCompletedCheckRun(t *testing.T) {
	setupTestSucceeded := func() github.WorkflowJobEvent {
		f, err := os.Open("testdata/webhook_workflow_job_with_completed_success_check_run.json")
		if err != nil {
			t.Fatalf("could not open the fixture: %s", err)
		}
		defer f.Close()
		var e github.WorkflowJobEvent
		if err := json.NewDecoder(f).Decode(&e); err != nil {
			t.Fatalf("invalid json: %s", err)
		}

		return e
	}
	setupTestFailure := func() github.WorkflowJobEvent {
		f, err := os.Open("testdata/webhook_workflow_job_with_completed_failure_check_run.json")
		if err != nil {
			t.Fatalf("could not open the fixture: %s", err)
		}
		defer f.Close()
		var e github.WorkflowJobEvent
		if err := json.NewDecoder(f).Decode(&e); err != nil {
			t.Fatalf("invalid json: %s", err)
		}

		return e
	}
	t.Run("Success", func(t *testing.T) {
		e := setupTestSucceeded()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"label1"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"",
			initObjs,
		)
	})
	t.Run("Failure", func(t *testing.T) {
		e := setupTestFailure()
		hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
				ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
					Name: "test-name",
				},
				ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
					{
						GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
							WorkflowJob: &actionsv1alpha1.WorkflowJobSpec{},
						},
					},
				},
			},
		}

		rd := &actionsv1alpha1.RunnerDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-name",
			},
			Spec: actionsv1alpha1.RunnerDeploymentSpec{
				Template: actionsv1alpha1.RunnerTemplate{
					Spec: actionsv1alpha1.RunnerSpec{
						RunnerConfig: actionsv1alpha1.RunnerConfig{
							Organization: "MYORG",
							Labels:       []string{"label1"},
						},
					},
				},
			},
		}

		initObjs := []runtime.Object{hra, rd}

		testServerWithInitObjs(t,
			"workflow_job",
			&e,
			200,
			"",
			initObjs,
		)
	})
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
	duration, _ := time.ParseDuration("10m")
	effectiveTime := now.Add(-duration)

	hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
		Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
			CapacityReservations: []actionsv1alpha1.CapacityReservation{
				{
					EffectiveTime:  metav1.Time{Time: effectiveTime.Add(-time.Second)},
					ExpirationTime: metav1.Time{Time: now.Add(-time.Second)},
					Replicas:       1,
				},
				{
					EffectiveTime:  metav1.Time{Time: effectiveTime},
					ExpirationTime: metav1.Time{Time: now},
					Replicas:       2,
				},
				{
					EffectiveTime:  metav1.Time{Time: effectiveTime.Add(time.Second)},
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

	sink := &testLogSink{
		name:   "testlog",
		writer: logs,
	}

	log := logr.New(sink)

	webhook.Log = log

	return logs
}

func testServerWithInitObjs(t *testing.T, eventType string, event interface{}, wantCode int, wantBody string, initObjs []runtime.Object) {
	t.Helper()

	hraWebhook := &HorizontalRunnerAutoscalerGitHubWebhook{}

	client := fake.NewClientBuilder().
		WithScheme(sc).
		WithRuntimeObjects(initObjs...).
		WithIndex(&actionsv1alpha1.HorizontalRunnerAutoscaler{}, scaleTargetKey, hraWebhook.indexer).
		Build()

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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if string(respBody) != wantBody {
		t.Fatal("body:", string(respBody))
	}
}

func testServer(t *testing.T, eventType string, event interface{}, wantCode int, wantBody string) {
	var initObjs []runtime.Object
	testServerWithInitObjs(t, eventType, event, wantCode, wantBody, initObjs)
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
		Body: io.NopCloser(bytes.NewBuffer(reqBody)),
	}

	return http.DefaultClient.Do(req)
}

// testLogSink is a sample logr.Logger that logs in-memory.
// It's only for testing log outputs.
type testLogSink struct {
	name      string
	keyValues map[string]interface{}

	writer io.Writer
}

var _ logr.LogSink = &testLogSink{}

func (l *testLogSink) Init(_ logr.RuntimeInfo) {

}

func (l *testLogSink) Info(_ int, msg string, kvs ...interface{}) {
	fmt.Fprintf(l.writer, "%s] %s\t", l.name, msg)
	for k, v := range l.keyValues {
		fmt.Fprintf(l.writer, "%s=%+v ", k, v)
	}
	for i := 0; i < len(kvs); i += 2 {
		fmt.Fprintf(l.writer, "%s=%+v ", kvs[i], kvs[i+1])
	}
	fmt.Fprintf(l.writer, "\n")
}

func (*testLogSink) Enabled(level int) bool {
	return true
}

func (l *testLogSink) Error(err error, msg string, kvs ...interface{}) {
	kvs = append(kvs, "error", err)
	l.Info(0, msg, kvs...)
}

func (l *testLogSink) WithName(name string) logr.LogSink {
	return &testLogSink{
		name:      l.name + "." + name,
		keyValues: l.keyValues,
		writer:    l.writer,
	}
}

func (l *testLogSink) WithValues(kvs ...interface{}) logr.LogSink {
	newMap := make(map[string]interface{}, len(l.keyValues)+len(kvs)/2)
	for k, v := range l.keyValues {
		newMap[k] = v
	}
	for i := 0; i < len(kvs); i += 2 {
		newMap[kvs[i].(string)] = kvs[i+1]
	}
	return &testLogSink{
		name:      l.name,
		keyValues: newMap,
		writer:    l.writer,
	}
}
