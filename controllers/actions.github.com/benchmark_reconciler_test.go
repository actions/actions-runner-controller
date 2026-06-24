package actionsgithubcom

import (
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newBenchmarkEphemeralRunnerSetReconciler(fakeClient client.Client, scheme *runtime.Scheme) *EphemeralRunnerSetReconciler {
	return &EphemeralRunnerSetReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: &ResourceBuilder{Scheme: scheme},
	}
}
