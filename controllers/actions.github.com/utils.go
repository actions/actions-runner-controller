package actionsgithubcom

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/util/rand"
)

func FilterLabels(labels map[string]string, filter string) map[string]string {
	filtered := map[string]string{}

	for k, v := range labels {
		if k != filter {
			filtered[k] = v
		}
	}

	return filtered
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz1234567890")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func mustJSON(v any) string {
	val, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(val)
}
