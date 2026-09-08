package actionsgithubcom

import (
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

// ephemeralRunnerSetActionableSpecChanged reports whether the runner spec the
// EphemeralRunnerSet is running differs from the one derived from the
// AutoscalingRunnerSet, in a way that requires re-applying it to the runners.
//
// Semantic.DeepEqual is used rather than cmp.Equal or reflect.DeepEqual because
// it treats a nil slice/map as equal to an empty one. That matters here: most
// PodSpec collection fields carry omitempty, so a template containing an
// explicitly empty value (`env: []`) is dropped when the EphemeralRunnerSet is
// written and reads back as nil. A strict comparison would report drift on every
// single reconcile, bumping ActionableRevision each time and making the
// EphemeralRunnerSet controller delete every idle and pending runner, forever.
// Semantic also knows how to compare resource.Quantity, and unlike cmp.Equal it
// cannot panic on types with unexported fields.
func ephemeralRunnerSetActionableSpecChanged(current, desired *v1alpha1.EphemeralRunnerSet) bool {
	if current == nil || desired == nil {
		return current != desired
	}

	return !apiequality.Semantic.DeepEqual(current.Spec.EphemeralRunnerSpec, desired.Spec.EphemeralRunnerSpec)
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

// listenerPodSpecRequiresRecreation reports whether the live listener pod must be
// deleted and rebuilt to match the desired spec.
//
// DeepDerivative, not DeepEqual: the live pod carries a large number of fields
// the desired pod never sets, written by the API server and by admission
// (nodeName, dnsPolicy, schedulerName, securityContext, enableServiceLinks,
// the default tolerations, the kube-api-access-* projected volume and its mount,
// terminationMessagePath, imagePullPolicy, secret defaultMode, ...). DeepEqual
// would therefore report drift on every reconcile of every healthy listener and
// spin in a delete/create loop. It cannot be made to work by pre-populating the
// defaults either, since nodeName is scheduler-assigned and the access-token
// volume has a generated name. See TestListenerPodSpecRequiresRecreation.
//
// The cost of DeepDerivative is that it ignores empty values on the desired side,
// so a field being *removed* is invisible to it. For everything sourced from the
// user-facing template that is harmless: the AutoscalingRunnerSet controller
// compares the whole AutoscalingListener spec with cmp.Equal and deletes the
// listener outright, which takes the pod with it. Container ports are the
// exception, because they come from the --listener-metrics-addr controller flag
// rather than from any resource, so disabling metrics would otherwise leave the
// port on the pod forever. Comparing port length is enough: additions and value
// changes are already caught by DeepDerivative, only removal is blind. The
// contents are deliberately not compared, because the API server defaults
// protocol to TCP and that would reintroduce the delete/create loop.
func listenerPodSpecRequiresRecreation(current, desired *corev1.Pod) bool {
	if current == nil || desired == nil {
		return current != desired
	}

	if listenerContainerPortsRemoved(current, desired) {
		return true
	}

	return !apiequality.Semantic.DeepDerivative(desired.Spec, current.Spec)
}

func listenerContainerPortsRemoved(current, desired *corev1.Pod) bool {
	for i := range desired.Spec.Containers {
		desiredContainer := &desired.Spec.Containers[i]
		currentContainer := findContainerByName(current.Spec.Containers, desiredContainer.Name)
		if currentContainer == nil {
			// A container the live pod does not have at all is drift that
			// DeepDerivative already reports; nothing to decide here.
			continue
		}
		if len(desiredContainer.Ports) < len(currentContainer.Ports) {
			return true
		}
	}
	return false
}

func findContainerByName(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}
