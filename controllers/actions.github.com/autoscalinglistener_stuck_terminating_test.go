package actionsgithubcom

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
)

func stuckTerminatingTestObjects(deletedAgo time.Duration, now time.Time, uid types.UID) (*v1alpha1.AutoscalingListener, *corev1.Pod) {
	listener := &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asl", Namespace: "arc-systems"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asl", Namespace: "arc-systems", UID: uid},
		Spec:       corev1.PodSpec{NodeName: "worker-1"},
	}
	if deletedAgo >= 0 {
		pod.DeletionTimestamp = &metav1.Time{Time: now.Add(-deletedAgo)}
	}
	return listener, pod
}

func drainEvents(recorder *events.FakeRecorder) []string {
	var got []string
	for {
		select {
		case e := <-recorder.Events:
			got = append(got, e)
		default:
			return got
		}
	}
}

func TestWarnIfListenerPodStuckTerminating(t *testing.T) {
	now := time.Now()

	t.Run("pod past the threshold gets one Warning event", func(t *testing.T) {
		recorder := events.NewFakeRecorder(10)
		r := &AutoscalingListenerReconciler{Recorder: recorder}
		listener, pod := stuckTerminatingTestObjects(6*time.Minute, now, "uid-1")

		requeue := r.warnIfListenerPodStuckTerminating(listener, pod, now, logf.Log)

		if requeue != 0 {
			t.Fatalf("requeue after warning = %v, want 0", requeue)
		}
		got := drainEvents(recorder)
		if len(got) != 1 {
			t.Fatalf("events = %q, want one", got)
		}
		if !strings.HasPrefix(got[0], corev1.EventTypeWarning+" "+listenerPodStuckTerminatingReason+" ") {
			t.Fatalf("event = %q, want a %s Warning", got[0], listenerPodStuckTerminatingReason)
		}
		if !strings.Contains(got[0], `"worker-1"`) {
			t.Fatalf("event = %q, want the node name", got[0])
		}
	})

	t.Run("pod inside the threshold gets no event and a requeue for the rest", func(t *testing.T) {
		recorder := events.NewFakeRecorder(10)
		r := &AutoscalingListenerReconciler{Recorder: recorder}
		listener, pod := stuckTerminatingTestObjects(2*time.Minute, now, "uid-1")

		requeue := r.warnIfListenerPodStuckTerminating(listener, pod, now, logf.Log)

		if requeue != 3*time.Minute {
			t.Fatalf("requeue = %v, want 3m0s", requeue)
		}
		if got := drainEvents(recorder); len(got) != 0 {
			t.Fatalf("events = %q, want none", got)
		}
	})

	t.Run("pod with a deletion deadline still ahead waits the whole threshold and more", func(t *testing.T) {
		recorder := events.NewFakeRecorder(10)
		r := &AutoscalingListenerReconciler{Recorder: recorder}
		// deletionTimestamp includes the grace period, so it can be in the future.
		listener, pod := stuckTerminatingTestObjects(-30*time.Second, now, "uid-1")
		pod.DeletionTimestamp = &metav1.Time{Time: now.Add(30 * time.Second)}

		requeue := r.warnIfListenerPodStuckTerminating(listener, pod, now, logf.Log)

		if requeue != defaultListenerPodStuckTerminatingWarningAfter+30*time.Second {
			t.Fatalf("requeue = %v, want the threshold plus the grace left", requeue)
		}
		if got := drainEvents(recorder); len(got) != 0 {
			t.Fatalf("events = %q, want none", got)
		}
	})

	t.Run("one event per stuck pod, not one per reconcile", func(t *testing.T) {
		recorder := events.NewFakeRecorder(10)
		r := &AutoscalingListenerReconciler{Recorder: recorder}
		listener, pod := stuckTerminatingTestObjects(10*time.Minute, now, "uid-1")

		for range 3 {
			r.warnIfListenerPodStuckTerminating(listener, pod, now, logf.Log)
		}
		if got := drainEvents(recorder); len(got) != 1 {
			t.Fatalf("events after 3 reconciles of one pod = %q, want one", got)
		}

		_, replacement := stuckTerminatingTestObjects(10*time.Minute, now, "uid-2")
		r.warnIfListenerPodStuckTerminating(listener, replacement, now, logf.Log)
		if got := drainEvents(recorder); len(got) != 1 {
			t.Fatalf("events for a new stuck pod = %q, want one more", got)
		}

		// Once the pod is gone the reconciler forgets it, so the same UID
		// stuck again would be a new episode.
		r.stuckTerminatingWarned.Delete(client.ObjectKeyFromObject(listener))
		r.warnIfListenerPodStuckTerminating(listener, replacement, now, logf.Log)
		if got := drainEvents(recorder); len(got) != 1 {
			t.Fatalf("events after the listener forgot the pod = %q, want one more", got)
		}
	})

	t.Run("a threshold override applies", func(t *testing.T) {
		recorder := events.NewFakeRecorder(10)
		r := &AutoscalingListenerReconciler{Recorder: recorder, listenerPodStuckTerminatingWarningAfter: time.Second}
		listener, pod := stuckTerminatingTestObjects(2*time.Second, now, "uid-1")

		if requeue := r.warnIfListenerPodStuckTerminating(listener, pod, now, logf.Log); requeue != 0 {
			t.Fatalf("requeue = %v, want 0", requeue)
		}
		if got := drainEvents(recorder); len(got) != 1 {
			t.Fatalf("events = %q, want one", got)
		}
	})

	t.Run("a nil recorder only logs", func(t *testing.T) {
		r := &AutoscalingListenerReconciler{}
		listener, pod := stuckTerminatingTestObjects(10*time.Minute, now, "uid-1")

		if requeue := r.warnIfListenerPodStuckTerminating(listener, pod, now, logf.Log); requeue != 0 {
			t.Fatalf("requeue = %v, want 0", requeue)
		}
	})
}

