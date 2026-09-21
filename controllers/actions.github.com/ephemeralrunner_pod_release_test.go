package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func terminatedRunnerPod(exitCode int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-runner", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode}},
				},
			},
		},
	}
}

// TestDeletePodOptionsSkipsGracePeriodOnlyWhenNothingIsRunning pins which pods
// are deleted without a grace period.
//
// A pod that is done holds its name, its place on the node and its share of any
// ResourceQuota until the API server stops waiting on the kubelet, which is
// what leaves a burst of finished jobs sitting in Terminating while the runners
// replacing them have nowhere to start. Skipping the wait is only safe once
// every container has stopped, so the cases below cover both the pod phase and
// the container states the phase lags behind, including the sidecars that
// outlive the runner container and the lost node whose phase says the pod
// failed while the kubelet's last word was that the runner is still up.
func TestDeletePodOptionsSkipsGracePeriodOnlyWhenNothingIsRunning(t *testing.T) {
	sidecarRunning := terminatedRunnerPod(0)
	sidecarRunning.Spec.Containers = append(sidecarRunning.Spec.Containers, corev1.Container{Name: "dind"})
	sidecarRunning.Status.ContainerStatuses = append(sidecarRunning.Status.ContainerStatuses, corev1.ContainerStatus{
		Name:  "dind",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})

	nativeSidecarRunning := terminatedRunnerPod(0)
	nativeSidecarRunning.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{Name: "dind", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}

	statusNotReportedYet := terminatedRunnerPod(0)
	statusNotReportedYet.Spec.Containers = append(statusNotReportedYet.Spec.Containers, corev1.Container{Name: "dind"})

	runnerStillRunning := terminatedRunnerPod(0)
	runnerStillRunning.Status.ContainerStatuses[0].State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}

	succeeded := terminatedRunnerPod(0)
	succeeded.Status.Phase = corev1.PodSucceeded

	failed := terminatedRunnerPod(1)
	failed.Status.Phase = corev1.PodFailed

	nodeLost := terminatedRunnerPod(0)
	nodeLost.Status.Phase = corev1.PodFailed
	nodeLost.Status.ContainerStatuses[0].State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}

	failedBeforeStarting := terminatedRunnerPod(0)
	failedBeforeStarting.Status.Phase = corev1.PodFailed
	failedBeforeStarting.Status.ContainerStatuses = nil
	failedBeforeStarting.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{Name: "init", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
	}

	tt := map[string]struct {
		pod       *corev1.Pod
		immediate bool
	}{
		"pod succeeded": {pod: succeeded, immediate: true},
		"pod failed":    {pod: failed, immediate: true},
		"runner exited but the phase has not moved":                             {pod: terminatedRunnerPod(0), immediate: true},
		"pod failed before its containers started":                              {pod: failedBeforeStarting, immediate: true},
		"runner is still running":                                               {pod: runnerStillRunning},
		"sidecar is still running":                                              {pod: sidecarRunning},
		"native sidecar is still running":                                       {pod: nativeSidecarRunning},
		"a container has not reported a status yet":                             {pod: statusNotReportedYet},
		"the phase failed the pod but a container is still reported as running": {pod: nodeLost},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			r := &EphemeralRunnerReconciler{}
			opts := r.deletePodOptions(tc.pod)

			var deleteOptions client.DeleteOptions
			for _, opt := range opts {
				opt.ApplyToDelete(&deleteOptions)
			}

			if !tc.immediate {
				assert.Empty(t, opts)
				assert.Nil(t, deleteOptions.GracePeriodSeconds)
				return
			}

			require.NotNil(t, deleteOptions.GracePeriodSeconds)
			assert.Equal(t, int64(0), *deleteOptions.GracePeriodSeconds)
		})
	}
}

// TestDeletePodOptionsHonorsTheConfiguredGracePeriod pins the escape hatch for
// clusters that want finished pods to stick around.
//
// Removing a pod the moment its job is over is what hands the cluster back to
// the runners waiting for it, but it also takes the pod away from anything that
// reads it afterwards, such as a log collector scraping from the API. The grace
// period is configurable for those, and a negative value hands the decision
// back to the pod, which is how the deletion behaved before it was skipped.
func TestDeletePodOptionsHonorsTheConfiguredGracePeriod(t *testing.T) {
	pod := terminatedRunnerPod(0)

	t.Run("a configured grace period is applied to a finished pod", func(t *testing.T) {
		r := &EphemeralRunnerReconciler{TerminatedPodGracePeriodSeconds: 30}

		var deleteOptions client.DeleteOptions
		for _, opt := range r.deletePodOptions(pod) {
			opt.ApplyToDelete(&deleteOptions)
		}

		require.NotNil(t, deleteOptions.GracePeriodSeconds)
		assert.Equal(t, int64(30), *deleteOptions.GracePeriodSeconds)
	})

	t.Run("a negative grace period leaves the deletion alone", func(t *testing.T) {
		r := &EphemeralRunnerReconciler{TerminatedPodGracePeriodSeconds: -1}

		assert.Empty(t, r.deletePodOptions(pod))
	})
}

