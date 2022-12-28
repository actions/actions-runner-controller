package actionsmetrics

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v47/github"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/actions/actions-runner-controller/github"
)

type EventReader struct {
	Log logr.Logger

	// GitHub Client to fetch information about job failures
	GitHubClient *github.Client

	// Event queue
	Events chan interface{}
}

// HandleWorkflowJobEvent send event to reader channel for processing
//
// forcing the events through a channel ensures they are processed in sequentially,
// and prevents any race conditions with githubWorkflowJobStatus
func (reader *EventReader) HandleWorkflowJobEvent(event interface{}) {
	reader.Events <- event
}

// ProcessWorkflowJobEvents pop events in a loop for processing
//
// Should be called asynchronously with `go`
func (reader *EventReader) ProcessWorkflowJobEvents(ctx context.Context) {
	for {
		select {
		case event := <-reader.Events:
			reader.ProcessWorkflowJobEvent(ctx, event)
		case <-ctx.Done():
			return
		}
	}
}

// ProcessWorkflowJobEvent processes a single event
//
// Events should be processed in the same order that Github emits them
func (reader *EventReader) ProcessWorkflowJobEvent(ctx context.Context, event interface{}) {

	e, ok := event.(*gogithub.WorkflowJobEvent)
	if !ok {
		return
	}

	// collect labels
	labels := make(prometheus.Labels)

	runsOn := strings.Join(e.WorkflowJob.Labels, `,`)
	labels["runs_on"] = runsOn
	labels["job_name"] = *e.WorkflowJob.Name

	// switch on job status
	switch action := e.GetAction(); action {
	case "queued":
		githubWorkflowJobsQueuedTotal.With(labels).Inc()

	case "in_progress":
		githubWorkflowJobsStartedTotal.With(labels).Inc()

		if reader.GitHubClient == nil {
			return
		}

		parseResult, err := reader.fetchAndParseWorkflowJobLogs(ctx, e)
		if err != nil {
			reader.Log.Error(err, "reading workflow job log")
			return
		} else {
			reader.Log.Info("reading workflow_job logs",
				"job_name", *e.WorkflowJob.Name,
				"job_id", fmt.Sprint(*e.WorkflowJob.ID),
			)
		}

		githubWorkflowJobQueueDurationSeconds.With(labels).Observe(parseResult.QueueTime.Seconds())

	case "completed":
		githubWorkflowJobsCompletedTotal.With(labels).Inc()

		// job_conclusion -> (neutral, success, skipped, cancelled, timed_out, action_required, failure)
		githubWorkflowJobConclusionsTotal.With(extraLabel("job_conclusion", *e.WorkflowJob.Conclusion, labels)).Inc()

		parseResult, err := reader.fetchAndParseWorkflowJobLogs(ctx, e)
		if err != nil {
			reader.Log.Error(err, "reading workflow job log")
			return
		} else {
			reader.Log.Info("reading workflow_job logs",
				"job_name", *e.WorkflowJob.Name,
				"job_id", fmt.Sprint(*e.WorkflowJob.ID),
			)
		}

		if *e.WorkflowJob.Conclusion == "failure" {
			failedStep := "null"
			for i, step := range e.WorkflowJob.Steps {

				// *step.Conclusion ~
				// "success",
				// "failure",
				// "neutral",
				// "cancelled",
				// "skipped",
				// "timed_out",
				// "action_required",
				// null
				if *step.Conclusion == "failure" {
					failedStep = fmt.Sprint(i)
					break
				}
				if *step.Conclusion == "timed_out" {
					failedStep = fmt.Sprint(i)
					parseResult.ExitCode = "timed_out"
					break
				}
			}
			githubWorkflowJobFailuresTotal.With(
				extraLabel("failed_step", failedStep,
					extraLabel("exit_code", parseResult.ExitCode, labels),
				),
			).Inc()
		}

		githubWorkflowJobRunDurationSeconds.With(extraLabel("job_conclusion", *e.WorkflowJob.Conclusion, labels)).Observe(parseResult.RunTime.Seconds())
	}
}

func extraLabel(key string, value string, labels prometheus.Labels) prometheus.Labels {
	fixedLabels := make(prometheus.Labels)
	for k, v := range labels {
		fixedLabels[k] = v
	}
	fixedLabels[key] = value
	return fixedLabels
}

type ParseResult struct {
	ExitCode  string
	QueueTime time.Duration
	RunTime   time.Duration
}

var logLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}.\d{7}Z)\s(.+)$`)
var exitCodeLine = regexp.MustCompile(`##\[error\]Process completed with exit code (\d)\.`)

func (reader *EventReader) fetchAndParseWorkflowJobLogs(ctx context.Context, e *gogithub.WorkflowJobEvent) (*ParseResult, error) {

	owner := *e.Repo.Owner.Login
	repo := *e.Repo.Name
	id := *e.WorkflowJob.ID
	url, _, err := reader.GitHubClient.Actions.GetWorkflowJobLogs(ctx, owner, repo, id, true)
	if err != nil {
		return nil, err
	}
	jobLogs, err := http.DefaultClient.Get(url.String())
	if err != nil {
		return nil, err
	}

	exitCode := "null"

	var (
		queuedTime    time.Time
		startedTime   time.Time
		completedTime time.Time
	)

	func() {
		// Read jobLogs.Body line by line

		defer jobLogs.Body.Close()
		lines := bufio.NewScanner(jobLogs.Body)

		for lines.Scan() {
			matches := logLine.FindStringSubmatch(lines.Text())
			if matches == nil {
				continue
			}
			timestamp := matches[1]
			line := matches[2]

			if strings.HasPrefix(line, "##[error]") {
				// Get exit code
				exitCodeMatch := exitCodeLine.FindStringSubmatch(line)
				if exitCodeMatch != nil {
					exitCode = exitCodeMatch[1]
				}
				continue
			}

			if strings.HasPrefix(line, "Waiting for a runner to pick up this job...") {
				queuedTime, _ = time.Parse(time.RFC3339, timestamp)
				continue
			}

			if strings.HasPrefix(line, "Job is about to start running on the runner:") {
				startedTime, _ = time.Parse(time.RFC3339, timestamp)
				continue
			}

			// Last line in the log will count as the completed time
			completedTime, _ = time.Parse(time.RFC3339, timestamp)
		}
	}()

	return &ParseResult{
		ExitCode:  exitCode,
		QueueTime: startedTime.Sub(queuedTime),
		RunTime:   completedTime.Sub(startedTime),
	}, nil
}
