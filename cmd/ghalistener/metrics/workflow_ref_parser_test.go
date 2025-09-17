package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseWorkflowRef(t *testing.T) {
	tests := []struct {
		name        string
		workflowRef string
		wantName    string
		wantTarget  string
	}{
		{
			name:        "standard branch reference with yml",
			workflowRef: "actions-runner-controller-sandbox/mumoshu-orgrunner-test-01/.github/workflows/blank.yml@refs/heads/main",
			wantName:    "blank",
			wantTarget:  "heads/main",
		},
		{
			name:        "branch with special characters",
			workflowRef: "owner/repo/.github/workflows/ci-cd.yml@refs/heads/feature/new-feature",
			wantName:    "ci-cd",
			wantTarget:  "heads/feature/new-feature",
		},
		{
			name:        "yaml extension",
			workflowRef: "owner/repo/.github/workflows/deploy.yaml@refs/heads/develop",
			wantName:    "deploy",
			wantTarget:  "heads/develop",
		},
		{
			name:        "tag reference",
			workflowRef: "owner/repo/.github/workflows/release.yml@refs/tags/v1.0.0",
			wantName:    "release",
			wantTarget:  "tags/v1.0.0",
		},
		{
			name:        "pull request reference",
			workflowRef: "owner/repo/.github/workflows/test.yml@refs/pull/123/merge",
			wantName:    "test",
			wantTarget:  "pull/123",
		},
		{
			name:        "empty workflow ref",
			workflowRef: "",
			wantName:    "",
			wantTarget:  "",
		},
		{
			name:        "invalid format - no @ separator",
			workflowRef: "owner/repo/.github/workflows/test.yml",
			wantName:    "",
			wantTarget:  "",
		},
		{
			name:        "workflow with dots in name",
			workflowRef: "owner/repo/.github/workflows/build.test.yml@refs/heads/main",
			wantName:    "build.test",
			wantTarget:  "heads/main",
		},
		{
			name:        "workflow with hyphen and underscore",
			workflowRef: "owner/repo/.github/workflows/build-test_deploy.yml@refs/heads/main",
			wantName:    "build-test_deploy",
			wantTarget:  "heads/main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWorkflowRef(tt.workflowRef)
			expected := WorkflowRefInfo{
				Name:   tt.wantName,
				Target: tt.wantTarget,
			}
			assert.Equal(t, expected, got, "ParseWorkflowRef(%q) returned unexpected result", tt.workflowRef)
		})
	}
}
