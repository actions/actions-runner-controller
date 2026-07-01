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
	assertAttr(t, q, "type", "runner.queue")

	s := byName["runner.startup"]
	require.NotNil(t, s)
	assert.Equal(t, now.Add(10*time.Second), s.StartTime())
	assert.Equal(t, now.Add(40*time.Second), s.EndTime())
	assertAttr(t, s, "type", "runner.startup")

	e := byName["runner.execution"]
	require.NotNil(t, e)
	assert.Equal(t, now.Add(40*time.Second), e.StartTime())
	assert.Equal(t, now.Add(5*time.Minute), e.EndTime())
	assertAttr(t, e, "type", "runner.execution")
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

	spans := exp.Spans()
	require.Len(t, spans, 1)
	s := spans[0]

	assertAttr(t, s, "cicd.pipeline.task.name", "test-suite")
	assertAttr(t, s, "github.repository", "org/repo")
	assertAttr(t, s, "cicd.worker.name", "runner-3")
	assertAttr(t, s, "github.conclusion", "failure")
	assertAttr(t, s, "cicd.pipeline.task.run.result", "failure")
}

func TestOTelRecorder_SetRunAttempt(t *testing.T) {
	exp := &captureExporter{}
	rec := newTestRecorder(t, exp)
	rec.SetRunAttempt(3)

	now := time.Now()
	msg := &scaleset.JobCompleted{
		Result: "Succeeded",
		JobMessageBase: scaleset.JobMessageBase{
			WorkflowRunID:    77777,
			JobID:            "1",
			RunnerAssignTime: now,
			FinishTime:       now.Add(time.Second),
		},
	}

	rec.RecordJobCompleted(msg)

	spans := exp.Spans()
	require.Len(t, spans, 1)
	assert.Equal(t, newTraceID(77777, 3), spans[0].SpanContext().TraceID())
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
