package metrics

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

var _ listener.MetricsRecorder = &OTelRecorder{}

// OTelRecorder implements listener.MetricsRecorder by emitting
// OpenTelemetry trace spans for each completed job. Three child spans
// are created under the job's parent span using deterministic IDs:
//
//   - runner.queue:     QueueTime → ScaleSetAssignTime
//   - runner.startup:   ScaleSetAssignTime → RunnerAssignTime
//   - runner.execution: RunnerAssignTime → FinishTime
//
// The deterministic ID scheme (MD5 of runID-attempt for trace ID,
// big-endian int64 for span ID) is compatible with otel-explorer's
// GitHub Actions trace view, allowing ARC spans to merge into existing
// workflow traces with zero correlation configuration.
type OTelRecorder struct {
	mu       sync.Mutex
	exporter sdktrace.SpanExporter
	logger   *slog.Logger

	// runAttempt defaults to 1. ARC messages don't include
	// run_attempt; override via SetRunAttempt if determinable.
	runAttempt int64
}

// NewOTelRecorder creates a recorder that exports job lifecycle spans
// via the given SpanExporter.
func NewOTelRecorder(exporter sdktrace.SpanExporter, logger *slog.Logger) *OTelRecorder {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &OTelRecorder{
		exporter:   exporter,
		logger:     logger,
		runAttempt: 1,
	}
}

// SetRunAttempt overrides the default run attempt (1) used for trace
// ID generation.
func (r *OTelRecorder) SetRunAttempt(attempt int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runAttempt = attempt
}

// Shutdown flushes pending spans and releases exporter resources.
func (r *OTelRecorder) Shutdown(ctx context.Context) error {
	return r.exporter.Shutdown(ctx)
}

func (r *OTelRecorder) RecordJobStarted(_ *scaleset.JobStarted) {}

func (r *OTelRecorder) RecordJobCompleted(msg *scaleset.JobCompleted) {
	r.mu.Lock()
	attempt := r.runAttempt
	r.mu.Unlock()

	spans := buildJobSpans(msg, attempt)
	if len(spans) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.exporter.ExportSpans(ctx, spans); err != nil {
		r.logger.Warn("failed to export OTel spans", "error", err)
	}
}

func (r *OTelRecorder) RecordStatistics(_ *scaleset.RunnerScaleSetStatistic) {}
func (r *OTelRecorder) RecordDesiredRunners(_ int)                           {}

func buildJobSpans(msg *scaleset.JobCompleted, attempt int64) []sdktrace.ReadOnlySpan {
	traceID := newTraceID(msg.WorkflowRunID, attempt)
	parentSpanID := toSpanID(msg.JobID)
	parentSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     parentSpanID,
		TraceFlags: trace.FlagsSampled,
	})

	conclusion := normalizeConclusion(msg.Result)
	repo := msg.OwnerName + "/" + msg.RepositoryName

	commonAttrs := []attribute.KeyValue{
		// GitHub Actions attributes
		attribute.Int64("github.run_id", msg.WorkflowRunID),
		attribute.String("github.job_id", msg.JobID),
		attribute.String("github.job_name", msg.JobDisplayName),
		attribute.String("github.repository", repo),
		attribute.String("github.runner_name", msg.RunnerName),
		attribute.Int("github.runner_id", msg.RunnerID),
		attribute.String("github.workflow_ref", msg.JobWorkflowRef),
		attribute.String("github.event_name", msg.EventName),
		// OTel CI/CD semantic conventions (cicd.*)
		attribute.String("cicd.pipeline.run.id", strconv.FormatInt(msg.WorkflowRunID, 10)),
		attribute.String("cicd.pipeline.task.name", msg.JobDisplayName),
		attribute.String("cicd.pipeline.task.run.id", msg.JobID),
		attribute.String("cicd.worker.name", msg.RunnerName),
		attribute.String("cicd.worker.id", strconv.Itoa(msg.RunnerID)),
		attribute.String("vcs.repository.url.full", "https://github.com/"+repo),
	}

	var stubs tracetest.SpanStubs

	if !msg.QueueTime.IsZero() && !msg.ScaleSetAssignTime.IsZero() {
		stubs = append(stubs, newStub(
			traceID, parentSC, "runner.queue",
			msg.QueueTime, msg.ScaleSetAssignTime,
			append(sliceClone(commonAttrs), attribute.String("type", "runner.queue")),
		))
	}

	if !msg.ScaleSetAssignTime.IsZero() && !msg.RunnerAssignTime.IsZero() {
		stubs = append(stubs, newStub(
			traceID, parentSC, "runner.startup",
			msg.ScaleSetAssignTime, msg.RunnerAssignTime,
			append(sliceClone(commonAttrs), attribute.String("type", "runner.startup")),
		))
	}

	if !msg.RunnerAssignTime.IsZero() && !msg.FinishTime.IsZero() {
		stubs = append(stubs, newStub(
			traceID, parentSC, "runner.execution",
			msg.RunnerAssignTime, msg.FinishTime,
			append(sliceClone(commonAttrs),
				attribute.String("type", "runner.execution"),
				attribute.String("github.conclusion", conclusion),
				attribute.String("cicd.pipeline.task.run.result", conclusionToSemconv(conclusion)),
			),
		))
	}

	return stubs.Snapshots()
}

func normalizeConclusion(raw string) string {
	switch strings.ToLower(raw) {
	case "success", "succeeded":
		return "success"
	case "failure", "failed":
		return "failure"
	case "cancelled", "canceled":
		return "cancelled"
	case "skipped":
		return "skipped"
	case "timed_out":
		return "timed_out"
	case "startup_failure":
		return "startup_failure"
	default:
		return strings.ToLower(raw)
	}
}

func conclusionToSemconv(conclusion string) string {
	switch conclusion {
	case "success":
		return "success"
	case "failure":
		return "failure"
	case "cancelled":
		return "cancellation"
	case "skipped":
		return "skip"
	case "timed_out":
		return "timeout"
	case "startup_failure":
		return "error"
	default:
		return conclusion
	}
}

func newStub(
	traceID trace.TraceID,
	parentSC trace.SpanContext,
	name string,
	start, end time.Time,
	attrs []attribute.KeyValue,
) tracetest.SpanStub {
	spanID := newSpanIDFromString(fmt.Sprintf("%s-%s", name, parentSC.SpanID()))
	return tracetest.SpanStub{
		Name: name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		}),
		Parent:     parentSC,
		StartTime:  start,
		EndTime:    end,
		Attributes: attrs,
	}
}

// Deterministic ID generation — compatible with otel-explorer's
// pkg/githubapi/ids.go scheme.

func newTraceID(runID, runAttempt int64) trace.TraceID {
	if runAttempt == 0 {
		runAttempt = 1
	}
	return trace.TraceID(md5.Sum([]byte(fmt.Sprintf("%d-%d", runID, runAttempt))))
}

func newSpanID(id int64) trace.SpanID {
	var sid trace.SpanID
	binary.BigEndian.PutUint64(sid[:], uint64(id))
	return sid
}

func newSpanIDFromString(s string) trace.SpanID {
	sum := md5.Sum([]byte(s))
	var sid trace.SpanID
	copy(sid[:], sum[:8])
	return sid
}

func toSpanID(jobID string) trace.SpanID {
	if id, err := strconv.ParseInt(jobID, 10, 64); err == nil {
		return newSpanID(id)
	}
	return newSpanIDFromString(jobID)
}

func sliceClone(s []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(s))
	copy(out, s)
	return out
}
