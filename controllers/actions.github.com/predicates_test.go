package actionsgithubcom

import (
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestAutoscalingRunnerSetOwnedEphemeralRunnerSetPredicate(t *testing.T) {
	base := func() *v1alpha1.EphemeralRunnerSet {
		return &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "runner-set",
				Namespace:   "default",
				Generation:  1,
				Labels:      map[string]string{"app": "runner"},
				Annotations: map[string]string{AnnotationKeyPatchID: "1"},
				Finalizers:  []string{"finalizer"},
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas:           2,
				PatchID:            3,
				ActionableRevision: 4,
			},
			Status: v1alpha1.EphemeralRunnerSetStatus{
				Phase: v1alpha1.EphemeralRunnerSetPhaseRunning,
			},
		}
	}

	// Every field the AutoscalingRunnerSet reconciler reads off an owned
	// EphemeralRunnerSet has to wake it up.
	for name, mutate := range map[string]func(*v1alpha1.EphemeralRunnerSet){
		"replicas":            func(s *v1alpha1.EphemeralRunnerSet) { s.Spec.Replicas = 5 },
		"patch id":            func(s *v1alpha1.EphemeralRunnerSet) { s.Spec.PatchID = 9 },
		"actionable revision": func(s *v1alpha1.EphemeralRunnerSet) { s.Spec.ActionableRevision = 7 },
		"ephemeral runner spec": func(s *v1alpha1.EphemeralRunnerSet) {
			s.Spec.EphemeralRunnerSpec.GitHubConfigURL = "https://github.com/org"
		},
		"ephemeral runner meta": func(s *v1alpha1.EphemeralRunnerSet) {
			s.Spec.EphemeralRunnerMetadata = &v1alpha1.ResourceMeta{Labels: map[string]string{"a": "b"}}
		},
		"labels":             func(s *v1alpha1.EphemeralRunnerSet) { s.Labels["app"] = "changed" },
		"annotations":        func(s *v1alpha1.EphemeralRunnerSet) { s.Annotations[AnnotationKeyPatchID] = "2" },
		"finalizers":         func(s *v1alpha1.EphemeralRunnerSet) { s.Finalizers = nil },
		"deletion timestamp": func(s *v1alpha1.EphemeralRunnerSet) { s.DeletionTimestamp = &metav1.Time{Time: time.Now()} },
		"generation":         func(s *v1alpha1.EphemeralRunnerSet) { s.Generation = 2 },
		"owner references":   func(s *v1alpha1.EphemeralRunnerSet) { s.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}} },
	} {
		t.Run("reconciles on "+name, func(t *testing.T) {
			old, updated := base(), base()
			mutate(updated)
			assert.True(t, autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
		})
	}

	t.Run("ignores the finished runner cleanup patch id", func(t *testing.T) {
		old, updated := base(), base()
		updated.Status.FinishedRunnerCleanupPatchID = 3
		updated.ResourceVersion = "2"
		assert.False(t, autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
	})

	t.Run("reconciles on unexpected types", func(t *testing.T) {
		assert.True(t, autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate().Update(event.UpdateEvent{
			ObjectOld: &corev1.Pod{},
			ObjectNew: &corev1.Pod{},
		}))
	})

	t.Run("does not filter create, delete or generic events", func(t *testing.T) {
		p := autoscalingRunnerSetOwnedEphemeralRunnerSetPredicate()
		assert.True(t, p.Create(event.CreateEvent{Object: base()}))
		assert.True(t, p.Delete(event.DeleteEvent{Object: base()}))
		assert.True(t, p.Generic(event.GenericEvent{Object: base()}))
	})
}

func TestEphemeralRunnerSetOwnedEphemeralRunnerPredicate(t *testing.T) {
	base := func() *v1alpha1.EphemeralRunner {
		return &v1alpha1.EphemeralRunner{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "runner",
				Namespace:  "default",
				Generation: 1,
				Annotations: map[string]string{
					AnnotationKeyPatchID:            "1",
					AnnotationKeyActionableRevision: "1",
				},
				Finalizers: []string{"finalizer"},
			},
			Status: v1alpha1.EphemeralRunnerStatus{
				Phase:    v1alpha1.EphemeralRunnerPhaseRunning,
				RunnerID: 42,
			},
		}
	}

	// The EphemeralRunnerSet reconciler groups runners by phase, reads the patch
	// id and actionable revision annotations, and needs the runner id to remove
	// the runner from the service.
	for name, mutate := range map[string]func(*v1alpha1.EphemeralRunner){
		"phase":               func(r *v1alpha1.EphemeralRunner) { r.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded },
		"runner id":           func(r *v1alpha1.EphemeralRunner) { r.Status.RunnerID = 43 },
		"patch id annotation": func(r *v1alpha1.EphemeralRunner) { r.Annotations[AnnotationKeyPatchID] = "2" },
		"actionable revision": func(r *v1alpha1.EphemeralRunner) { r.Annotations[AnnotationKeyActionableRevision] = "2" },
		"labels":              func(r *v1alpha1.EphemeralRunner) { r.Labels = map[string]string{"a": "b"} },
		"finalizers":          func(r *v1alpha1.EphemeralRunner) { r.Finalizers = nil },
		"deletion timestamp":  func(r *v1alpha1.EphemeralRunner) { r.DeletionTimestamp = &metav1.Time{Time: time.Now()} },
		"generation":          func(r *v1alpha1.EphemeralRunner) { r.Generation = 2 },
		"spec":                func(r *v1alpha1.EphemeralRunner) { r.Spec.GitHubConfigURL = "https://github.com/org" },
	} {
		t.Run("reconciles on "+name, func(t *testing.T) {
			old, updated := base(), base()
			mutate(updated)
			assert.True(t, ephemeralRunnerSetOwnedEphemeralRunnerPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
		})
	}

	t.Run("ignores runner status the runner set never reads", func(t *testing.T) {
		old, updated := base(), base()
		updated.Status.Ready = true
		updated.Status.Reason = "reason"
		updated.Status.Message = "message"
		updated.Status.RunnerName = "runner-name"
		updated.Status.Failures = map[string]metav1.Time{"pod": metav1.Now()}
		updated.Status.JobRequestID = 7
		updated.Status.JobID = "job"
		updated.Status.JobDisplayName = "display"
		updated.Status.JobRepositoryName = "org/repo"
		updated.Status.JobWorkflowRef = "ref"
		updated.ResourceVersion = "2"
		assert.False(t, ephemeralRunnerSetOwnedEphemeralRunnerPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
	})

	t.Run("reconciles on unexpected types", func(t *testing.T) {
		assert.True(t, ephemeralRunnerSetOwnedEphemeralRunnerPredicate().Update(event.UpdateEvent{
			ObjectOld: &corev1.Pod{},
			ObjectNew: &corev1.Pod{},
		}))
	})
}

func TestEphemeralRunnerOwnedPodPredicate(t *testing.T) {
	base := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "runner",
				Namespace: "default",
				UID:       types.UID("uid"),
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{
						Type:               corev1.PodReady,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(time.Now()),
					},
				},
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  v1alpha1.EphemeralRunnerContainerName,
						Ready: true,
						State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
					},
				},
				InitContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "init",
						State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
					},
				},
			},
		}
	}

	// Every pod field the EphemeralRunner reconciler branches on has to wake it up.
	for name, mutate := range map[string]func(*corev1.Pod){
		"phase":   func(p *corev1.Pod) { p.Status.Phase = corev1.PodFailed },
		"reason":  func(p *corev1.Pod) { p.Status.Reason = "Evicted" },
		"message": func(p *corev1.Pod) { p.Status.Message = "evicted" },
		"uid":     func(p *corev1.Pod) { p.UID = types.UID("other") },
		"deletion timestamp": func(p *corev1.Pod) {
			p.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		},
		"runner container terminated": func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}
		},
		"runner container exit code": func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 7}}
		},
		"runner container readiness": func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].Ready = false
		},
		"init container status": func(p *corev1.Pod) {
			p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}
		},
		"ready condition": func(p *corev1.Pod) {
			p.Status.Conditions[0].Status = corev1.ConditionFalse
		},
	} {
		t.Run("reconciles on "+name, func(t *testing.T) {
			old, updated := base(), base()
			mutate(updated)
			assert.True(t, ephemeralRunnerOwnedPodPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
		})
	}

	t.Run("ignores pod noise the runner never reads", func(t *testing.T) {
		old, updated := base(), base()
		updated.ResourceVersion = "2"
		updated.Labels = map[string]string{"a": "b"}
		updated.Status.PodIP = "10.0.0.1"
		updated.Status.PodIPs = []corev1.PodIP{{IP: "10.0.0.1"}}
		updated.Status.HostIP = "10.0.0.2"
		updated.Status.StartTime = &metav1.Time{Time: time.Now()}
		updated.Status.Conditions = append(updated.Status.Conditions,
			corev1.PodCondition{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			corev1.PodCondition{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
		)
		assert.False(t, ephemeralRunnerOwnedPodPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
	})

	t.Run("ready condition transition time alone does not reconcile", func(t *testing.T) {
		old, updated := base(), base()
		updated.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(time.Hour))
		assert.False(t, ephemeralRunnerOwnedPodPredicate().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}))
	})

	t.Run("reconciles on unexpected types", func(t *testing.T) {
		assert.True(t, ephemeralRunnerOwnedPodPredicate().Update(event.UpdateEvent{
			ObjectOld: &v1alpha1.EphemeralRunner{},
			ObjectNew: &v1alpha1.EphemeralRunner{},
		}))
	})
}
