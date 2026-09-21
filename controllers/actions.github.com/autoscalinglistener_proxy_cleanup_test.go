package actionsgithubcom

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

// The proxy secret holds credentials and is owned by the listener, so deleting
// the listener collects it. A stopped listener is not deleted, which makes
// cleanupResources the only thing that will ever remove it.
//
// Its name is derived from the listener, not from the spec, so cleanup must not
// ask the spec whether it exists. The spec of a parked listener is updated in
// place as the AutoscalingRunnerSet is edited, so a user who removes the proxy
// while the scale set is switched off would otherwise erase the only signal that
// the old secret is there, and leave the credentials behind indefinitely.
func TestCleanupResourcesDeletesTheProxySecretWithoutAskingTheSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	const namespace = "arc-system"

	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{Name: "test-listener", Namespace: namespace},
		Spec: v1alpha1.AutoscalingListenerSpec{
			AutoscalingRunnerSetNamespace: "arc-runners",
			AutoscalingRunnerSetName:      "test-ars",
			Phase:                         v1alpha1.AutoscalingListenerPhaseStopped,
			// The proxy has been removed from the scale set since the secret was
			// created, and the parked listener has already taken that edit.
			Proxy: nil,
		},
	}
	proxySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyListenerSecretName(listener),
			Namespace: namespace,
		},
		Data: map[string][]byte{"password": []byte("secret")},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listener, proxySecret).
		Build()

	reconciler := &AutoscalingListenerReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Log:    logr.Discard(),
	}

	_, err := reconciler.cleanupResources(context.Background(), listener, logr.Discard())
	require.NoError(t, err)

	err = fakeClient.Get(
		context.Background(),
		types.NamespacedName{Name: proxySecret.Name, Namespace: namespace},
		new(corev1.Secret),
	)
	require.True(
		t,
		kerrors.IsNotFound(err),
		"the proxy secret must be removed with the rest of the listener's resources, whatever the current spec says about a proxy",
	)
}
