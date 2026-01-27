package metrics

import (
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
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
		jobBase    actions.JobMessageBase
		wantName   string
		wantTarget string
	}{
		{
			name: "main branch workflow",
			jobBase: actions.JobMessageBase{
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
			jobBase: actions.JobMessageBase{
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
			jobBase: actions.JobMessageBase{
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
			jobBase: actions.JobMessageBase{
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
