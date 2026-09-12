package scaler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var discardLogger = slog.New(slog.DiscardHandler)

func TestEffectiveRateLimiterConfig(t *testing.T) {
	qps := 75
	burst := 150
	zero := 0
	negative := -1

	tests := []struct {
		name      string
		config    *v1alpha1.ScalerConfig
		wantQPS   int
		wantBurst int
		wantLog   string
		wantLevel string
	}{
		{
			name: "uses configured values",
			config: &v1alpha1.ScalerConfig{
				QPS:   &qps,
				Burst: &burst,
			},
			wantQPS:   qps,
			wantBurst: burst,
		},
		{
			name:      "defaults missing config",
			wantQPS:   defaultQPS,
			wantBurst: defaultBurst,
			wantLog:   "Listener scaler configuration is missing; using defaults",
			wantLevel: "DEBUG",
		},
		{
			name:      "defaults missing qps",
			config:    &v1alpha1.ScalerConfig{Burst: &burst},
			wantQPS:   defaultQPS,
			wantBurst: burst,
			wantLog:   "Listener scaler qps is missing; using default",
			wantLevel: "DEBUG",
		},
		{
			name:      "defaults missing burst",
			config:    &v1alpha1.ScalerConfig{QPS: &qps},
			wantQPS:   qps,
			wantBurst: defaultBurst,
			wantLog:   "Listener scaler burst is missing; using default",
			wantLevel: "DEBUG",
		},
		{
			name:      "defaults zero qps",
			config:    &v1alpha1.ScalerConfig{QPS: &zero, Burst: &burst},
			wantQPS:   defaultQPS,
			wantBurst: burst,
			wantLog:   "Listener scaler qps must be greater than 0; using default",
			wantLevel: "WARN",
		},
		{
			name:      "defaults negative burst",
			config:    &v1alpha1.ScalerConfig{QPS: &qps, Burst: &negative},
			wantQPS:   qps,
			wantBurst: defaultBurst,
			wantLog:   "Listener scaler burst must be greater than 0; using default",
			wantLevel: "WARN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

			qps, burst := effectiveRateLimiterConfig(tt.config, logger)

			assert.Equal(t, tt.wantQPS, qps)
			assert.Equal(t, tt.wantBurst, burst)
			if tt.wantLog == "" {
				assert.Empty(t, logs.String())
				return
			}
			assert.Contains(t, logs.String(), "msg="+strconv.Quote(tt.wantLog))
			// Missing values are a normal configuration, so they must not be
			// logged as warnings; only out-of-range values are.
			assert.Contains(t, logs.String(), "level="+tt.wantLevel)
		})
	}
}

// TestEffectiveRateLimiterConfig_QuietAtInfoLevel asserts that a listener which
// does not configure the scaler produces no output at the default log level.
func TestEffectiveRateLimiterConfig_QuietAtInfoLevel(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config *v1alpha1.ScalerConfig
	}{
		{name: "nil config"},
		{name: "empty config", config: &v1alpha1.ScalerConfig{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

			qps, burst := effectiveRateLimiterConfig(tt.config, logger)

			assert.Equal(t, defaultQPS, qps)
			assert.Equal(t, defaultBurst, burst)
			assert.Empty(t, logs.String())
		})
	}
}

func TestHandleJobStarted(t *testing.T) {
	jobInfo := &scaleset.JobStarted{
		RunnerName: "runner-1",
		JobMessageBase: scaleset.JobMessageBase{
			OwnerName:       "actions",
			RepositoryName:  "actions-runner-controller",
			JobID:           "job-1",
			WorkflowRunID:   456,
			JobWorkflowRef:  "actions/actions-runner-controller/.github/workflows/ci.yaml@refs/heads/main",
			JobDisplayName:  "build",
			RunnerRequestID: 123,
		},
	}

	t.Run("patches job fields and running phase together", func(t *testing.T) {
		runner := newTestEphemeralRunner(jobInfo.RunnerName, "")
		scaler, shutdown := newTestScaler(t, runner)
		defer shutdown()

		require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

		assertJobStartedStatus(t, runner, jobInfo)
		assert.Equal(t, v1alpha1.EphemeralRunnerPhaseRunning, runner.Status.Phase)
	})

	t.Run("repeated assignment remains idempotent", func(t *testing.T) {
		runner := newTestEphemeralRunner(jobInfo.RunnerName, v1alpha1.EphemeralRunnerPhaseRunning)
		scaler, shutdown := newTestScaler(t, runner)
		defer shutdown()

		require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))
		firstStatus := runner.Status
		require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

		assert.Equal(t, firstStatus, runner.Status)
		assertJobStartedStatus(t, runner, jobInfo)
		assert.Equal(t, v1alpha1.EphemeralRunnerPhaseRunning, runner.Status.Phase)
	})

	for _, phase := range []v1alpha1.EphemeralRunnerPhase{
		v1alpha1.EphemeralRunnerPhaseFailed,
		v1alpha1.EphemeralRunnerPhaseSucceeded,
		v1alpha1.EphemeralRunnerPhaseOutdated,
	} {
		t.Run("preserves "+string(phase)+" phase while patching job fields", func(t *testing.T) {
			runner := newTestEphemeralRunner(jobInfo.RunnerName, phase)
			scaler, shutdown := newTestScaler(t, runner)
			defer shutdown()

			require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

			assertJobStartedStatus(t, runner, jobInfo)
			assert.Equal(t, phase, runner.Status.Phase)
		})
	}

	t.Run("preserves deleting runner phase while patching job fields", func(t *testing.T) {
		runner := newTestEphemeralRunner(jobInfo.RunnerName, v1alpha1.EphemeralRunnerPhasePending)
		deletionTimestamp := metav1.Now()
		runner.DeletionTimestamp = &deletionTimestamp
		scaler, shutdown := newTestScaler(t, runner)
		defer shutdown()

		require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

		assertJobStartedStatus(t, runner, jobInfo)
		assert.Equal(t, v1alpha1.EphemeralRunnerPhasePending, runner.Status.Phase)
	})
}

