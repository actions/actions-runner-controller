package actionsgithubcom

import (
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

// listenerPodSpecRequiresRecreation reports whether the live listener pod must be
// deleted and rebuilt to match the desired spec.
//
// The config secret is checked first. The pod mounts it as a volume and the
// listener parses it once at startup, so a change to the scale set URL, the TLS
// certificate, the metrics configuration or the scaler tuning only reaches the
// listener after a restart. None of that is visible in the pod spec, which
// references the secret by name and is byte-identical before and after, so the
// desired pod carries the secret's resource version as an annotation and drift
// is detected by comparing it.
//
// A pod that predates this annotation counts as drift and is recreated once, on
// the first reconcile after the controller is upgraded. Ignoring it instead is
// tempting, to avoid that rollout, but it loses updates: the reconcile that
// declines to recreate the pod goes on to patch the desired annotations onto it,
// so a config change that landed while the old controller was running is
// recorded as already applied and the listener keeps serving the configuration
// it parsed at startup, with nothing to ever correct it. The rollout is cheap by
// comparison - deleting a listener does not disturb the runners it started, and
// an upgrade normally changes the listener image and so recreates the pod
// anyway.
//
// The resource version also moves when only the secret's labels or annotations
// change, which does not affect the listener. That is accepted rather than
// worked around by hashing the secret data: those fields come from the
// AutoscalingListener spec, and a change to that spec already makes the
// AutoscalingRunnerSet controller replace the listener wholesale.
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

	if listenerConfigChanged(current, desired) {
		return true
	}

	if listenerContainerPortsRemoved(current, desired) {
		return true
	}

	return !apiequality.Semantic.DeepDerivative(desired.Spec, current.Spec)
}

func listenerConfigChanged(current, desired *corev1.Pod) bool {
	desiredVersion := desired.Annotations[AnnotationKeyListenerConfigResourceVersion]
	if desiredVersion == "" {
		// Nothing to compare against, so there is nothing this check can say.
		// The desired pod is always built from a secret read back from the API
		// server, so this only happens in tests.
		return false
	}

	return current.Annotations[AnnotationKeyListenerConfigResourceVersion] != desiredVersion
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