var _ = Describe("AutoscalingListener with a listener pod stuck terminating", func() {
	const holdFinalizer = "arc.test/hold-listener-pod"

	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingListener *v1alpha1.AutoscalingListener

	BeforeEach(func() {
		ctx = context.Background()
		var autoscalingNS *corev1.Namespace
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret := createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache:  newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient()),
			},
			listenerPodStuckTerminatingWarningAfter: time.Second,
		}
		Expect(controller.SetupWithManager(mgr)).To(Succeed(), "failed to setup controller")

		minRunners, maxRunners := 1, 10
		autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-asrs", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &maxRunners,
				MinRunners:         &minRunners,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, autoscalingRunnerSet)).To(Succeed(), "failed to create AutoScalingRunnerSet")

		autoscalingListener = &v1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{Name: "test-asl", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.AutoscalingListenerSpec{
				GitHubConfigURL:               "https://github.com/owner/repo",
				GitHubConfigSecret:            configSecret.Name,
				RunnerScaleSetID:              1,
				AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
			},
		}
		Expect(k8sClient.Create(ctx, autoscalingListener)).To(Succeed(), "failed to create AutoScalingListener")

		startManagers(GinkgoT(), mgr)
	})

	It("emits one Warning event on the listener and leaves the pod alone", func() {
		key := client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}
		pod := new(corev1.Pod)
		Eventually(func() error { return k8sClient.Get(ctx, key, pod) },
			autoscalingListenerTestTimeout, autoscalingListenerTestInterval,
		).Should(Succeed(), "listener pod should be created")

		// envtest has no kubelet: a finalizer keeps the deleted pod around,
		// the way an unreachable node does.
		held := pod.DeepCopy()
		held.Finalizers = append(held.Finalizers, holdFinalizer)
		Expect(k8sClient.Patch(ctx, held, client.MergeFrom(pod))).To(Succeed())
		Expect(k8sClient.Delete(ctx, held)).To(Succeed())
		DeferCleanup(func() {
			current := new(corev1.Pod)
			if err := k8sClient.Get(ctx, key, current); err == nil {
				released := current.DeepCopy()
				released.Finalizers = nil
				_ = k8sClient.Patch(ctx, released, client.MergeFrom(current))
			}
		})

		stuckEvents := func() ([]eventsv1.Event, error) {
			var list eventsv1.EventList
			if err := k8sClient.List(ctx, &list, client.InNamespace(autoscalingListener.Namespace)); err != nil {
				return nil, err
			}
			var found []eventsv1.Event
			for _, e := range list.Items {
				if e.Reason == listenerPodStuckTerminatingReason && e.Regarding.Name == autoscalingListener.Name {
					found = append(found, e)
				}
			}
			return found, nil
		}
		Eventually(stuckEvents, autoscalingListenerTestTimeout, autoscalingListenerTestInterval).
			Should(HaveLen(1), "a Warning event should be emitted once the pod is past the threshold")

		found, err := stuckEvents()
		Expect(err).NotTo(HaveOccurred())
		Expect(found[0].Type).To(Equal(corev1.EventTypeWarning))
		Expect(found[0].Regarding.Kind).To(Equal("AutoscalingListener"))

		// Other reconciles of the same stuck pod do not add events or touch it.
		listener := new(v1alpha1.AutoscalingListener)
		Expect(k8sClient.Get(ctx, key, listener)).To(Succeed())
		touched := listener.DeepCopy()
		if touched.Annotations == nil {
			touched.Annotations = map[string]string{}
		}
		touched.Annotations["arc.test/reconcile"] = "again"
		Expect(k8sClient.Patch(ctx, touched, client.MergeFrom(listener))).To(Succeed())
		Consistently(func() (int, error) {
			found, err := stuckEvents()
			if found == nil || found[0].Series == nil {
				return len(found), err
			}
			return int(found[0].Series.Count) + len(found), err
		}, 3*time.Second, autoscalingListenerTestInterval).Should(Equal(1), "no more events for the same stuck pod")

		current := new(corev1.Pod)
		Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
		Expect(current.UID).To(Equal(held.UID), "the stuck pod is left as it was")
		Expect(current.DeletionTimestamp).NotTo(BeNil())
	})
})
