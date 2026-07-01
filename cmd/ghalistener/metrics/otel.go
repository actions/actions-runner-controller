package metrics

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const otelScopeName = "github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"

var _ listener.MetricsRecorder = &OTelRecorder{}

// OTelRecorder implements listener.MetricsRecorder by emitting
// OpenTelemetry trace spans for each completed job. Three child spans
// are created under the job's parent span using deterministic IDs:
//
//   - runner.queue:     QueueTime → ScaleSetAssignTime
//   - runner.startup:   ScaleSetAssignTime → RunnerAssignTime
//   - runner.execution: RunnerAssignTime → FinishTime
//
// The deterministic ID scheme follows the normative contract in
// actions/runner docs/otel-id-contract.md (SHA-256, truncated):
//
//	trace ID = sha256("{run_id}-{run_attempt}")[:16]
//	job span = sha256("job-{run_id}-{run_attempt}-{job_name}")[:8]
//
// where job_name is the job's display name. The runner's native OTLP
// exporter emits the job span with these exact IDs, so ARC spans merge
// under it with zero correlation configuration.
// defaultRunAttempt is the run attempt hashed into trace/span IDs. The
// scale-set message protocol (github.com/actions/scaleset
// JobMessageBase) does not carry github.run_attempt, so the listener
// cannot know which attempt a completed job belongs to and assumes the
// first.
//
// KNOWN LIMITATION: for workflow re-runs (attempt >= 2) these spans
// join the FIRST attempt's trace — the runner hashes the real attempt
// into its trace ID, so the re-run's own trace is missing queue and
// startup data. Fixing this requires the scale-set message to carry
// the run attempt (or a REST lookup per job).
const defaultRunAttempt = 1

type OTelRecorder struct {
	mu        sync.Mutex
	exporter  sdktrace.SpanExporter
	logger    *slog.Logger
	serverURL string
	res       *resource.Resource
	scope     instrumentation.Scope
}

// OTelRecorderConfig configures an OTelRecorder.
type OTelRecorderConfig struct {
	// Exporter receives the job lifecycle spans. Required.
	Exporter sdktrace.SpanExporter
	Logger   *slog.Logger
	// ServerURL is the GitHub server base URL used for vcs.*/cicd.*
	// URL attributes (GHES-safe). Defaults to https://github.com.
	ServerURL string
	// ScaleSetName and ScaleSetNamespace identify the scale set this
	// listener serves; they are attached to the OTel resource.
	ScaleSetName      string
	ScaleSetNamespace string
}

// NewOTelRecorder creates a recorder that exports job lifecycle spans
// via the given SpanExporter.
func NewOTelRecorder(cfg OTelRecorderConfig) *OTelRecorder {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	serverURL := strings.TrimRight(cfg.ServerURL, "/")
	if serverURL == "" {
		serverURL = "https://github.com"
	}

	resAttrs := []attribute.KeyValue{
		attribute.String("service.name", "gha-listener"),
		attribute.String("service.version", build.Version),
		// The listener is the scheduling side of the CI/CD system
		// (semconv cicd.system.component); the runner reports "agent".
		attribute.String("cicd.system.component", "controller"),
	}
	if cfg.ScaleSetNamespace != "" {
		resAttrs = append(resAttrs, attribute.String("service.namespace", cfg.ScaleSetNamespace))
	}
	if cfg.ScaleSetName != "" {
		resAttrs = append(resAttrs, attribute.String("github.scale_set.name", cfg.ScaleSetName))
	}

	// WithFromEnv comes last so the standard OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES env vars override the defaults above.
	res, err := resource.New(context.Background(),
		resource.WithAttributes(resAttrs...),
		resource.WithFromEnv(),
	)
	if err != nil {
		logger.Warn("failed to build OTel resource", "error", err)
	}
	if res == nil {
		res = resource.Empty()
	}

	return &OTelRecorder{
		exporter:  cfg.Exporter,
		logger:    logger,
		serverURL: serverURL,
		res:       res,
		scope: instrumentation.Scope{
			Name:    otelScopeName,
			Version: build.Version,
		},
	}
}

// Shutdown flushes pending spans and releases exporter resources.
func (r *OTelRecorder) Shutdown(ctx context.Context) error {
	return r.exporter.Shutdown(ctx)
}

func (r *OTelRecorder) RecordJobStarted(_ *scaleset.JobStarted) {}

