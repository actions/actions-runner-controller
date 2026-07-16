package actionsgithubcom

import (
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	_ = ephemeralRunnerSetActionableSpecChanged
	_ = nextActionableRevision
	_ = listenerPodCanonicalEqual
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

func listenerPodCanonicalForComparison(pod *corev1.Pod) *corev1.Pod {
	if pod == nil {
		return nil
	}

	canonical := pod.DeepCopy()
	canonical.UID = ""
	canonical.ResourceVersion = ""
	canonical.ManagedFields = nil
	canonical.CreationTimestamp = metav1.Time{}
	canonical.DeletionTimestamp = nil
	canonical.Finalizers = nil
	canonical.Generation = 0
	canonical.Status = corev1.PodStatus{}

	return canonical
}

func listenerPodCanonicalEqual(current, desired *corev1.Pod) bool {
	return cmp.Equal(listenerPodCanonicalForComparison(current), listenerPodCanonicalForComparison(desired))
}

func listenerPodSpecRequiresRecreation(current, desired *corev1.Pod) bool {
	if current == nil || desired == nil {
		return current != desired
	}

	return !apiequality.Semantic.DeepDerivative(desired.Spec, current.Spec)
}
