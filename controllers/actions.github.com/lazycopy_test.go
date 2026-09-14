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

// Pins the hazard documented on lazyCopy: the caller keeps the pointer it
// handed to newLazyCopy, so it can write through it without going via Mutate.
// A write that lands before the first Mutate is already in the snapshot, so it
// is absent from the patch and never reaches the API server. The type cannot
// prevent this, which is why the ordering is a caller invariant rather than a
// guarantee.
func TestLazyCopyDropsWritesMadeBeforeTheFirstMutate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "pod",
			Annotations: map[string]string{"smuggled": "old", "declared": "old"},
		},
	}
	lazy := newLazyCopy(pod)

	// Bypasses Mutate, so it is captured by the snapshot taken below.
	pod.Annotations["smuggled"] = "new"

	lazy.Mutate().Annotations["declared"] = "new"

	data, err := lazy.MergeFrom().Data(pod)
	require.NoError(t, err)
	assert.JSONEq(t, `{"metadata":{"annotations":{"declared":"new"}}}`, string(data))
}

// The status patch in patchAppliedActionableRevisionStatus relies on being able
// to attach an optimistic lock, so that a patch computed from a stale read is
// rejected by the API server rather than silently moving the applied revision
// backwards. Without the precondition the patch can never conflict, so the
// surrounding RetryOnConflict would never fire.
func TestLazyCopyMergeFromForwardsMergeOptions(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "pod",
			ResourceVersion: "42",
		},
	}
	lazy := newLazyCopy(pod)
	lazy.Mutate().Labels = map[string]string{"key": "value"}

	data, err := lazy.MergeFrom(client.MergeFromWithOptimisticLock{}).Data(pod)
	require.NoError(t, err)
	assert.JSONEq(
		t,
		`{"metadata":{"labels":{"key":"value"},"resourceVersion":"42"}}`,
		string(data),
		"the lock is carried as a resourceVersion precondition in the patch body, taken from the snapshot rather than the mutated object",
	)
}
