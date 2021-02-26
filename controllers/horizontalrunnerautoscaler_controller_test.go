package controllers

import (
	"github.com/google/go-cmp/cmp"
	actionsv1alpha1 "github.com/summerwind/actions-runner-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestGetValidCacheEntries(t *testing.T) {
	now := time.Now()

	hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
		Status: actionsv1alpha1.HorizontalRunnerAutoscalerStatus{
			CacheEntries: []actionsv1alpha1.CacheEntry{
				{
					Key:            "foo",
					Value:          1,
					ExpirationTime: metav1.Time{Time: now.Add(-time.Second)},
				},
				{
					Key:            "foo",
					Value:          2,
					ExpirationTime: metav1.Time{Time: now},
				},
				{
					Key:            "foo",
					Value:          3,
					ExpirationTime: metav1.Time{Time: now.Add(time.Second)},
				},
			},
		},
	}

	revs := getValidCacheEntries(hra, now)

	counts := map[string]int{}

	for _, r := range revs {
		counts[r.Key] += r.Value
	}

	want := map[string]int{"foo": 3}

	if d := cmp.Diff(want, counts); d != "" {
		t.Errorf("%s", d)
	}
}
