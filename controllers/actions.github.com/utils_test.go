package actionsgithubcom

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/rand"
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

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz1234567890")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
