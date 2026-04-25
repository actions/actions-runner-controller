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

func TestOTelRecorder_EmitsThreeSpans(t *testing.T) {
	exp := &captureExporter{}
	rec := NewOTelRecorder(exp, nil)

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

	expectedParent := newSpanID(42)
	for _, s := range spans {
		assert.Equal(t, expectedParent, s.Parent().SpanID())
	}

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
	assertAttr(t, e, "github.conclusion", "Succeeded")
}

func TestOTelRecorder_SkipsMissingTimestamps(t *testing.T) {
	exp := &captureExporter{}
	rec := NewOTelRecorder(exp, nil)

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
	rec := NewOTelRecorder(exp, nil)

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

	assertAttr(t, s, "github.job_name", "test-suite")
	assertAttr(t, s, "github.repository", "org/repo")
	assertAttr(t, s, "github.runner_name", "runner-3")
	assertAttr(t, s, "github.conclusion", "Failed")
}

func TestOTelRecorder_SetRunAttempt(t *testing.T) {
	exp := &captureExporter{}
	rec := NewOTelRecorder(exp, nil)
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
	rec := NewOTelRecorder(exp, nil)
	rec.RecordJobStarted(&scaleset.JobStarted{})
	assert.Empty(t, exp.Spans())
}

func TestOTelRecorder_StatisticsIsNoOp(t *testing.T) {
	exp := &captureExporter{}
	rec := NewOTelRecorder(exp, nil)
	rec.RecordStatistics(&scaleset.RunnerScaleSetStatistic{TotalRunningJobs: 5})
	assert.Empty(t, exp.Spans())
}

func TestCompositeRecorder_DelegatesAll(t *testing.T) {
	exp1 := &captureExporter{}
	exp2 := &captureExporter{}
	r1 := NewOTelRecorder(exp1, nil)
	r2 := NewOTelRecorder(exp2, nil)
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

func TestDeterministicIDs(t *testing.T) {
	tid1 := newTraceID(12345, 1)
	tid2 := newTraceID(12345, 1)
	tid3 := newTraceID(12345, 2)
	assert.Equal(t, tid1, tid2, "same inputs must produce same trace ID")
	assert.NotEqual(t, tid1, tid3, "different attempt must produce different trace ID")

	sid1 := newSpanID(42)
	sid2 := newSpanID(42)
	sid3 := newSpanID(43)
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
