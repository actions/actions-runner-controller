package controllers

import (
	"fmt"

	gogithub "github.com/google/go-github/v37/github"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunnerCache holds cached returns from github runner API calls
// - default time for cache is 60 * time.Seconds()
type RunnerCache struct {
	ExpirationTime metav1.Time
	Runners        []*gogithub.Runner
}

func (i *interface{}) test() {
	fmt.Println(i)
}

func filterLabels(labels map[string]string, filter string) map[string]string {
	filtered := map[string]string{}

	for k, v := range labels {
		if k != filter {
			filtered[k] = v
		}
	}

	return filtered
}
