package metrics

import (
	"testing"

	"github.com/actions/scaleset"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsWithWorkflowRefParsing(t *testing.T) {
	// Create a test exporter
	exporter := &exporter{
		scaleSetLabels: prometheus.Labels{
			labelKeyEnterprise:              "test-enterprise",
			labelKeyOrganization:            "test-org",
			labelKeyRepository:              "test-repo",
			labelKeyRunnerScaleSetName:      "test-scale-set",
			labelKeyRunnerScaleSetNamespace: "test-namespace",
		},
	}

	tests := []struct {
		name       string
		jobBase    scaleset.JobMessageBase
		wantName   string
		wantTarget string
	}{
		{
			name: "main branch workflow",
			jobBase: scaleset.JobMessageBase{
				OwnerName:      "actions",
				RepositoryName: "runner",
				JobDisplayName: "Build and Test",
				JobWorkflowRef: "actions/runner/.github/workflows/build.yml@refs/heads/main",
				EventName:      "push",
			},
			wantName:   "build",
			wantTarget: "heads/main",
		},
		{
			name: "feature branch workflow",
			jobBase: scaleset.JobMessageBase{
				OwnerName:      "myorg",
				RepositoryName: "myrepo",
				JobDisplayName: "CI/CD Pipeline",
				JobWorkflowRef: "myorg/myrepo/.github/workflows/ci-cd-pipeline.yml@refs/heads/feature/new-metrics",
				EventName:      "push",
			},
			wantName:   "ci-cd-pipeline",
			wantTarget: "heads/feature/new-metrics",
		},
		{
			name: "pull request workflow",
			jobBase: scaleset.JobMessageBase{
				OwnerName:      "actions",
				RepositoryName: "runner",
				JobDisplayName: "PR Checks",
				JobWorkflowRef: "actions/runner/.github/workflows/pr-checks.yml@refs/pull/123/merge",
				EventName:      "pull_request",
			},
			wantName:   "pr-checks",
			wantTarget: "pull/123",
		},
		{
			name: "tag workflow",
			jobBase: scaleset.JobMessageBase{
				OwnerName:      "actions",
				RepositoryName: "runner",
				JobDisplayName: "Release",
				JobWorkflowRef: "actions/runner/.github/workflows/release.yml@refs/tags/v1.2.3",
				EventName:      "release",
			},
			wantName:   "release",
			wantTarget: "tags/v1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := exporter.jobLabels(&tt.jobBase)

			// Build expected labels
			expectedLabels := prometheus.Labels{
				labelKeyEnterprise:        "test-enterprise",
				labelKeyOrganization:      tt.jobBase.OwnerName,
				labelKeyRepository:        tt.jobBase.RepositoryName,
				labelKeyJobName:           tt.jobBase.JobDisplayName,
				labelKeyJobWorkflowRef:    tt.jobBase.JobWorkflowRef,
				labelKeyJobWorkflowName:   tt.wantName,
				labelKeyJobWorkflowTarget: tt.wantTarget,
				labelKeyEventName:         tt.jobBase.EventName,
			}

			// Assert all expected labels match
			assert.Equal(t, expectedLabels, labels, "jobLabels() returned unexpected labels for %s", tt.name)
		})
	}
}

func TestRecordStatisticsExposesAllFields(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := installMetrics(defaultMetrics, reg, discardLogger)

	exp := &exporter{
		scaleSetLabels: prometheus.Labels{
			labelKeyRunnerScaleSetName:      "test-scale-set",
			labelKeyRunnerScaleSetNamespace: "test-namespace",
			labelKeyEnterprise:              "",
			labelKeyOrganization:            "test-org",
			labelKeyRepository:              "test-repo",
		},
		metrics: m,
	}

	stats := &scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs:     5,
		TotalAcquiredJobs:      3,
		TotalAssignedJobs:      10,
		TotalRunningJobs:       7,
		TotalRegisteredRunners: 12,
		TotalBusyRunners:       7,
		TotalIdleRunners:       5,
	}

	exp.RecordStatistics(stats)

	gathered, err := reg.Gather()
	require.NoError(t, err)

	metricsByName := make(map[string]*io_prometheus_client.MetricFamily, len(gathered))
	for _, mf := range gathered {
		metricsByName[mf.GetName()] = mf
	}

	wantGauges := map[string]float64{
		"gha_available_jobs":     5,
		"gha_acquired_jobs":      3,
		"gha_assigned_jobs":      10,
		"gha_running_jobs":       7,
		"gha_registered_runners": 12,
		"gha_busy_runners":       7,
		"gha_idle_runners":       5,
	}

	for name, wantVal := range wantGauges {
		mf, ok := metricsByName[name]
		require.True(t, ok, "metric %q not found in gathered metrics", name)
		require.NotEmpty(t, mf.GetMetric(), "metric %q has no samples", name)
		assert.Equal(t, wantVal, mf.GetMetric()[0].GetGauge().GetValue(), "metric %q value mismatch", name)
	}
}
