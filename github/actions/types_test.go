package actions

import (
	"testing"
)

func TestJobMessageBase_GetJobName(t *testing.T) {
	tests := []struct {
		name            string
		jobDisplayName  string
		jobName         string
		expectedJobName string
		description     string
	}{
		{
			name:            "Prefers JobDisplayName when both are set",
			jobDisplayName:  "Build and Test",
			jobName:         "build-test",
			expectedJobName: "Build and Test",
			description:     "When both fields are populated, JobDisplayName should be preferred",
		},
		{
			name:            "Falls back to JobName when JobDisplayName is empty",
			jobDisplayName:  "",
			jobName:         "build-test",
			expectedJobName: "build-test",
			description:     "When JobDisplayName is empty, should fall back to JobName",
		},
		{
			name:            "Returns empty string when both are empty",
			jobDisplayName:  "",
			jobName:         "",
			expectedJobName: "",
			description:     "When both fields are empty, should return empty string",
		},
		{
			name:            "Uses JobDisplayName when JobName is empty",
			jobDisplayName:  "Integration Tests",
			jobName:         "",
			expectedJobName: "Integration Tests",
			description:     "When only JobDisplayName is set, should use it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobBase := &JobMessageBase{
				JobDisplayName: tt.jobDisplayName,
				JobName:        tt.jobName,
			}

			result := jobBase.GetJobName()

			if result != tt.expectedJobName {
				t.Errorf("GetJobName() = %q, want %q. %s", result, tt.expectedJobName, tt.description)
			}
		})
	}
}
