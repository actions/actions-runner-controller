package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type captureExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *captureExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *captureExporter) Shutdown(_ context.Context) error { return nil }

func (e *captureExporter) Spans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]sdktrace.ReadOnlySpan, len(e.spans))
	copy(out, e.spans)
	return out
}

func newTestRecorder(t *testing.T, exp sdktrace.SpanExporter) *OTelRecorder {
	t.Helper()
	return NewOTelRecorder(OTelRecorderConfig{Exporter: exp})
}

// flush drains the recorder's async export queue so tests can assert
// on the exported spans deterministically.
func flush(t *testing.T, rec *OTelRecorder) {
	t.Helper()
	require.NoError(t, rec.Shutdown(context.Background()))
}

func TestOTelRecorder_EmitsThreeSpans(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	msg := &scaleset.JobCompleted{
		Result:     "Succeeded",
		RunnerID:   7,
		RunnerName: "runner-abc-xyz",
		JobMessageBase: scaleset.JobMessageBase{
			RunnerRequestID:    1,
			WorkflowRunID:      99999,
			JobID:              "42",
			JobDisplayName:     "build",
			OwnerName:          "acme",
			RepositoryName:     "widgets",
			JobWorkflowRef:     "acme/widgets/.github/workflows/ci.yml@refs/heads/main",
			QueueTime:          now,
			ScaleSetAssignTime: now.Add(10 * time.Second),
			RunnerAssignTime:   now.Add(40 * time.Second),
			FinishTime:         now.Add(5 * time.Minute),
		},
	}

	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 3)

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byName[s.Name()] = s
	}

	expectedTraceID := newTraceID(99999, 1)
	for _, s := range spans {
		assert.Equal(t, expectedTraceID, s.SpanContext().TraceID())
	}

	// Parent must be the contract job span ID the runner emits:
	// sha256("job-{run_id}-{run_attempt}-{job_name}")[:8].
	expectedParent := newJobSpanID(99999, 1, "build")
	for _, s := range spans {
		assert.Equal(t, expectedParent, s.Parent().SpanID())
	}
	assert.Equal(t, "81606d47848a59c0", expectedParent.String())

	q := byName["runner.queue"]
	require.NotNil(t, q)
	assert.Equal(t, now, q.StartTime())
	assert.Equal(t, now.Add(10*time.Second), q.EndTime())
	// M1: key must be github.record_type (matches runner's OTelTraceExporter.cs:526,618,711)
	assertAttr(t, q, "github.record_type", "runner.queue")

	s := byName["runner.startup"]
	require.NotNil(t, s)
	assert.Equal(t, now.Add(10*time.Second), s.StartTime())
	assert.Equal(t, now.Add(40*time.Second), s.EndTime())
	assertAttr(t, s, "github.record_type", "runner.startup")

	e := byName["runner.execution"]
	require.NotNil(t, e)
	assert.Equal(t, now.Add(40*time.Second), e.StartTime())
	assert.Equal(t, now.Add(5*time.Minute), e.EndTime())
	assertAttr(t, e, "github.record_type", "runner.execution")
	assertAttr(t, e, "github.conclusion", "success")
	assertAttr(t, e, "cicd.pipeline.task.run.result", "success")

	// Shared attributes must match the runner exporter's names, types,
	// and values so a single TraceQL query matches both span sets.
	for _, span := range byName {
		assertAttr(t, span, "github.run_id", "99999")
		assertAttr(t, span, "github.run_attempt", "1")
		assertAttr(t, span, "github.repository", "acme/widgets")
		assertAttr(t, span, "cicd.pipeline.run.id", "99999")
		assertAttr(t, span, "cicd.pipeline.run.url.full", "https://github.com/acme/widgets/actions/runs/99999/attempts/1")
		assertAttr(t, span, "cicd.pipeline.task.name", "build")
		assertAttr(t, span, "cicd.pipeline.task.run.id", "81606d47848a59c0")
		assertAttr(t, span, "cicd.worker.name", "runner-abc-xyz")
		assertAttr(t, span, "cicd.worker.id", "7")
		assertAttr(t, span, "vcs.repository.url.full", "https://github.com/acme/widgets")
		assertAttr(t, span, "vcs.provider.name", "github")

		// Old key must not appear; runner uses github.record_type (M1).
		assertNoAttr(t, span, "type")
		// Keys the runner never emits must not be invented here.
		assertNoAttr(t, span, "github.job_id")
		assertNoAttr(t, span, "github.job_name")
		assertNoAttr(t, span, "github.runner_name")
		assertNoAttr(t, span, "github.runner_id")
	}
}

