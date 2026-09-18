package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// The safe-to-evict annotation exists so the cluster autoscaler can drain a node
// that holds nothing but idle runners. The value therefore has to follow job
// assignment, which is the listener's write of Status.JobID, and it has to stay
// out of the way of a template that states its own preference.
func TestSafeToEvictFor(t *testing.T) {
	withJob := func(runner *v1alpha1.EphemeralRunner) {
		runner.Status.JobID = "job-1"
	}
	withRunnerAnnotation := func(runner *v1alpha1.EphemeralRunner) {
		runner.Annotations = map[string]string{AnnotationKeyClusterAutoscalerSafeToEvict: "false"}
	}
	withTemplateAnnotation := func(runner *v1alpha1.EphemeralRunner) {
		runner.Spec.Annotations = map[string]string{AnnotationKeyClusterAutoscalerSafeToEvict: "false"}
	}

	tt := []struct {
		name        string
		manage      bool
		mutate      func(*v1alpha1.EphemeralRunner)
		wantValue   string
		wantManaged bool
	}{
		{name: "disabled leaves the annotation alone", manage: false, wantManaged: false},
		{name: "idle runner is safe to evict", manage: true, wantValue: "true", wantManaged: true},
		{name: "runner with a job is not safe to evict", manage: true, mutate: withJob, wantValue: "false", wantManaged: true},
		{name: "explicit annotation on the runner wins", manage: true, mutate: withRunnerAnnotation, wantManaged: false},
		{name: "explicit annotation on the pod template wins", manage: true, mutate: withTemplateAnnotation, wantManaged: false},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			runner := &v1alpha1.EphemeralRunner{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "runner"},
			}
			if tc.mutate != nil {
				tc.mutate(runner)
			}

			r := &EphemeralRunnerReconciler{ManageSafeToEvictAnnotation: tc.manage}
			value, managed := r.safeToEvictFor(runner)

			assert.Equal(t, tc.wantManaged, managed)
			assert.Equal(t, tc.wantValue, value)
		})
	}
}

// A runner that never picks up a job must not generate a patch on every
// reconcile, and a runner that is on its way out must not be patched at all:
// the annotation only matters while the pod is a live eviction candidate.
func TestReconcileSafeToEvictAnnotation(t *testing.T) {
	key := types.NamespacedName{Namespace: "default", Name: "runner"}

	tt := []struct {
		name           string
		manage         bool
		jobID          string
		podAnnotations map[string]string
		deleting       bool
		wantPatches    int
		wantValue      string
	}{
		{
			name:        "idle runner without the annotation is annotated",
			manage:      true,
			wantPatches: 1,
			wantValue:   "true",
		},
		{
			name:           "runner that just got a job flips to false",
			manage:         true,
			jobID:          "job-1",
			podAnnotations: map[string]string{AnnotationKeyClusterAutoscalerSafeToEvict: "true"},
			wantPatches:    1,
			wantValue:      "false",
		},
		{
			name:           "annotation already at the desired value is not patched",
			manage:         true,
			podAnnotations: map[string]string{AnnotationKeyClusterAutoscalerSafeToEvict: "true"},
			wantPatches:    0,
			wantValue:      "true",
		},
		{
			name:        "disabled controller does not touch the pod",
			manage:      false,
			wantPatches: 0,
		},
		{
			name:        "pod on its way out is not patched",
			manage:      true,
			deleting:    true,
			wantPatches: 0,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, clientgoscheme.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   key.Namespace,
					Name:        key.Name,
					Annotations: tc.podAnnotations,
					Finalizers:  []string{"test.actions.github.com/hold"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName, Image: runnerImage}},
				},
			}

			var patches int
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						patches++
						return c.Patch(ctx, obj, patch, opts...)
					},
				}).
				Build()

			if tc.deleting {
				require.NoError(t, fakeClient.Delete(context.Background(), pod))
				require.NoError(t, fakeClient.Get(context.Background(), key, pod))
				require.False(t, pod.DeletionTimestamp.IsZero())
			}

			runner := &v1alpha1.EphemeralRunner{
				ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
				Status:     v1alpha1.EphemeralRunnerStatus{JobID: tc.jobID},
			}

			r := &EphemeralRunnerReconciler{
				Client:                      fakeClient,
				ManageSafeToEvictAnnotation: tc.manage,
			}

			require.NoError(t, r.reconcileSafeToEvictAnnotation(context.Background(), runner, pod, logr.Discard()))
			assert.Equal(t, tc.wantPatches, patches)

			var got corev1.Pod
			require.NoError(t, fakeClient.Get(context.Background(), key, &got))
			assert.Equal(t, tc.wantValue, got.Annotations[AnnotationKeyClusterAutoscalerSafeToEvict])
		})
	}
}
