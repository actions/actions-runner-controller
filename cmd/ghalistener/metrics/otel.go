package metrics

import (
	"context"
	"crypto/sha256"
	"errors"
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

// spanQueueSize bounds the export queue. Export runs on a background
// goroutine so the listener's autoscaling message loop never waits on
// the telemetry backend; when the queue is full (collector down or
// slow), new job spans are dropped with a warning.
const spanQueueSize = 64

// exportTimeout bounds a single OTLP export attempt.
const exportTimeout = 5 * time.Second

// maxPendingQueueEntries caps the in-memory map that holds
// queue-phase timestamps from JobStarted messages. Sized well above
// the realistic concurrent-job count on any single scale set; if the
// cap is reached a warning is logged and the new entry is dropped
// (the runner.queue span will be missing for that job).
const maxPendingQueueEntries = 1000

// pendingQueueTTL is the maximum age of an unmatched queue-time entry.
// Entries older than this are silently discarded at lookup so that
// jobs which never send a JobCompleted (e.g. abandoned mid-run) cannot
// leak memory across a months-long listener lifetime.
const pendingQueueTTL = 24 * time.Hour

// pendingQueueEntry holds the queue-phase timestamps captured from a
// JobStarted message. They are keyed by JobID and evicted when the
// corresponding JobCompleted arrives.
//
// Background: the github.com broker omits QueueTime from JobCompleted
// in production (confirmed across multiple runs). This fallback
// captures it from the earlier JobStarted message instead — but note:
// as of 2026-07-01, github.com sends QueueTime=0 on JobStarted too
// (verified live; only ScaleSetAssignTime/RunnerAssignTime are
// populated), so today no runner.queue span is emitted at all. The
// path is kept because it is cheap, tested, and lights up the moment
// the broker starts populating the field (e.g. GHES or a future fix).
//
// If the broker also omits QueueTime from JobStarted (i.e. both
// fields are zero), no runner.queue span is emitted — using the
// listener-observed wall-clock receipt time as the queue start would
// produce a near-zero span anchored at the wrong end of the interval.
type pendingQueueEntry struct {
	queueTime          time.Time
	scaleSetAssignTime time.Time
	storedAt           time.Time // wall-clock receipt; used for TTL only
}

type OTelRecorder struct {
	exporter  sdktrace.SpanExporter
	logger    *slog.Logger
	serverURL string
	res       *resource.Resource
	scope     instrumentation.Scope

	mu         sync.Mutex
	stopped    bool
	queue      chan []sdktrace.ReadOnlySpan
	workerDone chan struct{}

	// pendingMu guards pendingQueue.
	pendingMu    sync.Mutex
	pendingQueue map[string]pendingQueueEntry
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

	r := &OTelRecorder{
		exporter:  cfg.Exporter,
		logger:    logger,
		serverURL: serverURL,
		res:       res,
		scope: instrumentation.Scope{
			Name:    otelScopeName,
			Version: build.Version,
		},
		queue:        make(chan []sdktrace.ReadOnlySpan, spanQueueSize),
		workerDone:   make(chan struct{}),
		pendingQueue: make(map[string]pendingQueueEntry),
	}
	go r.exportLoop()
	return r
}

// exportLoop drains the span queue on a background goroutine so
// RecordJobCompleted never blocks the caller.
func (r *OTelRecorder) exportLoop() {
	defer close(r.workerDone)
	for spans := range r.queue {
		ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
		if err := r.exporter.ExportSpans(ctx, spans); err != nil {
			r.logger.Warn("failed to export OTel spans", "error", err)
		}
		cancel()
	}
}

// Shutdown drains queued spans (bounded by ctx) and releases exporter
// resources. It is idempotent; records arriving afterwards are dropped.
func (r *OTelRecorder) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.queue)
	}
	r.mu.Unlock()

	select {
	case <-r.workerDone:
		return r.exporter.Shutdown(ctx)
	case <-ctx.Done():
		return errors.Join(ctx.Err(), r.exporter.Shutdown(ctx))
	}
}

// RecordJobStarted captures the queue-phase timestamps from the
// JobStarted message so they are available when the corresponding
// JobCompleted arrives. The GitHub broker sends QueueTime=0 on
// JobCompleted in production; JobStarted carries the real value.
func (r *OTelRecorder) RecordJobStarted(msg *scaleset.JobStarted) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	if len(r.pendingQueue) >= maxPendingQueueEntries {
		r.logger.Warn("OTel pending-queue map full, runner.queue span may be missing",
			"job_id", msg.JobID, "run_id", msg.WorkflowRunID)
		return
	}
	r.pendingQueue[msg.JobID] = pendingQueueEntry{
		queueTime:          msg.QueueTime,
		scaleSetAssignTime: msg.ScaleSetAssignTime,
		storedAt:           time.Now(),
	}
}

