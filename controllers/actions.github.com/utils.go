package actionsgithubcom

import (
	"k8s.io/apimachinery/pkg/util/rand"
	"os"
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

func ShouldSkipRbac() bool {
	return os.Getenv(SkipRbacSetupForController) == "true"
}

func ShouldSkipListenerRbacSetup() bool {
	return os.Getenv(SkipRbacSetupForListeners) == "true"
}

func ShouldSkipListenerSaCreation() bool {
	return os.Getenv(RequireListenerSAProvided) == "true"
}
