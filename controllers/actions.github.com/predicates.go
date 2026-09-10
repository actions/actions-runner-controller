package actionsgithubcom

import (
	"slices"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// The predicates in this file exist purely to keep work out of the workqueue.
// They must never change what a reconciler does, so each of them is written as
// the projection of the fields its reconciler actually reads: an update is
// dropped only when every one of those fields is unchanged. Whenever a
// reconciler starts reading a new field, the matching projection below has to
// grow with it.
//
// Create, delete and generic events are always delivered. Only updates are
// considered here.

// autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate filters updates of the
// EphemeralRunnerSets owned by an AutoscalingRunnerSet.
//
// The AutoscalingRunnerSet reconciler reads the runner set's object metadata
// (labels, annotations, finalizers and deletion timestamp), its whole spec, and,
// through ephemeralRunnerSetOutdatedForAppliedRevision, Status.Phase together
// with Status.AppliedActionableRevision. It never reads
// Status.FinishedRunnerCleanupPatchID, which the EphemeralRunnerSet rewrites for
// every listener patch id and which is the noisiest part of the object.
func autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRunnerSet, oldOk := e.ObjectOld.(*v1alpha1.EphemeralRunnerSet)
			newRunnerSet, newOk := e.ObjectNew.(*v1alpha1.EphemeralRunnerSet)
			if !oldOk || !newOk {
				// Not the type we reasoned about, so we cannot claim the event
				// is irrelevant. Let it through.
				return true
			}

			if !equalReconciledObjectMeta(&oldRunnerSet.ObjectMeta, &newRunnerSet.ObjectMeta) ||
				!equality.Semantic.DeepEqual(&oldRunnerSet.Spec, &newRunnerSet.Spec) {
				return true
			}

			return oldRunnerSet.Status.Phase != newRunnerSet.Status.Phase ||
				oldRunnerSet.Status.AppliedActionableRevision != newRunnerSet.Status.AppliedActionableRevision
		},
	}
}

// ephemeralRunnerSetOwnedEphemeralRunnerPredicate filters updates of the
// EphemeralRunners owned by an EphemeralRunnerSet.
//
// Besides object metadata and spec, the EphemeralRunnerSet reconciler reads
// Status.Phase, to group runners by state, and Status.RunnerID, to decide
// whether a runner still has to be removed from the service. The rest of the
// runner status (readiness, failure bookkeeping, reason, message and the job
// details written by the listener) is never read, and it is by far the noisiest
// part of the object.
func ephemeralRunnerSetOwnedEphemeralRunnerPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRunner, oldOk := e.ObjectOld.(*v1alpha1.EphemeralRunner)
			newRunner, newOk := e.ObjectNew.(*v1alpha1.EphemeralRunner)
			if !oldOk || !newOk {
				return true
			}

			if !equalReconciledObjectMeta(&oldRunner.ObjectMeta, &newRunner.ObjectMeta) ||
				!equality.Semantic.DeepEqual(&oldRunner.Spec, &newRunner.Spec) {
				return true
			}

			return oldRunner.Status.Phase != newRunner.Status.Phase ||
				oldRunner.Status.RunnerID != newRunner.Status.RunnerID
		},
	}
}

// ephemeralRunnerOwnedPodPredicate filters updates of the pod owned by an
// EphemeralRunner.
//
// The EphemeralRunner reconciler reads the pod UID, its deletion timestamp, the
// pod phase, reason and message, the container and init container statuses, and
// the Ready condition. It never reads the pod spec or the remaining status
// fields, which is where most pod updates land: assigned IPs, the node the pod
// was scheduled on, start time and the conditions other than Ready.
func ephemeralRunnerOwnedPodPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPod, oldOk := e.ObjectOld.(*corev1.Pod)
			newPod, newOk := e.ObjectNew.(*corev1.Pod)
			if !oldOk || !newOk {
				return true
			}

			if oldPod.UID != newPod.UID ||
				!equalTime(oldPod.DeletionTimestamp, newPod.DeletionTimestamp) {
				return true
			}

			oldStatus, newStatus := &oldPod.Status, &newPod.Status
			if oldStatus.Phase != newStatus.Phase ||
				oldStatus.Reason != newStatus.Reason ||
				oldStatus.Message != newStatus.Message {
				return true
			}

			if !equality.Semantic.DeepEqual(oldStatus.ContainerStatuses, newStatus.ContainerStatuses) ||
				!equality.Semantic.DeepEqual(oldStatus.InitContainerStatuses, newStatus.InitContainerStatuses) {
				return true
			}

			return podReady(oldPod) != podReady(newPod)
		},
	}
}

// equalReconciledObjectMeta compares the metadata fields the controllers in this
// package branch on. Bookkeeping the API server owns, such as the resource
// version, the managed fields and the timestamps outside of deletion, is
// deliberately left out.
// podReady reports whether the pod advertises the Ready condition. It is the
// single source of truth for both the reconciler, which mirrors the result into
// EphemeralRunner.Status.Ready, and the pod predicate, which has to wake the
// reconciler whenever the result changes.
func podReady(pod *corev1.Pod) bool {
	var ready bool
	var lastTransitionTime time.Time
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.LastTransitionTime.After(lastTransitionTime) {
			ready = condition.Status == corev1.ConditionTrue
			lastTransitionTime = condition.LastTransitionTime.Time
		}
	}
	return ready
}

func equalReconciledObjectMeta(old, new *metav1.ObjectMeta) bool {
	return old.Generation == new.Generation &&
		equalTime(old.DeletionTimestamp, new.DeletionTimestamp) &&
		slices.Equal(old.Finalizers, new.Finalizers) &&
		equality.Semantic.DeepEqual(old.Labels, new.Labels) &&
		equality.Semantic.DeepEqual(old.Annotations, new.Annotations) &&
		equality.Semantic.DeepEqual(old.OwnerReferences, new.OwnerReferences)
}

func equalTime(old, new *metav1.Time) bool {
	switch {
	case old == nil && new == nil:
		return true
	case old == nil || new == nil:
		return false
	default:
		return old.Equal(new)
	}
}
