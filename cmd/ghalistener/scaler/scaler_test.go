package scaler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
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

	t.Run("guards the phase transition with the observed resourceVersion", func(t *testing.T) {
		runner := newTestEphemeralRunner(jobInfo.RunnerName, v1alpha1.EphemeralRunnerPhaseRunning)
		scaler, api, shutdown := newTestScalerWithAPI(t, runner, nil)
		defer shutdown()

		observedResourceVersion := runner.ResourceVersion
		require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

		meta, ok := api.patch()["metadata"].(map[string]any)
		require.True(t, ok, "patch must carry a metadata precondition, got %v", api.patch())
		assert.Equal(t, observedResourceVersion, meta["resourceVersion"])
	})

	t.Run("omits the precondition when the phase is not transitioned", func(t *testing.T) {
		runner := newTestEphemeralRunner(jobInfo.RunnerName, v1alpha1.EphemeralRunnerPhaseFailed)
		scaler, api, shutdown := newTestScalerWithAPI(t, runner, nil)
		defer shutdown()

		require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

		_, hasMetadata := api.patch()["metadata"]
		assert.False(t, hasMetadata, "job fields alone must not be guarded, got %v", api.patch())
	})

	// A runner can reach a terminal phase between the read that decides the
	// transition and the patch that applies it. Without a precondition the
	// listener would resurrect it back into Running.
	for _, phase := range []v1alpha1.EphemeralRunnerPhase{
		v1alpha1.EphemeralRunnerPhaseFailed,
		v1alpha1.EphemeralRunnerPhaseSucceeded,
		v1alpha1.EphemeralRunnerPhaseOutdated,
	} {
		t.Run("does not resurrect a runner that became "+string(phase)+" concurrently", func(t *testing.T) {
			runner := newTestEphemeralRunner(jobInfo.RunnerName, v1alpha1.EphemeralRunnerPhasePending)

			var raced bool
			scaler, api, shutdown := newTestScalerWithAPI(t, runner, func(runner *v1alpha1.EphemeralRunner) {
				if raced {
					return
				}
				raced = true
				runner.Status.Phase = phase
			})
			defer shutdown()

			require.NoError(t, scaler.HandleJobStarted(context.Background(), jobInfo))

			assert.Equal(t, phase, runner.Status.Phase)
			assertJobStartedStatus(t, runner, jobInfo)

			gets, patches, conflicts := api.counts()
			assert.Equal(t, 1, conflicts, "the stale patch must be rejected")
			assert.Equal(t, 2, gets, "the runner must be re-read after the conflict")
			assert.Equal(t, 2, patches)
		})
	}

	t.Run("gives up when the runner keeps changing", func(t *testing.T) {
		runner := newTestEphemeralRunner(jobInfo.RunnerName, v1alpha1.EphemeralRunnerPhasePending)
		scaler, api, shutdown := newTestScalerWithAPI(t, runner, func(runner *v1alpha1.EphemeralRunner) {})
		defer shutdown()

		err := scaler.HandleJobStarted(context.Background(), jobInfo)
		require.Error(t, err)
		assert.True(t, kerrors.IsConflict(err), "expected a conflict error, got %v", err)

		_, _, conflicts := api.counts()
		assert.Equal(t, retry.DefaultRetry.Steps, conflicts)
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

	scaler, _, shutdown := newTestScalerWithAPI(t, runner, nil)
	return scaler, shutdown
}

// fakeRunnerAPI records what the scaler sent to the API server.
type fakeRunnerAPI struct {
	mu         sync.Mutex
	gets       int
	patches    int
	conflicts  int
	lastPatch  map[string]any
	patchedRVs []string
}

func (f *fakeRunnerAPI) counts() (gets, patches, conflicts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets, f.patches, f.conflicts
}

func (f *fakeRunnerAPI) patch() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastPatch
}

// newTestScalerWithAPI serves runner over a fake API server that emulates the
// resourceVersion precondition enforced by kube-apiserver on merge patches.
// afterGet, when set, runs after every read is served and simulates another
// writer mutating the runner before the scaler's patch lands.
func newTestScalerWithAPI(t *testing.T, runner *v1alpha1.EphemeralRunner, afterGet func(runner *v1alpha1.EphemeralRunner)) (*Scaler, *fakeRunnerAPI, func()) {
	t.Helper()

	api := &fakeRunnerAPI{}
	if runner.ResourceVersion == "" {
		runner.ResourceVersion = "1"
	}

	bumpResourceVersion := func() {
		rv, err := strconv.Atoi(runner.ResourceVersion)
		require.NoError(t, err)
		runner.ResourceVersion = strconv.Itoa(rv + 1)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			api.gets++
			require.NoError(t, json.NewEncoder(w).Encode(runner))
			if afterGet != nil {
				afterGet(runner)
				bumpResourceVersion()
			}
		case http.MethodPatch:
			api.patches++

			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			var raw map[string]any
			require.NoError(t, json.Unmarshal(body, &raw))
			api.lastPatch = raw

			// Emulate the optimistic concurrency check performed by the API
			// server when metadata.resourceVersion is present in the patch.
			if meta, ok := raw["metadata"].(map[string]any); ok {
				if rv, ok := meta["resourceVersion"].(string); ok {
					api.patchedRVs = append(api.patchedRVs, rv)
					if rv != runner.ResourceVersion {
						api.conflicts++
						writeConflict(t, w, runner.Name)
						return
					}
				}
			}

			var patch v1alpha1.EphemeralRunner
			require.NoError(t, json.Unmarshal(body, &patch))

			runner.Status.JobRequestID = patch.Status.JobRequestID
			runner.Status.JobRepositoryName = patch.Status.JobRepositoryName
			runner.Status.JobID = patch.Status.JobID
			runner.Status.WorkflowRunID = patch.Status.WorkflowRunID
			runner.Status.JobWorkflowRef = patch.Status.JobWorkflowRef
			runner.Status.JobDisplayName = patch.Status.JobDisplayName
			if patch.Status.Phase != "" {
				runner.Status.Phase = patch.Status.Phase
			}
			bumpResourceVersion()

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
	}, api, server.Close
}

func writeConflict(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()

	w.WriteHeader(http.StatusConflict)
	require.NoError(t, json.NewEncoder(w).Encode(&metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure,
		Code:     http.StatusConflict,
		Reason:   metav1.StatusReasonConflict,
		Message:  fmt.Sprintf("Operation cannot be fulfilled on ephemeralrunners.actions.github.com %q: the object has been modified", name),
	}))
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