// TestReconcileReleasesTheRunnerPodAsSoonAsTheJobIsDone pins when, and how, the
// pod of a finished runner goes away.
//
// The finalizer deletes the pod as well, but only on the reconcile that follows
// the deletion of the EphemeralRunner. Until then the pod is still occupying the
// cluster, so a scale set draining a burst of jobs spends a queue round trip per
// runner before the next one can be scheduled. The delete also has to skip the
// grace period, or the pod lingers in Terminating for the full
// terminationGracePeriodSeconds with nothing left inside it to shut down.
func TestReconcileReleasesTheRunnerPodAsSoonAsTheJobIsDone(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-runner",
			Namespace:  "default",
			Finalizers: []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName},
		},
		Spec: v1alpha1.EphemeralRunnerSpec{
			GitHubConfigURL: "https://github.com/org/repo",
			PodTemplateSpec: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName}},
				},
			},
		},
		Status: v1alpha1.EphemeralRunnerStatus{
			Phase:      v1alpha1.EphemeralRunnerPhaseRunning,
			RunnerID:   42,
			RunnerName: "test-runner",
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-runner", Namespace: "default"},
		Data:       map[string][]byte{"runnerId": []byte("42"), "runnerName": []byte("test-runner")},
	}

	var podDeleteOptions []client.DeleteOptions
	c := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runner, secret, terminatedRunnerPod(0)).
		WithStatusSubresource(&v1alpha1.EphemeralRunner{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*corev1.Pod); ok {
					var applied client.DeleteOptions
					for _, opt := range opts {
						opt.ApplyToDelete(&applied)
					}
					podDeleteOptions = append(podDeleteOptions, applied)
				}
				return c.Delete(ctx, obj, opts...)
			},
		}).
		Build()

	reconciler := &EphemeralRunnerReconciler{
		Client:          c,
		Scheme:          scheme,
		ResourceBuilder: ResourceBuilder{ResourceCache: newTestResourceCache()},
	}

	key := types.NamespacedName{Namespace: "default", Name: "test-runner"}
	_, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	require.Len(t, podDeleteOptions, 1, "the pod of a finished runner must be deleted by the reconcile that observes the exit")
	require.NotNil(t, podDeleteOptions[0].GracePeriodSeconds)
	assert.Equal(t, int64(0), *podDeleteOptions[0].GracePeriodSeconds)

	err = c.Get(t.Context(), key, new(corev1.Pod))
	assert.True(t, kerrors.IsNotFound(err), "the pod must be gone, got %v", err)

	var got v1alpha1.EphemeralRunner
	require.NoError(t, c.Get(t.Context(), key, &got), "the runner is deleted, but its finalizers keep it until the deletion reconcile runs")
	assert.False(t, got.DeletionTimestamp.IsZero(), "the runner must be deleted by the same reconcile")
	assert.ElementsMatch(t,
		[]string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName},
		got.Finalizers,
		"the deletion reconcile still owns the rest of the cleanup, and the set still has to see the runner going away",
	)
}

// TestAFailedPodReleaseStillDeletesTheRunner pins what happens when the early
// pod deletion does not work.
//
// Releasing the pod here is an optimisation, not the thing that makes the
// cleanup correct: the deletion reconcile deletes the pod as well. So a pod
// that cannot be deleted must not stop the runner from being deleted, and must
// not cost a reconcile retrying it, or a burst of finishing jobs would queue
// behind whatever is making the API server unhappy.
func TestAFailedPodReleaseStillDeletesTheRunner(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	runner := &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-runner",
			Namespace:  "default",
			Finalizers: []string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName},
		},
		Status: v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded, RunnerID: 42},
	}

	c := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runner, terminatedRunnerPod(0)).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*corev1.Pod); ok {
					return kerrors.NewServiceUnavailable("etcd is unhappy")
				}
				return c.Delete(ctx, obj, opts...)
			},
		}).
		Build()

	reconciler := &EphemeralRunnerReconciler{
		Client:          c,
		Scheme:          scheme,
		ResourceBuilder: ResourceBuilder{ResourceCache: newTestResourceCache()},
	}

	require.NoError(t, reconciler.releaseFinishedRunner(t.Context(), runner, terminatedRunnerPod(0), logr.Discard()))

	var got v1alpha1.EphemeralRunner
	require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "test-runner"}, &got))
	assert.False(t, got.DeletionTimestamp.IsZero(), "the runner must still be deleted")
	assert.ElementsMatch(t,
		[]string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName},
		got.Finalizers,
		"the deletion reconcile has to be left something to delete the pod with",
	)
}