func (r *OTelRecorder) RecordJobCompleted(msg *scaleset.JobCompleted) {
	// Retrieve and evict the pending entry stored at JobStarted time.
	r.pendingMu.Lock()
	pending, found := r.pendingQueue[msg.JobID]
	delete(r.pendingQueue, msg.JobID)
	r.pendingMu.Unlock()

	// Discard TTL-expired entries (abandoned jobs, listener restarts).
	if found && time.Since(pending.storedAt) > pendingQueueTTL {
		found = false
		pending = pendingQueueEntry{}
	}

	spans := r.buildJobSpans(msg, defaultRunAttempt, pending)
	if len(spans) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	select {
	case r.queue <- spans:
	default:
		r.logger.Warn("OTel span queue full, dropping job spans",
			"job", msg.JobDisplayName, "run_id", msg.WorkflowRunID)
	}
}

func (r *OTelRecorder) RecordStatistics(_ *scaleset.RunnerScaleSetStatistic) {}
func (r *OTelRecorder) RecordDesiredRunners(_ int)                           {}

// buildJobSpans constructs the runner.queue / runner.startup /
// runner.execution span stubs for the given completed job.
//
// pending holds the queue-phase timestamps captured from the earlier
// JobStarted message (see RecordJobStarted). It is used as a fallback
// when msg.QueueTime is zero, which is the observed production
// behaviour of the GitHub broker: JobCompleted never carries QueueTime
// while JobStarted does. The span timestamps therefore measure
// broker-set queue time, not listener-observed wall-clock time.
func (r *OTelRecorder) buildJobSpans(msg *scaleset.JobCompleted, attempt int64, pending pendingQueueEntry) []sdktrace.ReadOnlySpan {
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
		// cicd.worker.* are SPAN attrs here (not resource attrs) because one
		// listener serves many runners; the runner itself emits them as
		// resource attrs (one worker per process). Query both with TraceQL:
		//   span.cicd.worker.name || resource.cicd.worker.name
		// See runner docs/otel-id-contract.md § cicd.worker scope.
		attribute.String("cicd.worker.name", msg.RunnerName),
		attribute.String("cicd.worker.id", strconv.Itoa(msg.RunnerID)),
		attribute.String("vcs.repository.url.full", r.serverURL+"/"+repo),
		attribute.String("vcs.provider.name", "github"),
	}

	var stubs tracetest.SpanStubs

	// Resolve queue-phase start/end. Prefer timestamps from JobCompleted;
	// fall back to those stored from the earlier JobStarted when the
	// broker sends QueueTime=0 on JobCompleted (production behaviour).
	queueStart := msg.QueueTime
	queueEnd := msg.ScaleSetAssignTime
	if queueStart.IsZero() && !pending.queueTime.IsZero() {
		queueStart = pending.queueTime
		if queueEnd.IsZero() {
			queueEnd = pending.scaleSetAssignTime
		}
	}

	if !queueStart.IsZero() && !queueEnd.IsZero() {
		stubs = append(stubs, r.newStub(
			traceID, parentSC, "runner.queue",
			queueStart, queueEnd,
			// github.record_type matches the runner's attribute key (OTelTraceExporter.cs:526,618,711)
			append(sliceClone(commonAttrs), attribute.String("github.record_type", "runner.queue")),
		))
	}

	if !msg.ScaleSetAssignTime.IsZero() && !msg.RunnerAssignTime.IsZero() {
		stubs = append(stubs, r.newStub(
			traceID, parentSC, "runner.startup",
			msg.ScaleSetAssignTime, msg.RunnerAssignTime,
			append(sliceClone(commonAttrs), attribute.String("github.record_type", "runner.startup")),
		))
	}

	if !msg.RunnerAssignTime.IsZero() && !msg.FinishTime.IsZero() {
		stubs = append(stubs, r.newStub(
			traceID, parentSC, "runner.execution",
			msg.RunnerAssignTime, msg.FinishTime,
			append(sliceClone(commonAttrs),
				attribute.String("github.record_type", "runner.execution"),
				attribute.String("github.conclusion", conclusion),
				attribute.String("cicd.pipeline.task.run.result", conclusionToSemconv(conclusion)),
			),
		))
	}

	return stubs.Snapshots()
}

func normalizeConclusion(raw string) string {
	// Mirrors the runner's NormalizeConclusion (OTelTraceExporter.cs:1008-1019).
	// Abandoned maps to failure (TaskResult.Abandoned → "failure" in runner).
	// Any unknown value maps to "error" rather than being lowercased through.
	switch strings.ToLower(raw) {
	case "success", "succeeded":
		return "success"
	case "failure", "failed":
		return "failure"
	case "abandoned":
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
		return "error"
	}
}

func conclusionToSemconv(conclusion string) string {
	// semconv cicd.pipeline.task.run.result enum: success|failure|cancellation|skip|timeout|error
	// Mirrors ToSemconvResult (OTelTraceExporter.cs:1022-1033): unknown → "error".
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
	default:
		return "error"
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
