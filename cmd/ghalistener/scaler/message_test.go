package scaler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type acquiringClient struct {
	listener.Client
	acquire func(context.Context, []int64) ([]int64, error)
}

func (c acquiringClient) AcquireJobs(ctx context.Context, ids []int64) ([]int64, error) {
	return c.acquire(ctx, ids)
}

type messageRecorder struct {
	metrics.Recorder
	events []string
}

func (r *messageRecorder) RecordStatistics(*scaleset.RunnerScaleSetStatistic) {
	r.events = append(r.events, "statistics")
}

func (r *messageRecorder) RecordJobStarted(*scaleset.JobStarted) {
	r.events = append(r.events, "started")
}

func (r *messageRecorder) RecordJobCompleted(*scaleset.JobCompleted) {
	r.events = append(r.events, "completed")
}

func (r *messageRecorder) RecordDesiredRunners(count int) {
	r.events = append(r.events, "desired")
}

func TestScaleHandlesListenerMessages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		jobs       bool
		nilMessage bool
		noStats    bool
		acquireErr bool
		patchErr   string
		wantErr    string
		wantEvents []string
	}{
		{name: "initial statistics", wantEvents: []string{"statistics", "desired"}},
		{name: "idle poll uses cached statistics", nilMessage: true},
		{name: "jobs and metrics", jobs: true, wantEvents: []string{"statistics", "started", "completed", "desired"}},
		{name: "acquisition failure", jobs: true, acquireErr: true, wantErr: "failed to acquire available jobs", wantEvents: []string{"statistics"}},
		{name: "runner patch failure", jobs: true, patchErr: "ephemeralrunners/", wantErr: "failed to handle job started", wantEvents: []string{"statistics", "started"}},
		{name: "scale failure", patchErr: "ephemeralrunnersets/", wantErr: "failed to handle desired runner count", wantEvents: []string{"statistics"}},
		{name: "missing statistics", noStats: true, wantErr: "message statistics are required"},
		{name: "idle poll without initial statistics", nilMessage: true, noStats: true, wantErr: "initial message statistics are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var patches []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				patches = append(patches, r.URL.Path)
				if tc.patchErr != "" && strings.Contains(r.URL.Path, tc.patchErr) {
					http.Error(w, "patch failed", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "ephemeralrunnersets/") {
					var set v1alpha1.EphemeralRunnerSet
					require.NoError(t, json.NewDecoder(r.Body).Decode(&set))
					require.Equal(t, 5, set.Spec.Replicas)
					require.NoError(t, json.NewEncoder(w).Encode(&set))
				} else {
					var runner v1alpha1.EphemeralRunner
					require.NoError(t, json.NewDecoder(r.Body).Decode(&runner))
					require.Equal(t, "job", runner.Status.JobID)
					require.NoError(t, json.NewEncoder(w).Encode(&runner))
				}
			}))
			defer server.Close()
			clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
			require.NoError(t, err)
			recorder := &messageRecorder{Recorder: metrics.Discard}
			acquisitions := 0
			w := &Scaler{
				clientset: clientset, logger: discardLogger, recorder: recorder,
				config: Config{
					EphemeralRunnerSetNamespace: "default", EphemeralRunnerSetName: "set",
					MinRunners: 2, MaxRunners: 10,
				},
				targetRunners: -1, patchSeq: -1,
				messageClient: acquiringClient{acquire: func(ctx context.Context, ids []int64) ([]int64, error) {
					acquisitions++
					require.Equal(t, t.Context(), ctx)
					require.Equal(t, []int64{10, 11}, ids)
					if tc.acquireErr {
						return nil, errors.New("acquisition failed")
					}
					return ids[:1], nil
				}},
			}
			message := &scaleset.RunnerScaleSetMessage{
				MessageID: listener.InitialMessageID, Statistics: &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3},
			}
			if tc.jobs {
				message.JobAvailableMessages = []*scaleset.JobAvailable{
					{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 10}},
					{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 11}},
				}
				message.JobStartedMessages = []*scaleset.JobStarted{{
					RunnerName: "runner", JobMessageBase: scaleset.JobMessageBase{JobID: "job"},
				}}
				message.JobCompletedMessages = []*scaleset.JobCompleted{{}}
			}
			if tc.noStats {
				message.Statistics = nil
			}
			if tc.nilMessage {
				w.latestStatistics = message.Statistics
				message = nil
			}

			err = w.Scale(t.Context(), message)
			require.Equal(t, tc.wantEvents, recorder.events)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				if tc.acquireErr || tc.noStats {
					require.Empty(t, patches)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.jobs, acquisitions == 1)
				if tc.jobs {
					require.Equal(t, []string{
						"/apis/actions.github.com/v1alpha1/namespaces/default/ephemeralrunners/runner/status",
						"/apis/actions.github.com/v1alpha1/namespaces/default/ephemeralrunnersets/set",
					}, patches)
					require.False(t, w.dirty)
				} else {
					require.Len(t, patches, 1)
				}
			}
		})
	}
}
