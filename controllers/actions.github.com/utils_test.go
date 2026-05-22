package actionsgithubcom

import (
	"reflect"
	"testing"

	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
)

func Test_filterLabels(t *testing.T) {
	type args struct {
		labels map[string]string
		filter string
	}
	tests := []struct {
		name string
		args args
		want map[string]string
	}{
		{
			name: "ok",
			args: args{
				labels: map[string]string{LabelKeyRunnerTemplateHash: "abc", LabelKeyPodTemplateHash: "def"},
				filter: LabelKeyRunnerTemplateHash,
			},
			want: map[string]string{LabelKeyPodTemplateHash: "def"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FilterLabels(tt.args.labels, tt.args.filter); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_buildRunnerScaleSetLabels(t *testing.T) {
	logger := logr.Discard()

	tests := []struct {
		name         string
		scaleSetName string
		specLabels   []string
		want         []scaleset.Label
	}{
		{
			name:         "name only, no extra labels",
			scaleSetName: "my-runner",
			specLabels:   nil,
			want: []scaleset.Label{
				{Name: "my-runner", Type: "System"},
			},
		},
		{
			name:         "name plus extra labels",
			scaleSetName: "my-runner",
			specLabels:   []string{"linux", "x64"},
			want: []scaleset.Label{
				{Name: "my-runner", Type: "System"},
				{Name: "linux", Type: "System"},
				{Name: "x64", Type: "System"},
			},
		},
		{
			name:         "deduplicates name from extra labels",
			scaleSetName: "my-runner",
			specLabels:   []string{"my-runner", "linux"},
			want: []scaleset.Label{
				{Name: "my-runner", Type: "System"},
				{Name: "linux", Type: "System"},
			},
		},
		{
			name:         "deduplicates within extra labels",
			scaleSetName: "my-runner",
			specLabels:   []string{"linux", "linux", "x64"},
			want: []scaleset.Label{
				{Name: "my-runner", Type: "System"},
				{Name: "linux", Type: "System"},
				{Name: "x64", Type: "System"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRunnerScaleSetLabels(tt.scaleSetName, tt.specLabels, logger)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildRunnerScaleSetLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_runnerScaleSetLabelsAnnotation(t *testing.T) {
	tests := []struct {
		name         string
		scaleSetName string
		specLabels   []string
		want         string
	}{
		{
			name:         "name only",
			scaleSetName: "my-runner",
			specLabels:   nil,
			want:         "my-runner",
		},
		{
			name:         "sorted output",
			scaleSetName: "my-runner",
			specLabels:   []string{"x64", "linux"},
			want:         "linux,my-runner,x64",
		},
		{
			name:         "deduplicates",
			scaleSetName: "my-runner",
			specLabels:   []string{"my-runner", "linux"},
			want:         "linux,my-runner",
		},
		{
			name:         "stable regardless of input order",
			scaleSetName: "my-runner",
			specLabels:   []string{"c", "a", "b"},
			want:         "a,b,c,my-runner",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runnerScaleSetLabelsAnnotation(tt.scaleSetName, tt.specLabels)
			if got != tt.want {
				t.Errorf("runnerScaleSetLabelsAnnotation() = %q, want %q", got, tt.want)
			}
		})
	}
}
