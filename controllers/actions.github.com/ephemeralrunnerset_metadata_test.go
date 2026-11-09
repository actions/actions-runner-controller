package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestScaleUpWithOptionalRunnerMetadata(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metadata *v1alpha1.ResourceMeta
	}{
		{name: "omitted"},
		{name: "empty", metadata: &v1alpha1.ResourceMeta{}},
		{name: "labels only", metadata: &v1alpha1.ResourceMeta{Labels: map[string]string{"example.com/custom": "label"}}},
		{name: "annotations only", metadata: &v1alpha1.ResourceMeta{Annotations: map[string]string{"example.com/custom": "annotation"}}},
		{name: "both", metadata: &v1alpha1.ResourceMeta{
			Labels:      map[string]string{"example.com/custom": "label"},
			Annotations: map[string]string{"example.com/custom": "annotation", AnnotationKeyPatchID: "999"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			set := &v1alpha1.EphemeralRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-set", Namespace: "test-ns", UID: "test-set",
					Finalizers: []string{EphemeralRunnerSetFinalizerName},
				},
				Spec: v1alpha1.EphemeralRunnerSetSpec{
					Replicas: 3, PatchID: 1,
					EphemeralRunnerMetadata: tt.metadata,
					EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
						GitHubConfigURL: "https://github.com/org/repo",
						PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "runner", Image: "runner:latest"}},
						}},
					},
				},
			}
			original := set.DeepCopy()
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
				WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}).
				WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
				Build()
			r := &EphemeralRunnerSetReconciler{
				Client: c, APIReader: c, Log: logr.Discard(), Scheme: scheme,
				ResourceBuilder: ResourceBuilder{Scheme: scheme, ResourceCache: newTestResourceCache()},
			}

			// Reconcile exercises the errgroup workers, whose panics cannot be
			// caught by controller-runtime's recovery around Reconcile itself.
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
			require.NoError(t, err)
			var runners v1alpha1.EphemeralRunnerList
			require.NoError(t, c.List(t.Context(), &runners))
			require.Len(t, runners.Items, set.Spec.Replicas)
			for _, runner := range runners.Items {
				assert.Equal(t, "1", runner.Annotations[AnnotationKeyPatchID])
				assert.Equal(t, "0", runner.Annotations[AnnotationKeyActionableRevision])
				require.NotNil(t, metav1.GetControllerOf(&runner))
				assert.Equal(t, set.UID, metav1.GetControllerOf(&runner).UID)
				if tt.metadata != nil {
					for key, value := range tt.metadata.Labels {
						assert.Equal(t, value, runner.Labels[key])
					}
					assert.Equal(t, tt.metadata.Annotations["example.com/custom"], runner.Annotations["example.com/custom"])
				}
			}
			assert.Equal(t, original.Spec, set.Spec)
			assert.Equal(t, original.Labels, set.Labels)
			assert.Equal(t, original.Annotations, set.Annotations)
		})
	}
}
