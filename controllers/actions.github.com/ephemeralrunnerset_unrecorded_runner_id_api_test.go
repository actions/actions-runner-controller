package actionsgithubcom

import (
	"context"
	"fmt"
	"sync"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// The set runs under a manager, so it is woken by real watch events. The runner
// controller is stepped by hand, which is what leaves a pod in the state under
// test: envtest runs no scheduler and no kubelet, so the pod is exactly as the
// spec left it. The Actions service is modeled, and answers for registration 7.
var _ = Describe("Test EphemeralRunnerSet scale down of a runner without a recorded runner ID", func() {
	const registeredRunnerID = 7

	var (
		ctx              context.Context
		ns               *corev1.Namespace
		runnerSet        *v1alpha1.EphemeralRunnerSet
		runnerController *EphemeralRunnerReconciler
		runnerKey        types.NamespacedName

		mu          sync.Mutex
		removeReply error
		removals    []int64
	)

	setRemoveReply := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		removeReply = err
	}
	removedRunners := func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), removals...)
	}

	reconcileRunner := func() ctrl.Result {
		result, err := runnerController.Reconcile(ctx, ctrl.Request{NamespacedName: runnerKey})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return result
	}
	getRunner := func() (*v1alpha1.EphemeralRunner, error) {
		runner := new(v1alpha1.EphemeralRunner)
		return runner, k8sClient.Get(ctx, runnerKey, runner)
	}
	getPod := func() (*corev1.Pod, error) {
		pod := new(corev1.Pod)
		return pod, k8sClient.Get(ctx, runnerKey, pod)
	}
	setPodStatus := func(status corev1.PodStatus) {
		pod, err := getPod()
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		pod.Status = status
		ExpectWithOffset(1, k8sClient.Status().Update(ctx, pod)).To(Succeed())
	}
	runnerContainerState := func(state corev1.ContainerState) corev1.PodStatus {
		return corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  v1alpha1.EphemeralRunnerContainerName,
				State: state,
			}},
		}
	}
	scaleToZero := func() {
		current := new(v1alpha1.EphemeralRunnerSet)
		ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(runnerSet), current)).To(Succeed())
		updated := current.DeepCopy()
		updated.Spec.Replicas = 0
		updated.Spec.PatchID = 0
		ExpectWithOffset(1, k8sClient.Patch(ctx, updated, client.MergeFrom(current))).To(Succeed())
	}
	// The runner controller is left alone until the set has deleted the runner,
	// because it would put back the registration finalizer that a scale down of a
	// registered runner drops before deleting it.
	expectRunnerRetired := func() {
		EventuallyWithOffset(1, func() (bool, error) {
			runner, err := getRunner()
			if kerrors.IsNotFound(err) {
				return true, nil
			}
			return err == nil && !runner.DeletionTimestamp.IsZero(), err
		}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "scale down did not retire the runner")
	}
	expectRunnerAndPodGone := func() {
		EventuallyWithOffset(1, func() bool {
			reconcileRunner()
			_, err := getRunner()
			return kerrors.IsNotFound(err)
		}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "the runner was not finalized")

		// An unscheduled pod goes as soon as it is deleted.
		pod, err := getPod()
		if !kerrors.IsNotFound(err) {
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			ExpectWithOffset(1, pod.DeletionTimestamp.IsZero()).To(BeFalse(), "the runner pod was left behind")
		}
		ExpectWithOffset(1, kerrors.IsNotFound(k8sClient.Get(ctx, runnerKey, new(corev1.Secret)))).To(BeTrue(), "the jitconfig secret was left behind")
	}

	BeforeEach(func() {
		ctx = context.Background()
		removeReply, removals = nil, nil

		var mgr ctrl.Manager
		ns, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret := createDefaultSecret(GinkgoT(), k8sClient, ns.Name)

		service := scalefake.NewClient(
			scalefake.WithGenerateJitRunnerConfig(&scaleset.RunnerScaleSetJitRunnerConfig{
				Runner:           &scaleset.RunnerReference{ID: registeredRunnerID, Name: "runner", RunnerScaleSetID: 100},
				EncodedJITConfig: "jit",
			}, nil),
			scalefake.WithRemoveRunnerFunc(func(_ context.Context, id int64) error {
				mu.Lock()
				defer mu.Unlock()
				removals = append(removals, id)
				if id != registeredRunnerID {
					return fmt.Errorf("%w: runner %d", scaleset.NotFoundError, id)
				}
				return removeReply
			}),
		)
		multiClient := scalefake.NewMultiClient(scalefake.WithClient(service))

		setController := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache:  newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), multiClient),
			},
		}
		Expect(setController.SetupWithManager(mgr)).To(Succeed())

		runnerController = &EphemeralRunnerReconciler{
			Client:    k8sClient,
			APIReader: k8sClient,
			Scheme:    mgr.GetScheme(),
			Log:       logf.Log,
			// The workers are never started, so nothing leaves the queue.
			UnregistrationQueue: NewRunnerUnregistrationQueue(logf.Log, nil, 0),
			ResourceBuilder: ResourceBuilder{
				Scheme:         mgr.GetScheme(),
				ResourceCache:  newTestResourceCache(),
				SecretResolver: secretresolver.New(k8sClient, multiClient),
			},
		}

		runnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ers", Namespace: ns.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				PatchID:  1,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName, Image: runnerImage}},
					}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, runnerSet)).To(Succeed())
		startManagers(GinkgoT(), mgr)

		Eventually(func() (int, error) {
			var runners v1alpha1.EphemeralRunnerList
			if err := k8sClient.List(ctx, &runners, client.InNamespace(ns.Name)); err != nil {
				return 0, err
			}
			if len(runners.Items) == 1 {
				runnerKey = client.ObjectKeyFromObject(&runners.Items[0])
			}
			return len(runners.Items), nil
		}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal(1), "the set did not create its runner")

		// Registers the runner and creates the pod.
		reconcileRunner()
		_, err := getPod()
		Expect(err).NotTo(HaveOccurred())
		runner, err := getRunner()
		Expect(err).NotTo(HaveOccurred())
		Expect(runner.Status.RunnerID).To(BeZero())
	})

	It("retires a runner whose pod cannot be scheduled", func() {
		setPodStatus(corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  corev1.PodReasonUnschedulable,
				Message: "0/1 nodes are available",
			}},
		})
		reconcileRunner()
		runner, err := getRunner()
		Expect(err).NotTo(HaveOccurred())
		Expect(runner.Status.RunnerID).To(BeZero(), "a pod without a runner container status must not publish the runner ID")

		scaleToZero()

		expectRunnerRetired()
		expectRunnerAndPodGone()
		Expect(removedRunners()).To(Equal([]int64{registeredRunnerID}), "the registration was not removed before the pod")
	})

	It("retires a runner whose pod is waiting on its container", func() {
		setPodStatus(runnerContainerState(corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}))
		reconcileRunner()
		runner, err := getRunner()
		Expect(err).NotTo(HaveOccurred())
		Expect(runner.Status.RunnerID).To(Equal(registeredRunnerID))

		scaleToZero()

		expectRunnerRetired()
		expectRunnerAndPodGone()
		Expect(removedRunners()).To(Equal([]int64{registeredRunnerID}))
	})

	It("keeps the pod of a busy runner that has not recorded its runner ID", func() {
		// The container is running and the service says it is executing a job,
		// but the runner controller has not caught up: the runner still says 0 and
		// has no job.
		setPodStatus(runnerContainerState(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}))
		setRemoveReply(fmt.Errorf("%w: %w", scaleset.ConflictError, scaleset.JobStillRunningError))
		runner, err := getRunner()
		Expect(err).NotTo(HaveOccurred())
		Expect(runner.Status.RunnerID).To(BeZero())
		Expect(runner.HasJob()).To(BeFalse())

		scaleToZero()

		expectRunnerRetired()
		Expect(removedRunners()).To(BeEmpty(), "the set asked the service about a runner without a recorded ID")

		// Finalizing asks the service about the registration in the jitconfig
		// secret before the live pod goes, and is told to keep it.
		for range 3 {
			Expect(reconcileRunner().RequeueAfter).To(Equal(busyRunnerRequeueInterval))
			runner, err = getRunner()
			Expect(err).NotTo(HaveOccurred())
			Expect(runner.Finalizers).To(ContainElement(ephemeralRunnerActionsFinalizerName))
			pod, err := getPod()
			Expect(err).NotTo(HaveOccurred())
			Expect(pod.DeletionTimestamp.IsZero()).To(BeTrue(), "the pod of a runner executing a job was deleted")
		}
		Expect(removedRunners()).To(HaveEach(int64(registeredRunnerID)))
		Expect(removedRunners()).NotTo(BeEmpty())

		// The job finished and the service let go of the runner.
		setRemoveReply(nil)
		expectRunnerAndPodGone()
	})
})
