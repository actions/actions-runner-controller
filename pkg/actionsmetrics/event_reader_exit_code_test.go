package actionsmetrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v52/github"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/actions/actions-runner-controller/github"
)

func ptr[T any](v T) *T { return &v }

// exitCodeLabelForJob collects github_workflow_job_failures_total and returns
// the exit_code label recorded for the job with the given display name.
func exitCodeLabelForJob(t *testing.T, jobName string) (string, bool) {
	t.Helper()

	ch := make(chan prometheus.Metric, 1024)
	go func() {
		githubWorkflowJobFailuresTotal.Collect(ch)
		close(ch)
	}()

	for m := range ch {
		var written dto.Metric
		require.NoError(t, m.Write(&written))

		var gotJobName, gotExitCode string
		for _, label := range written.Label {
			switch label.GetName() {
			case "job_name":
				gotJobName = label.GetValue()
			case "exit_code":
				gotExitCode = label.GetValue()
			}
		}

		if gotJobName == jobName {
			return gotExitCode, true
		}
	}

	return "", false
}

// TestCompletedJobReportsMultiDigitExitCode drives the completed workflow_job
// event through the same entry point the webhook server calls and asserts the
// exit_code label comes from the "Process completed with exit code N." line in
// the job log.
//
// Multi-digit exit codes are the interesting case: 137 (SIGKILL, typically an
// OOM kill) and 143 (SIGTERM) are what a cancelled or memory-starved job
// actually exits with, and 127 is a shell that could not find the command.
func TestCompletedJobReportsMultiDigitExitCode(t *testing.T) {
	tests := []struct {
		name      string
		exitCode  string
		wantLabel string
	}{
		{name: "single digit exit code", exitCode: "1", wantLabel: "1"},
		{name: "OOM kill", exitCode: "137", wantLabel: "137"},
		{name: "SIGTERM", exitCode: "143", wantLabel: "143"},
		{name: "command not found", exitCode: "127", wantLabel: "127"},
		{name: "general failure", exitCode: "255", wantLabel: "255"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const (
				owner = "example"
				repo  = "example"
				jobID = int64(42)
			)

			jobLog := fmt.Sprintf(`
2024-01-01T00:00:00.0000000Z Waiting for a runner to pick up this job...
2024-01-01T00:00:10.0000000Z Job is about to start running on the runner: example-runner
2024-01-01T00:00:20.0000000Z ##[error]Process completed with exit code %s.
`, tt.exitCode)

			mux := http.NewServeMux()

			var server *httptest.Server

			mux.HandleFunc(
				fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", owner, repo, jobID),
				func(w http.ResponseWriter, r *http.Request) {
					// GetWorkflowJobLogs expects a 302 whose Location header
					// holds the log archive URL.
					w.Header().Set("Location", server.URL+"/download"+r.URL.Path)
					w.WriteHeader(http.StatusFound)
				},
			)
			mux.HandleFunc(
				fmt.Sprintf("/download/repos/%s/%s/actions/jobs/%d/logs", owner, repo, jobID),
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, jobLog)
				},
			)

			server = httptest.NewServer(mux)
			defer server.Close()

			baseURL, err := url.Parse(server.URL + "/")
			require.NoError(t, err)

			ghClient := gogithub.NewClient(nil)
			ghClient.BaseURL = baseURL

			reader := &EventReader{
				Log:          logr.Discard(),
				GitHubClient: &github.Client{Client: ghClient},
				Events:       make(chan interface{}, 1),
			}

			// A distinct job name per subtest keeps the label lookup unambiguous
			// and keeps the shared counter from bleeding between cases.
			jobName := "exit-code-" + tt.exitCode

			reader.ProcessWorkflowJobEvent(context.Background(), &gogithub.WorkflowJobEvent{
				Action: ptr("completed"),
				WorkflowJob: &gogithub.WorkflowJob{
					ID:         ptr(jobID),
					Name:       ptr(jobName),
					Conclusion: ptr("failure"),
					Labels:     []string{"self-hosted"},
					Steps: []*gogithub.TaskStep{
						{Conclusion: ptr("failure")},
					},
				},
				Repo: &gogithub.Repository{
					Name:     ptr(repo),
					FullName: ptr(owner + "/" + repo),
					Owner:    &gogithub.User{Login: ptr(owner)},
				},
			})

			got, ok := exitCodeLabelForJob(t, jobName)
			require.True(t, ok, "no failure metric recorded for job %q", jobName)

			assert.Equal(t, tt.wantLabel, got,
				"exit_code label for a job that exited %s", tt.exitCode)
		})
	}
}
