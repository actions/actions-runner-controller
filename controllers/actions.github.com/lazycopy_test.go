package actionsgithubcom

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLazyCopyDoesNotCopyUntilMutated(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod"}}
	lazy := newLazyCopy(pod)

	assert.False(t, lazy.Modified())
	assert.Panics(t, func() { lazy.MergeFrom() })
}

func TestLazyCopySnapshotsBeforeTheFirstMutation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pod",
			Annotations: map[string]string{"key": "old"},
		},
	}
	lazy := newLazyCopy(pod)

	lazy.Mutate().Annotations["key"] = "new"
	require.True(t, lazy.Modified())

	data, err := lazy.MergeFrom().Data(pod)
	require.NoError(t, err)
	assert.JSONEq(t, `{"metadata":{"annotations":{"key":"new"}}}`, string(data))
}

func TestLazyCopySnapshotsOnlyOnce(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pod",
			Annotations: map[string]string{"first": "old", "second": "old"},
		},
	}
	lazy := newLazyCopy(pod)

	lazy.Mutate().Annotations["first"] = "new"
	// The second mutation must diff against the state before the first one,
	// otherwise the earlier change is dropped from the patch.
	lazy.Mutate().Annotations["second"] = "new"

	data, err := lazy.MergeFrom().Data(pod)
	require.NoError(t, err)
	assert.JSONEq(t, `{"metadata":{"annotations":{"first":"new","second":"new"}}}`, string(data))
}

func TestLazyCopyMergeFromIsAMergePatch(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod"}}
	lazy := newLazyCopy(pod)
	lazy.Mutate().Labels = map[string]string{"key": "value"}

	assert.Equal(t, client.MergeFrom(pod).Type(), lazy.MergeFrom().Type())
}
