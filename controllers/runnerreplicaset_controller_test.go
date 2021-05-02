package controllers

import (
	"context"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"time"

	"github.com/google/go-github/v33/github"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	actionsv1alpha1 "github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github/fake"
)

var (
	runnersList *fake.RunnersList
	server      *httptest.Server
)

// SetupTest will set up a testing environment.
// This includes:
// * creating a Namespace to be used during the test
// * starting the 'RunnerReconciler'
// * stopping the 'RunnerReplicaSetReconciler" after the test ends
// Call this function at the start of each of your tests.
func SetupTest(ctx context.Context) *corev1.Namespace {
	var stopCh chan struct{}
	ns := &corev1.Namespace{}

	BeforeEach(func() {
		stopCh = make(chan struct{})
		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-" + randStringRunes(5)},
		}

		err := k8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Namespace: ns.Name,
		})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		runnersList = fake.NewRunnersList()
		server = runnersList.GetServer()
		ghClient := newGithubClient(server)

		controller := &RunnerReplicaSetReconciler{
			Client:       mgr.GetClient(),
			Scheme:       scheme.Scheme,
			Log:          logf.Log,
			Recorder:     mgr.GetEventRecorderFor("runnerreplicaset-controller"),
			GitHubClient: ghClient,
			Name:         "runnerreplicaset-" + ns.Name,
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

		server.Close()
		err := k8sClient.Delete(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	})

	return ns
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz1234567890")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func intPtr(v int) *int {
	return &v
}

var _ = Context("Inside of a new namespace", func() {
	ctx := context.TODO()
	ns := SetupTest(ctx)

	Describe("when no existing resources exist", func() {

		It("should create a new Runner resource from the specified template, add a another Runner on replicas increased, and removes all the replicas when set to 0", func() {
			name := "example-runnerreplicaset"

			{
				rs := &actionsv1alpha1.RunnerReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerReplicaSetSpec{
						Replicas: intPtr(1),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						Template: actionsv1alpha1.RunnerTemplate{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									"foo": "bar",
								},
							},
							Spec: actionsv1alpha1.RunnerSpec{
								Repository: "test/valid",
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

				runners := actionsv1alpha1.RunnerList{Items: []actionsv1alpha1.Runner{}}

				Eventually(
					func() int {
						selector, err := metav1.LabelSelectorAsSelector(
							&metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
						)
						if err != nil {
							logf.Log.Error(err, "failed to create labelselector")
							return -1
						}
						err = k8sClient.List(
							ctx,
							&runners,
							client.InNamespace(ns.Name),
							client.MatchingLabelsSelector{Selector: selector},
						)
						if err != nil {
							logf.Log.Error(err, "list runners")
							return -1
						}

						for i, runner := range runners.Items {
							runnersList.Add(&github.Runner{
								ID:     pointer.Int64Ptr(int64(i) + 1),
								Name:   pointer.StringPtr(runner.Name),
								OS:     pointer.StringPtr("linux"),
								Status: pointer.StringPtr("online"),
								Busy:   pointer.BoolPtr(false),
							})
						}

						return len(runners.Items)
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1))
			}

			{
				// We wrap the update in the Eventually block to avoid the below error that occurs due to concurrent modification
				// made by the controller to update .Status.AvailableReplicas and .Status.ReadyReplicas
				//   Operation cannot be fulfilled on runnerreplicasets.actions.summerwind.dev "example-runnerreplicaset": the object has been modified; please apply your changes to the latest version and try again
				Eventually(func() error {
					var rs actionsv1alpha1.RunnerReplicaSet

					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: name}, &rs)

					Expect(err).NotTo(HaveOccurred(), "failed to get test RunnerReplicaSet resource")

					rs.Spec.Replicas = intPtr(2)

					return k8sClient.Update(ctx, &rs)
				},
					time.Second*1, time.Millisecond*500).Should(BeNil())

				runners := actionsv1alpha1.RunnerList{Items: []actionsv1alpha1.Runner{}}

				Eventually(
					func() int {
						selector, err := metav1.LabelSelectorAsSelector(
							&metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
						)
						if err != nil {
							logf.Log.Error(err, "failed to create labelselector")
							return -1
						}
						err = k8sClient.List(
							ctx,
							&runners,
							client.InNamespace(ns.Name),
							client.MatchingLabelsSelector{Selector: selector},
						)
						if err != nil {
							logf.Log.Error(err, "list runners")
						}

						for i, runner := range runners.Items {
							runnersList.Add(&github.Runner{
								ID:     pointer.Int64Ptr(int64(i) + 1),
								Name:   pointer.StringPtr(runner.Name),
								OS:     pointer.StringPtr("linux"),
								Status: pointer.StringPtr("online"),
								Busy:   pointer.BoolPtr(false),
							})
						}

						return len(runners.Items)
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(2))
			}

			{
				// We wrap the update in the Eventually block to avoid the below error that occurs due to concurrent modification
				// made by the controller to update .Status.AvailableReplicas and .Status.ReadyReplicas
				//   Operation cannot be fulfilled on runnersets.actions.summerwind.dev "example-runnerset": the object has been modified; please apply your changes to the latest version and try again
				Eventually(func() error {
					var rs actionsv1alpha1.RunnerReplicaSet

					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: name}, &rs)

					Expect(err).NotTo(HaveOccurred(), "failed to get test RunnerReplicaSet resource")

					rs.Spec.Replicas = intPtr(0)

					return k8sClient.Update(ctx, &rs)
				},
					time.Second*1, time.Millisecond*500).Should(BeNil())

				runners := actionsv1alpha1.RunnerList{Items: []actionsv1alpha1.Runner{}}

				Eventually(
					func() int {
						selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						})
						Expect(err).ToNot(HaveOccurred())

						var regOnly actionsv1alpha1.Runner
						if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: registrationOnlyRunnerNameFor(name)}, &regOnly); err != nil {
							logf.Log.Info(fmt.Sprintf("Failed getting registration-only runner in test: %v", err))
							return -1
						} else {
							updated := regOnly.DeepCopy()
							updated.Status.Phase = "Completed"

							if err := k8sClient.Status().Patch(ctx, updated, client.MergeFrom(&regOnly)); err != nil {
								logf.Log.Info(fmt.Sprintf("Failed updating registration-only runner in test: %v", err))
								return -1
							}

							runnersList.Add(&github.Runner{
								ID:     pointer.Int64Ptr(1001),
								Name:   pointer.StringPtr(regOnly.Name),
								OS:     pointer.StringPtr("linux"),
								Status: pointer.StringPtr("offline"),
								Busy:   pointer.BoolPtr(false),
							})
						}

						if err := k8sClient.List(ctx, &runners, client.InNamespace(ns.Name), client.MatchingLabelsSelector{Selector: selector}); err != nil {
							logf.Log.Error(err, "list runners")
							return -1
						}

						for i, runner := range runners.Items {
							runnersList.Add(&github.Runner{
								ID:     pointer.Int64Ptr(int64(i) + 1),
								Name:   pointer.StringPtr(runner.Name),
								OS:     pointer.StringPtr("linux"),
								Status: pointer.StringPtr("online"),
								Busy:   pointer.BoolPtr(false),
							})
						}

						return len(runners.Items)
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(0))
			}
		})
	})
})
