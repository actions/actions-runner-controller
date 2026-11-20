package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// A cache miss can reach the create path twice. Unlike runners, runner sets
// have a fixed name, so the API server prevents a second child from being made.
func TestAutoscalingRunnerSetRepeatedChildCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &AutoscalingRunnerSetReconciler{
		Client: c, Log: logr.Discard(), Scheme: scheme,
		ResourceBuilder: ResourceBuilder{Scheme: scheme, ResourceCache: newTestResourceCache()},
	}
	set := &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-set", Namespace: "test-ns", UID: "test-set",
			Annotations: map[string]string{runnerScaleSetIDAnnotationKey: "1"},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{GitHubConfigUrl: "https://github.com/org/repo"},
	}
	_, err := r.createEphemeralRunnerSet(t.Context(), set, logr.Discard())
	require.NoError(t, err)
	_, err = r.createEphemeralRunnerSet(t.Context(), set, logr.Discard())
	require.True(t, kerrors.IsAlreadyExists(err), "a repeated create must use the same name")
	var children v1alpha1.EphemeralRunnerSetList
	require.NoError(t, c.List(t.Context(), &children))
	require.Len(t, children.Items, 1)
	require.Equal(t, set.Name, children.Items[0].Name)
}
