package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCleanupResourcesForKnownTerminalPodDeletesPodAndSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runner",
			Namespace: "default",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runner.Name,
			Namespace: runner.Namespace,
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runner.Name,
			Namespace: runner.Namespace,
		},
	}

	reconciler := &EphemeralRunnerReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod, secret).Build(),
	}

	require.NoError(t, reconciler.cleanupResourcesForPod(ctx, runner, pod, logr.Discard()))

	key := types.NamespacedName{Namespace: runner.Namespace, Name: runner.Name}
	require.True(t, kerrors.IsNotFound(reconciler.Get(ctx, key, &corev1.Pod{})))
	require.True(t, kerrors.IsNotFound(reconciler.Get(ctx, key, &corev1.Secret{})))
}

func TestCleanupRunnerLinkedSecretsDeletesByRunnerPodLabel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runner",
			Namespace: "default",
		},
	}
	linkedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linked-secret",
			Namespace: runner.Namespace,
			Labels: map[string]string{
				"runner-pod": runner.Name,
			},
		},
	}
	unrelatedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unrelated-secret",
			Namespace: runner.Namespace,
			Labels: map[string]string{
				"runner-pod": "other-runner",
			},
		},
	}
	otherNamespaceSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linked-secret-other-namespace",
			Namespace: "other-namespace",
			Labels: map[string]string{
				"runner-pod": runner.Name,
			},
		},
	}

	reconciler := &EphemeralRunnerReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(linkedSecret, unrelatedSecret, otherNamespaceSecret).Build(),
	}

	require.NoError(t, reconciler.cleanupRunnerLinkedSecrets(ctx, runner, logr.Discard()))

	require.True(t, kerrors.IsNotFound(reconciler.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: linkedSecret.Name}, &corev1.Secret{})))
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: unrelatedSecret.Name}, &corev1.Secret{}))
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Namespace: otherNamespaceSecret.Namespace, Name: otherNamespaceSecret.Name}, &corev1.Secret{}))
}

func TestPodBecameCleanupCandidate(t *testing.T) {
	t.Parallel()

	pendingPod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}
	runningPod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	succeededPod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	failedPod := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}}

	require.False(t, podBecameCleanupCandidate(pendingPod, runningPod))
	require.True(t, podBecameCleanupCandidate(runningPod, succeededPod))
	require.True(t, podBecameCleanupCandidate(runningPod, failedPod))
	require.False(t, podBecameCleanupCandidate(succeededPod, failedPod))
}

func TestPrioritizedWorkQueueAddsTerminalPodPriority(t *testing.T) {
	t.Parallel()

	queue := priorityqueue.New[reconcile.Request]("test-prioritized-workqueue")
	defer queue.ShutDown()

	request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "test-runner"}}
	prioritizedWorkQueue{TypedRateLimitingInterface: queue, priority: terminalPodUpdatePriority}.Add(request)

	item, priority, shutdown := queue.GetWithPriority()
	defer queue.Done(item)

	require.False(t, shutdown)
	require.Equal(t, request, item)
	require.Equal(t, terminalPodUpdatePriority, priority)
}
