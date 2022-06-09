package controllers

import (
	"reflect"
	"testing"
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
			if got := filterLabels(tt.args.labels, tt.args.filter); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}
