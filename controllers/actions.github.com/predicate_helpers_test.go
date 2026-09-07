package actionsgithubcom

import (
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestEphemeralRunnerPrimaryPredicate(t *testing.T) {
	g := gomega.NewWithT(t)
	predicate := ephemeralRunnerPrimaryPredicate()
	runner := &v1alpha1.EphemeralRunner{}

	g.Expect(predicate.Create(event.CreateEvent{Object: runner})).To(gomega.BeTrue())
	g.Expect(predicate.Delete(event.DeleteEvent{Object: runner})).To(gomega.BeTrue())

	oldRunner := &v1alpha1.EphemeralRunner{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
	newRunner := oldRunner.DeepCopy()
	newRunner.Generation = 2
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeTrue())

	newRunner = oldRunner.DeepCopy()
	newRunner.Finalizers = []string{ephemeralRunnerFinalizerName}
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeTrue())

	newRunner = oldRunner.DeepCopy()
	deletionTimestamp := metav1.NewTime(time.Now())
	newRunner.DeletionTimestamp = &deletionTimestamp
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeTrue())

	newRunner = oldRunner.DeepCopy()
	newRunner.Status.JobRequestID = 123
	newRunner.Status.JobID = "job-id"
	newRunner.Status.JobRepositoryName = "owner/repo"
	newRunner.Status.JobWorkflowRef = "owner/repo/.github/workflows/ci.yaml@refs/heads/main"
	newRunner.Status.WorkflowRunID = 456
	newRunner.Status.JobDisplayName = "build"
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeFalse())
}

func TestEphemeralRunnerSetPrimaryPredicate(t *testing.T) {
	g := gomega.NewWithT(t)
	predicate := ephemeralRunnerSetPrimaryPredicate()
	runnerSet := &v1alpha1.EphemeralRunnerSet{}

	g.Expect(predicate.Create(event.CreateEvent{Object: runnerSet})).To(gomega.BeTrue())
	g.Expect(predicate.Delete(event.DeleteEvent{Object: runnerSet})).To(gomega.BeTrue())

	oldRunnerSet := &v1alpha1.EphemeralRunnerSet{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
	newRunnerSet := oldRunnerSet.DeepCopy()
	newRunnerSet.Generation = 2
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeTrue())

	newRunnerSet = oldRunnerSet.DeepCopy()
	newRunnerSet.Status.Phase = v1alpha1.EphemeralRunnerSetPhaseRunning
	newRunnerSet.Status.AppliedActionableRevision = 2
	newRunnerSet.Status.FinishedRunnerCleanupPatchID = 3
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeFalse())
}

func TestEphemeralRunnerSetOwnedEphemeralRunnerPredicate(t *testing.T) {
	g := gomega.NewWithT(t)
	predicate := ephemeralRunnerSetOwnedEphemeralRunnerPredicate()
	oldRunner := &v1alpha1.EphemeralRunner{ObjectMeta: metav1.ObjectMeta{Generation: 1}}

	g.Expect(predicate.Create(event.CreateEvent{Object: oldRunner})).To(gomega.BeTrue())
	g.Expect(predicate.Delete(event.DeleteEvent{Object: oldRunner})).To(gomega.BeTrue())

	newRunner := oldRunner.DeepCopy()
	newRunner.Generation = 2
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeTrue())

	newRunner = oldRunner.DeepCopy()
	newRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeTrue())

	newRunner = oldRunner.DeepCopy()
	newRunner.Status.RunnerID = 123
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeTrue())

	newRunner = oldRunner.DeepCopy()
	newRunner.Status.JobRequestID = 123
	newRunner.Status.JobID = "job-id"
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunner, ObjectNew: newRunner})).To(gomega.BeFalse())
}

func TestAutoscalingRunnerSetOwnedEphemeralRunnerSetPredicate(t *testing.T) {
	g := gomega.NewWithT(t)
	predicate := autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate()
	oldRunnerSet := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Generation:      1,
			ResourceVersion: "1000",
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: 2,
			PatchID:  1,
		},
	}

	g.Expect(predicate.Create(event.CreateEvent{Object: oldRunnerSet})).To(gomega.BeTrue())
	g.Expect(predicate.Delete(event.DeleteEvent{Object: oldRunnerSet})).To(gomega.BeTrue())

	newRunnerSet := oldRunnerSet.DeepCopy()
	newRunnerSet.Spec.PatchID = 2
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeFalse())

	newRunnerSet = oldRunnerSet.DeepCopy()
	newRunnerSet.ResourceVersion = "1001"
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeFalse())

	newRunnerSet = oldRunnerSet.DeepCopy()
	newRunnerSet.Spec.Replicas = 3
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeTrue())

	newRunnerSet = oldRunnerSet.DeepCopy()
	newRunnerSet.Spec.ActionableRevision = 1
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeTrue())

	newRunnerSet = oldRunnerSet.DeepCopy()
	newRunnerSet.Status.Phase = v1alpha1.EphemeralRunnerSetPhaseRunning
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeTrue())

	newRunnerSet = oldRunnerSet.DeepCopy()
	newRunnerSet.Finalizers = []string{"test-finalizer"}
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeTrue())

	newRunnerSet = oldRunnerSet.DeepCopy()
	deletionTimestamp := metav1.NewTime(time.Now())
	newRunnerSet.DeletionTimestamp = &deletionTimestamp
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldRunnerSet, ObjectNew: newRunnerSet})).To(gomega.BeTrue())
}

func TestEphemeralRunnerOwnedPodPredicate(t *testing.T) {
	g := gomega.NewWithT(t)
	predicate := ephemeralRunnerOwnedPodPredicate()
	basePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-pod",
			ResourceVersion: "1000",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	g.Expect(predicate.Create(event.CreateEvent{Object: basePod})).To(gomega.BeTrue())
	g.Expect(predicate.Delete(event.DeleteEvent{Object: basePod})).To(gomega.BeTrue())

	updatedPod := basePod.DeepCopy()
	updatedPod.ResourceVersion = "1001"
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: basePod, ObjectNew: updatedPod})).To(gomega.BeFalse())

	updatedPod = basePod.DeepCopy()
	updatedPod.Status.Phase = corev1.PodRunning
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: basePod, ObjectNew: updatedPod})).To(gomega.BeTrue())

	updatedPod = basePod.DeepCopy()
	updatedPod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name: v1alpha1.EphemeralRunnerContainerName,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{},
			},
		},
	}
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: basePod, ObjectNew: updatedPod})).To(gomega.BeTrue())

	updatedPod = basePod.DeepCopy()
	updatedPod.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{
			Name: "setup",
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
			},
		},
	}
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: basePod, ObjectNew: updatedPod})).To(gomega.BeTrue())

	updatedPod = basePod.DeepCopy()
	deletionTimestamp := metav1.Now()
	updatedPod.DeletionTimestamp = &deletionTimestamp
	g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: basePod, ObjectNew: updatedPod})).To(gomega.BeTrue())
}
