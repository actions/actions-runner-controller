package controllers

import (
	"context"
	"math/rand"
	"net/http/httptest"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/github/fake"
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
func SetupTest(ctx2 context.Context) *corev1.Namespace {
	var ctx context.Context
	var cancel func()
	ns := &corev1.Namespace{}

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(ctx2)
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

		controller := &RunnerReplicaSetReconciler{
			Client:   mgr.GetClient(),
			Scheme:   scheme.Scheme,
			Log:      logf.Log,
			Recorder: mgr.GetEventRecorderFor("runnerreplicaset-controller"),
			Name:     "runnerreplicaset-" + ns.Name,
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		defer cancel()

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
	name := "example-runnerreplicaset"

	getRunnerCount := func() int {
		runners := actionsv1alpha1.RunnerList{Items: []actionsv1alpha1.Runner{}}

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

		runnersList.Sync(runners.Items)

		return len(runners.Items)
	}

	Describe("RunnerReplicaSet", func() {
		It("should create a new Runner resource from the specified template", func() {
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
								RunnerConfig: actionsv1alpha1.RunnerConfig{
									Repository: "test/valid",
									Image:      "bar",
								},
								RunnerPodSpec: actionsv1alpha1.RunnerPodSpec{
									Env: []corev1.EnvVar{
										{Name: "FOO", Value: "FOOVALUE"},
									},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				Eventually(
					getRunnerCount,
					time.Second*5, time.Second).Should(BeEquivalentTo(1))
			}
		})

		It("should create 2 runners when specified 2 replicas", func() {
			{
				rs := &actionsv1alpha1.RunnerReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerReplicaSetSpec{
						Replicas: intPtr(2),
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
								RunnerConfig: actionsv1alpha1.RunnerConfig{
									Repository: "test/valid",
									Image:      "bar",
								},
								RunnerPodSpec: actionsv1alpha1.RunnerPodSpec{
									Env: []corev1.EnvVar{
										{Name: "FOO", Value: "FOOVALUE"},
									},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				Eventually(
					getRunnerCount,
					time.Second*5, time.Second).Should(BeEquivalentTo(2))
			}
		})

		It("should not create any runners when specified 0 replicas", func() {
			{
				rs := &actionsv1alpha1.RunnerReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerReplicaSetSpec{
						Replicas: intPtr(0),
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
								RunnerConfig: actionsv1alpha1.RunnerConfig{
									Repository: "test/valid",
									Image:      "bar",
								},
								RunnerPodSpec: actionsv1alpha1.RunnerPodSpec{
									Env: []corev1.EnvVar{
										{Name: "FOO", Value: "FOOVALUE"},
									},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				Consistently(
					getRunnerCount,
					time.Second*5, time.Second).Should(BeEquivalentTo(0))
			}
		})
	})
})