func newTestEphemeralRunner(name string, phase v1alpha1.EphemeralRunnerPhase) *v1alpha1.EphemeralRunner {
	return &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Status: v1alpha1.EphemeralRunnerStatus{
			Phase: phase,
		},
	}
}

func newTestScaler(t *testing.T, runner *v1alpha1.EphemeralRunner) (*Scaler, func()) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			require.NoError(t, json.NewEncoder(w).Encode(runner))
		case http.MethodPatch:
			var patch v1alpha1.EphemeralRunner
			require.NoError(t, json.NewDecoder(r.Body).Decode(&patch))

			runner.Status.JobRequestID = patch.Status.JobRequestID
			runner.Status.JobRepositoryName = patch.Status.JobRepositoryName
			runner.Status.JobID = patch.Status.JobID
			runner.Status.WorkflowRunID = patch.Status.WorkflowRunID
			runner.Status.JobWorkflowRef = patch.Status.JobWorkflowRef
			runner.Status.JobDisplayName = patch.Status.JobDisplayName
			if patch.Status.Phase != "" {
				runner.Status.Phase = patch.Status.Phase
			}

			require.NoError(t, json.NewEncoder(w).Encode(runner))
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)

	return &Scaler{
		clientset: clientset,
		config: Config{
			EphemeralRunnerSetNamespace: runner.Namespace,
		},
		targetRunners: -1,
		patchSeq:      -1,
		logger:        discardLogger,
	}, server.Close
}

func assertJobStartedStatus(t *testing.T, runner *v1alpha1.EphemeralRunner, jobInfo *scaleset.JobStarted) {
	t.Helper()

	assert.Equal(t, jobInfo.RunnerRequestID, runner.Status.JobRequestID)
	assert.Equal(t, jobInfo.JobID, runner.Status.JobID)
	assert.Equal(t, jobInfo.OwnerName+"/"+jobInfo.RepositoryName, runner.Status.JobRepositoryName)
	assert.Equal(t, jobInfo.WorkflowRunID, runner.Status.WorkflowRunID)
	assert.Equal(t, jobInfo.JobWorkflowRef, runner.Status.JobWorkflowRef)
	assert.Equal(t, jobInfo.JobDisplayName, runner.Status.JobDisplayName)
}

func TestSetDesiredWorkerState_MinMaxDefaults(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 0,
				MaxRunners: math.MaxInt32,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("init calculate with acquired 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
		assert.Equal(t, 0, patchID)
	})

	t.Run("init calculate with acquired 1", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
		assert.Equal(t, 0, patchID)
	})

	t.Run("increment patch when job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("increment patch when called with same parameters", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		patchID = w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("calculate desired scale when acquired > 0 and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		w.dirty = true
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("adjust when acquired == 0 and completed == 1", func(t *testing.T) {
		w := newEmptyWorker()
		w.dirty = true
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.False(t, w.dirty)
		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_MinSet(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 1,
				MaxRunners: math.MaxInt32,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("desired patch is 0 but sequence continues on empty batch and min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 4, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 2, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_MaxSet(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 0,
				MaxRunners: 5,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("request back to 0 on job done", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 5, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		w.setDesiredWorkerState(5)
		assert.False(t, w.dirty)
		assert.Equal(t, 5, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count > max and completed > 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(1)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(6)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 5, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale back to 0 when count was > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(6)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 0, w.targetRunners)
		assert.Equal(t, 2, w.patchSeq)
	})
}

func TestSetDesiredWorkerState_MinMaxSet(t *testing.T) {
	newEmptyWorker := func() *Scaler {
		return &Scaler{
			config: Config{
				MinRunners: 1,
				MaxRunners: 3,
			},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("initial scale when acquired == 0 and completed == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to min when count == 0", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(2)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)
	})

	t.Run("scale up to max when count > max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(4)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("scale to max when count == max", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)
	})

	t.Run("force 0 on empty batch and last patch == min runners", func(t *testing.T) {
		w := newEmptyWorker()
		patchID := w.setDesiredWorkerState(3)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID)
		assert.Equal(t, 3, w.targetRunners)
		assert.Equal(t, 0, w.patchSeq)

		w.dirty = true
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 1, patchID)
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 1, w.patchSeq)

		// Empty batch on min runners
		patchID = w.setDesiredWorkerState(0)
		assert.False(t, w.dirty)
		assert.Equal(t, 0, patchID) // forcing the state
		assert.Equal(t, 1, w.targetRunners)
		assert.Equal(t, 2, w.patchSeq)
	})
}