func (r *OTelRecorder) RecordJobCompleted(msg *scaleset.JobCompleted) {
	spans := r.buildJobSpans(msg, defaultRunAttempt)
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

func (r *OTelRecorder) buildJobSpans(msg *scaleset.JobCompleted, attempt int64) []sdktrace.ReadOnlySpan {
	traceID := newTraceID(msg.WorkflowRunID, attempt)
	// Parent to the contract job span the runner emits. JobDisplayName
	// is the job's display name (GitHub API job.name), the job_name the
	// contract mandates — not the github.job YAML key.
	parentSpanID := newJobSpanID(msg.WorkflowRunID, attempt, msg.JobDisplayName)
	parentSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     parentSpanID,
		TraceFlags: trace.FlagsSampled,
	})

	conclusion := normalizeConclusion(msg.Result)
	repo := msg.OwnerName + "/" + msg.RepositoryName
	runID := strconv.FormatInt(msg.WorkflowRunID, 10)
	runAttempt := strconv.FormatInt(attempt, 10)
	runURL := fmt.Sprintf("%s/%s/actions/runs/%s/attempts/%s", r.serverURL, repo, runID, runAttempt)

	// Attribute names, types, and values must match the runner's
	// native OTLP exporter (Runner.Worker/OTelTraceExporter.cs) so a
	// single query matches both span sets in the merged trace.
	commonAttrs := []attribute.KeyValue{
		// GitHub Actions attributes (no semconv equivalent)
		attribute.String("github.run_id", runID),
		attribute.String("github.run_attempt", runAttempt),
		attribute.String("github.repository", repo),
		attribute.String("github.workflow_ref", msg.JobWorkflowRef),
		attribute.String("github.event_name", msg.EventName),
		// OTel CI/CD semantic conventions (cicd.*, vcs.*)
		attribute.String("cicd.pipeline.run.id", runID),
		attribute.String("cicd.pipeline.run.url.full", runURL),
		attribute.String("cicd.pipeline.task.name", msg.JobDisplayName),
		attribute.String("cicd.pipeline.task.run.id", parentSpanID.String()),
		attribute.String("cicd.worker.name", msg.RunnerName),
		attribute.String("cicd.worker.id", strconv.Itoa(msg.RunnerID)),
		attribute.String("vcs.repository.url.full", r.serverURL+"/"+repo),
		attribute.String("vcs.provider.name", "github"),
	}

	var stubs tracetest.SpanStubs

	if !msg.QueueTime.IsZero() && !msg.ScaleSetAssignTime.IsZero() {
		stubs = append(stubs, r.newStub(
			traceID, parentSC, "runner.queue",
			msg.QueueTime, msg.ScaleSetAssignTime,
			append(sliceClone(commonAttrs), attribute.String("type", "runner.queue")),
		))
	}

	if !msg.ScaleSetAssignTime.IsZero() && !msg.RunnerAssignTime.IsZero() {
		stubs = append(stubs, r.newStub(
			traceID, parentSC, "runner.startup",
			msg.ScaleSetAssignTime, msg.RunnerAssignTime,
			append(sliceClone(commonAttrs), attribute.String("type", "runner.startup")),
		))
	}

	if !msg.RunnerAssignTime.IsZero() && !msg.FinishTime.IsZero() {
		stubs = append(stubs, r.newStub(
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

func (r *OTelRecorder) newStub(
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
		Parent:               parentSC,
		StartTime:            start,
		EndTime:              end,
		Attributes:           attrs,
		Resource:             r.res,
		InstrumentationScope: r.scope,
	}
}

// Deterministic ID generation per the normative contract in
// actions/runner docs/otel-id-contract.md: leading bytes of SHA-256
// (never MD5 — MD5 fails on FIPS-enabled hosts). See
// TestIDContractGoldenVectors for the shared conformance vectors.

func newTraceID(runID, runAttempt int64) trace.TraceID {
	if runAttempt == 0 {
		runAttempt = 1
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", runID, runAttempt)))
	var tid trace.TraceID
	copy(tid[:], sum[:16])
	return tid
}

func newSpanIDFromString(s string) trace.SpanID {
	sum := sha256.Sum256([]byte(s))
	var sid trace.SpanID
	copy(sid[:], sum[:8])
	return sid
}

func newJobSpanID(runID, runAttempt int64, jobName string) trace.SpanID {
	if runAttempt == 0 {
		runAttempt = 1
	}
	return newSpanIDFromString(fmt.Sprintf("job-%d-%d-%s", runID, runAttempt, jobName))
}

func sliceClone(s []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(s))
	copy(out, s)
	return out
}