func TestOTelRecorder_ServerURLIsConfigurable(t *testing.T) {
	exp := &captureExporter{}
	rec := NewOTelRecorder(OTelRecorderConfig{
		Exporter:  exp,
		ServerURL: "https://ghes.example.com/",
	})

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    12345,
			JobID:            "1",
			JobDisplayName:   "build",
			OwnerName:        "org",
			RepositoryName:   "repo",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Minute),
		},
	}

	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	assertAttr(t, spans[0], "vcs.repository.url.full", "https://ghes.example.com/org/repo")
	assertAttr(t, spans[0], "cicd.pipeline.run.url.full", "https://ghes.example.com/org/repo/actions/runs/12345/attempts/1")
}

func TestOTelRecorder_ResourceAndScope(t *testing.T) {
	exp := &captureExporter{}
	rec := NewOTelRecorder(OTelRecorderConfig{
		Exporter:          exp,
		ScaleSetName:      "my-scale-set",
		ScaleSetNamespace: "arc-runners",
	})

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    1,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}

	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	s := spans[0]

	res := s.Resource()
	require.NotNil(t, res)
	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	assert.Equal(t, "gha-listener", got["service.name"])
	assert.NotEmpty(t, got["service.version"])
	assert.Equal(t, "arc-runners", got["service.namespace"])
	assert.Equal(t, "my-scale-set", got["github.scale_set.name"])

	scope := s.InstrumentationScope()
	assert.Equal(t, "github.com/actions/actions-runner-controller/cmd/ghalistener/metrics", scope.Name)
}

func TestOTelRecorder_ResourceHonorsOTelEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "my-custom-listener")

	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    1,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}

	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	for _, kv := range spans[0].Resource().Attributes() {
		if string(kv.Key) == "service.name" {
			assert.Equal(t, "my-custom-listener", kv.Value.AsString())
			return
		}
	}
	t.Error("service.name not found on resource")
}

func TestOTelRecorder_SkipsMissingTimestamps(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    12345,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Minute),
		},
	}

	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1, "only runner.execution when queue/startup timestamps are zero")
	assert.Equal(t, "runner.execution", spans[0].Name())
}

func TestOTelRecorder_CommonAttributes(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result:     "Failed",
		RunnerID:   3,
		RunnerName: "runner-3",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    55555,
			JobID:            "100",
			JobDisplayName:   "test-suite",
			OwnerName:        "org",
			RepositoryName:   "repo",
			JobWorkflowRef:   "org/repo/.github/workflows/test.yml@main",
			RunnerAssignTime: now,
			FinishTime:       now.Add(2 * time.Minute),
		},
	}

	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	s := spans[0]

	assertAttr(t, s, "cicd.pipeline.task.name", "test-suite")
	assertAttr(t, s, "github.repository", "org/repo")
	assertAttr(t, s, "cicd.worker.name", "runner-3")
	assertAttr(t, s, "github.conclusion", "failure")
	assertAttr(t, s, "cicd.pipeline.task.run.result", "failure")
}

// blockingExporter blocks every ExportSpans call until released.
type blockingExporter struct {
	captureExporter
	release chan struct{}
}

func (e *blockingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	<-e.release
	return e.captureExporter.ExportSpans(ctx, spans)
}

func completedMsg(runID int64) *scaleset.JobCompleted {
	now := time.Now()
	return &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    runID,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}
}

// The autoscaling message loop calls RecordJobCompleted synchronously;
// it must never wait on the telemetry backend.
func TestOTelRecorder_RecordDoesNotBlockOnSlowExporter(t *testing.T) {
	exp := &blockingExporter{release: make(chan struct{})}
	defer close(exp.release)
	rec := newTestRecorder(t, exp)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			rec.RecordJobCompleted(completedMsg(int64(i + 1)))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RecordJobCompleted blocked on a stalled exporter")
	}
}

func TestOTelRecorder_ShutdownFlushesQueuedSpans(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	for i := range 10 {
		rec.RecordJobCompleted(completedMsg(int64(i + 1)))
	}

	require.NoError(t, rec.Shutdown(context.Background()))
	assert.Len(t, exp.Spans(), 10)
}

func TestOTelRecorder_RecordAfterShutdownIsNoOp(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)
	require.NoError(t, rec.Shutdown(context.Background()))

	rec.RecordJobCompleted(completedMsg(1))

	assert.Empty(t, exp.Spans())
	require.NoError(t, rec.Shutdown(context.Background()), "Shutdown must be idempotent")
}

