package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	actionsv1alpha1 "github.com/summerwind/actions-runner-controller/api/v1alpha1"
)

// SetupDeploymentTest will set up a testing environment.
// This includes:
// * creating a Namespace to be used during the test
// * starting the 'RunnerDeploymentReconciler'
// * stopping the 'RunnerDeploymentReconciler" after the test ends
// Call this function at the start of each of your tests.
func SetupDeploymentTest(ctx context.Context) *corev1.Namespace {
	var stopCh chan struct{}
	ns := &corev1.Namespace{}

	BeforeEach(func() {
		stopCh = make(chan struct{})
		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-" + randStringRunes(5)},
		}

		err := k8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		controller := &RunnerDeploymentReconciler{
			Client:   mgr.GetClient(),
			Scheme:   scheme.Scheme,
			Log:      logf.Log,
			Recorder: mgr.GetEventRecorderFor("runnerreplicaset-controller"),
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(stopCh)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		close(stopCh)

		err := k8sClient.Delete(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	})

	return ns
}

var _ = Context("Inside of a new namespace", func() {
	ctx := context.TODO()
	ns := SetupDeploymentTest(ctx)

	Describe("when no existing resources exist", func() {

		It("should create a new RunnerReplicaSet resource from the specified template, add a another RunnerReplicaSet on template modification, and eventually removes old runnerreplicasets", func() {
			name := "example-runnerdeploy"

			{
				rs := &actionsv1alpha1.RunnerDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerDeploymentSpec{
						Replicas: intPtr(1),
						Template: actionsv1alpha1.RunnerTemplate{
							Spec: actionsv1alpha1.RunnerSpec{
								Repository: "foo/bar",
								Image:      "bar",
								Env: []corev1.EnvVar{
									{Name: "FOO", Value: "FOOVALUE"},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() int {
						err := k8sClient.List(ctx, &runnerSets, client.InNamespace(ns.Name))
						if err != nil {
							logf.Log.Error(err, "list runner sets")
						}

						return len(runnerSets.Items)
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1))

				Eventually(
					func() int {
						err := k8sClient.List(ctx, &runnerSets, client.InNamespace(ns.Name))
						if err != nil {
							logf.Log.Error(err, "list runner sets")
						}

						if len(runnerSets.Items) == 0 {
							logf.Log.Info("No runnerreplicasets exist yet")
							return -1
						}

						return *runnerSets.Items[0].Spec.Replicas
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1))
			}

			{
				// We wrap the update in the Eventually block to avoid the below error that occurs due to concurrent modification
				// made by the controller to update .Status.AvailableReplicas and .Status.ReadyReplicas
				//   Operation cannot be fulfilled on runnersets.actions.summerwind.dev "example-runnerset": the object has been modified; please apply your changes to the latest version and try again
				Eventually(func() error {
					var rd actionsv1alpha1.RunnerDeployment

					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: name}, &rd)

					Expect(err).NotTo(HaveOccurred(), "failed to get test RunnerReplicaSet resource")

					rd.Spec.Replicas = intPtr(2)

					return k8sClient.Update(ctx, &rd)
				},
					time.Second*1, time.Millisecond*500).Should(BeNil())

				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() int {
						err := k8sClient.List(ctx, &runnerSets, client.InNamespace(ns.Name))
						if err != nil {
							logf.Log.Error(err, "list runner sets")
						}

						return len(runnerSets.Items)
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1))

				Eventually(
					func() int {
						err := k8sClient.List(ctx, &runnerSets, client.InNamespace(ns.Name))
						if err != nil {
							logf.Log.Error(err, "list runner sets")
						}

						return *runnerSets.Items[0].Spec.Replicas
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(2))
			}
		})
	})
})
