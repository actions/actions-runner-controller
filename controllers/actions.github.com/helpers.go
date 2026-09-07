package actionsgithubcom

import (
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

var (
	_ = ephemeralRunnerSetActionableSpecChanged
	_ = nextActionableRevision
)

func ephemeralRunnerSetActionableSpecChanged(current, desired *v1alpha1.EphemeralRunnerSet) bool {
	if current == nil || desired == nil {
		return current != desired
	}

	return !cmp.Equal(current.Spec.EphemeralRunnerSpec, desired.Spec.EphemeralRunnerSpec)
}

func nextActionableRevision(current *v1alpha1.EphemeralRunnerSet) int64 {
	if current == nil {
		return 1
	}

	if current.Spec.ActionableRevision > current.Status.AppliedActionableRevision {
		return current.Spec.ActionableRevision + 1
	}

	return current.Status.AppliedActionableRevision + 1
}

func listenerPodSpecRequiresRecreation(current, desired *corev1.Pod) bool {
	if current == nil || desired == nil {
		return current != desired
	}

	return !apiequality.Semantic.DeepDerivative(desired.Spec, current.Spec)
}