func TestOTelRecorder_ShutdownHonorsContext(t *testing.T) {
	exp := &blockingExporter{release: make(chan struct{})}
	defer close(exp.release)
	rec := newTestRecorder(t, exp)

	rec.RecordJobCompleted(completedMsg(1))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := rec.Shutdown(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestOTelRecorder_JobStartedIsNoOp(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)
	rec.RecordJobStarted(&scaleset.JobStarted{})
	assert.Empty(t, exp.Spans())
}

func TestOTelRecorder_StatisticsIsNoOp(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)
	rec.RecordStatistics(&scaleset.RunnerScaleSetStatistic{TotalRunningJobs: 5})
	assert.Empty(t, exp.Spans())
}

func TestCompositeRecorder_DelegatesAll(t *testing.T) {
	exp1 := &captureExporter{}
	exp2 := &captureExporter{}
	r1 := newTestRecorder(t, exp1)
	r2 := newTestRecorder(t, exp2)
	comp := NewComposite(r1, r2)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    1,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}
	comp.RecordJobCompleted(msg)
	flush(t, r1)
	flush(t, r2)

	assert.Len(t, exp1.Spans(), 1)
	assert.Len(t, exp2.Spans(), 1)
}

// Conformance vectors from the normative ID contract
// (actions/runner docs/otel-id-contract.md). A conforming
// implementation MUST reproduce these exactly; the runner's L0 suite
// asserts the same values.
func TestIDContractGoldenVectors(t *testing.T) {
	assert.Equal(t, "acad1e2a107636235fd56bb742499bd0", newTraceID(99999, 1).String(),
		`trace ID must be sha256("{run_id}-{run_attempt}")[:16]`)
	assert.Equal(t, "81606d47848a59c0", newJobSpanID(99999, 1, "build").String(),
		`job span ID must be sha256("job-{run_id}-{run_attempt}-{job_name}")[:8]`)
	assert.Equal(t, newTraceID(99999, 1), newTraceID(99999, 0),
		"run_attempt 0 must normalize to 1")
	assert.Equal(t, newJobSpanID(99999, 1, "build"), newJobSpanID(99999, 0, "build"),
		"run_attempt 0 must normalize to 1")
}

func TestDeterministicIDs(t *testing.T) {
	tid1 := newTraceID(12345, 1)
	tid2 := newTraceID(12345, 1)
	tid3 := newTraceID(12345, 2)
	assert.Equal(t, tid1, tid2, "same inputs must produce same trace ID")
	assert.NotEqual(t, tid1, tid3, "different attempt must produce different trace ID")

	sid1 := newJobSpanID(42, 1, "build")
	sid2 := newJobSpanID(42, 1, "build")
	sid3 := newJobSpanID(42, 1, "deploy")
	assert.Equal(t, sid1, sid2)
	assert.NotEqual(t, sid1, sid3)
}

// TestNormalizeConclusion_Conformance verifies M3: ARC must mirror the runner's
// NormalizeConclusion mapping (OTelTraceExporter.cs:1008-1019).
// Abandoned → "failure"; any unknown raw value → "error" (not lowercased pass-through).
func TestNormalizeConclusion_Conformance(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		// known set passes through
		{"success", "success"},
		{"Succeeded", "success"},
		{"failure", "failure"},
		{"Failed", "failure"},
		{"cancelled", "cancelled"},
		{"Canceled", "cancelled"},
		{"skipped", "skipped"},
		{"timed_out", "timed_out"},
		{"startup_failure", "startup_failure"},
		// abandoned maps to failure (mirrors TaskResult.Abandoned in runner)
		{"abandoned", "failure"},
		{"Abandoned", "failure"},
		// unknown raw values → "error", not lowercased pass-through
		{"totally_unknown", "error"},
		{"", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeConclusion(tc.raw))
		})
	}
}

// TestConclusionToSemconv_Conformance verifies M3: unknown conclusion must map to
// "error" for cicd.pipeline.task.run.result (mirrors ToSemconvResult default in runner).
func TestConclusionToSemconv_Conformance(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"success", "success"},
		{"failure", "failure"},
		{"cancelled", "cancellation"},
		{"skipped", "skip"},
		{"timed_out", "timeout"},
		{"startup_failure", "error"},
		// unknown → "error", not the raw value passed through
		{"error", "error"},
		{"unknown_value", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, conclusionToSemconv(tc.in))
		})
	}
}

// TestOTelRecorder_AbandonedConclusion verifies M3 end-to-end:
// a job with Result="Abandoned" must emit github.conclusion="failure"
// and cicd.pipeline.task.run.result="failure".
func TestOTelRecorder_AbandonedConclusion(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Abandoned",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    1,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}
	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	assertAttr(t, spans[0], "github.conclusion", "failure")
	assertAttr(t, spans[0], "cicd.pipeline.task.run.result", "failure")
}

// TestOTelRecorder_UnknownConclusion verifies M3 end-to-end:
// an unrecognised Result must emit github.conclusion="error"
// and cicd.pipeline.task.run.result="error" (not the raw lowercased value).
func TestOTelRecorder_UnknownConclusion(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "SomeNewFutureState",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    1,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}
	rec.RecordJobCompleted(msg)
	flush(t, rec)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	assertAttr(t, spans[0], "github.conclusion", "error")
	assertAttr(t, spans[0], "cicd.pipeline.task.run.result", "error")
}

// TestOTelRecorder_QueueSpanFromJobStarted verifies that runner.queue is
// emitted even when JobCompleted.QueueTime is zero (the confirmed
// production behaviour: the GitHub broker omits QueueTime from
// JobCompleted). The recorder must fall back to the QueueTime and
// ScaleSetAssignTime captured from the earlier JobStarted message,
// keyed by JobID.
func TestOTelRecorder_QueueSpanFromJobStarted(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	// JobStarted arrives first and carries the real queue-phase timestamps.
	rec.RecordJobStarted(&scaleset.JobStarted{
		JobMessageBase: scaleset.JobMessageBase{
			JobID:              "job-42",
			WorkflowRunID:      99999,
			JobDisplayName:     "build",
			QueueTime:          now,
			ScaleSetAssignTime: now.Add(10 * time.Second),
			RunnerAssignTime:   now.Add(40 * time.Second),
		},
	})

	// JobCompleted arrives with QueueTime=0 — the broker bug.
	rec.RecordJobCompleted(&scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			JobID:              "job-42",
			WorkflowRunID:      99999,
			JobDisplayName:     "build",
			OwnerName:          "acme",
			RepositoryName:     "widgets",
			QueueTime:          time.Time{}, // zero — broker does not send it
			ScaleSetAssignTime: now.Add(10 * time.Second),
			RunnerAssignTime:   now.Add(40 * time.Second),
			FinishTime:         now.Add(5 * time.Minute),
		},
	})
	flush(t, rec)

	spans := exp.Spans()
	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		byName[s.Name()] = s
	}

	q := byName["runner.queue"]
	require.NotNil(t, q, "runner.queue must fire even when JobCompleted.QueueTime=0")
	assert.Equal(t, now, q.StartTime(), "start must be QueueTime from JobStarted")
	assert.Equal(t, now.Add(10*time.Second), q.EndTime(), "end must be ScaleSetAssignTime")
}

// TestOTelRecorder_QueueEntryEvictedOnCompleted verifies that the
// pending-queue map is cleared after JobCompleted so a second
// JobCompleted for the same JobID does not re-emit runner.queue.
func TestOTelRecorder_QueueEntryEvictedOnCompleted(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)

	now := time.Now()

	rec.RecordJobStarted(&scaleset.JobStarted{
		JobMessageBase: scaleset.JobMessageBase{
			JobID:              "job-99",
			WorkflowRunID:      1,
			QueueTime:          now,
			ScaleSetAssignTime: now.Add(5 * time.Second),
		},
	})

	first := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			JobID:            "job-99",
			WorkflowRunID:    1,
			RunnerAssignTime: now.Add(10 * time.Second),
			FinishTime:       now.Add(time.Minute),
		},
	}
	rec.RecordJobCompleted(first)

	// Simulate a duplicate/stale JobCompleted for the same job.
	rec.RecordJobCompleted(first)
	flush(t, rec)

	queueCount := 0
	for _, s := range exp.Spans() {
		if s.Name() == "runner.queue" {
			queueCount++
		}
	}
	assert.Equal(t, 1, queueCount, "runner.queue must be emitted exactly once per job")
}

func assertAttr(t *testing.T, span sdktrace.ReadOnlySpan, key, expected string) {
	t.Helper()
	for _, a := range span.Attributes() {
		if string(a.Key) == key {
			assert.Equal(t, expected, a.Value.AsString(), "attribute %q", key)
			return
		}
	}
	t.Errorf("attribute %q not found on span %q", key, span.Name())
}

func assertNoAttr(t *testing.T, span sdktrace.ReadOnlySpan, key string) {
	t.Helper()
	for _, a := range span.Attributes() {
		if string(a.Key) == key {
			t.Errorf("attribute %q must not be set on span %q", key, span.Name())
		}
	}
}
