package controllers

import (
	"context"
	"time"

	"github.com/summerwind/actions-runner-controller/github/fake"

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

type testEnvironment struct {
	Namespace *corev1.Namespace
	Responses *fake.FixedResponses
}

var (
	workflowRunsFor3Replicas = `{"total_count": 5, "workflow_runs":[{"status":"queued"}, {"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`
	workflowRunsFor1Replicas = `{"total_count": 6, "workflow_runs":[{"status":"queued"}, {"status":"completed"}, {"status":"completed"}, {"status":"completed"}, {"status":"completed"}]}"`
)

// SetupIntegrationTest will set up a testing environment.
// This includes:
// * creating a Namespace to be used during the test
// * starting all the reconcilers
// * stopping all the reconcilers after the test ends
// Call this function at the start of each of your tests.
func SetupIntegrationTest(ctx context.Context) *testEnvironment {
	var stopCh chan struct{}
	ns := &corev1.Namespace{}

	responses := &fake.FixedResponses{}
	responses.ListRepositoryWorkflowRuns = &fake.Handler{
		Status: 200,
		Body:   workflowRunsFor3Replicas,
	}
	fakeGithubServer := fake.NewServer(fake.WithFixedResponses(responses))

	BeforeEach(func() {
		stopCh = make(chan struct{})
		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-" + randStringRunes(5)},
		}

		err := k8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		runnersList = fake.NewRunnersList()
		server = runnersList.GetServer()
		ghClient := newGithubClient(server)

		replicasetController := &RunnerReplicaSetReconciler{
			Client:       mgr.GetClient(),
			Scheme:       scheme.Scheme,
			Log:          logf.Log,
			Recorder:     mgr.GetEventRecorderFor("runnerreplicaset-controller"),
			GitHubClient: ghClient,
		}
		err = replicasetController.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		deploymentsController := &RunnerDeploymentReconciler{
			Client:   mgr.GetClient(),
			Scheme:   scheme.Scheme,
			Log:      logf.Log,
			Recorder: mgr.GetEventRecorderFor("runnerdeployment-controller"),
		}
		err = deploymentsController.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		client := newGithubClient(fakeGithubServer)

		autoscalerController := &HorizontalRunnerAutoscalerReconciler{
			Client:       mgr.GetClient(),
			Scheme:       scheme.Scheme,
			Log:          logf.Log,
			GitHubClient: client,
			Recorder:     mgr.GetEventRecorderFor("horizontalrunnerautoscaler-controller"),
		}
		err = autoscalerController.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(stopCh)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		close(stopCh)

		fakeGithubServer.Close()

		err := k8sClient.Delete(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	})

	return &testEnvironment{Namespace: ns, Responses: responses}
}

var _ = Context("Inside of a new namespace", func() {
	ctx := context.TODO()
	env := SetupIntegrationTest(ctx)
	ns := env.Namespace
	responses := env.Responses

	Describe("when no existing resources exist", func() {

		It("should create and scale runners", func() {
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
								Repository: "test/valid",
								Image:      "bar",
								Group:      "baz",
								Env: []corev1.EnvVar{
									{Name: "FOO", Value: "FOOVALUE"},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerDeployment resource")

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

					Expect(err).NotTo(HaveOccurred(), "failed to get test RunnerDeployment resource")

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

			// Scale-up to 3 replicas
			{
				hra := &actionsv1alpha1.HorizontalRunnerAutoscaler{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.HorizontalRunnerAutoscalerSpec{
						ScaleTargetRef: actionsv1alpha1.ScaleTargetRef{
							Name: name,
						},
						MinReplicas:                       intPtr(1),
						MaxReplicas:                       intPtr(3),
						ScaleDownDelaySecondsAfterScaleUp: nil,
						Metrics:                           nil,
					},
				}

				err := k8sClient.Create(ctx, hra)

				Expect(err).NotTo(HaveOccurred(), "failed to create test HorizontalRunnerAutoscaler resource")

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
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(3))
			}

			// Scale-down to 1 replica
			{
				responses.ListRepositoryWorkflowRuns.Body = workflowRunsFor1Replicas

				var hra actionsv1alpha1.HorizontalRunnerAutoscaler

				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: name}, &hra)

				Expect(err).NotTo(HaveOccurred(), "failed to get test HorizontalRunnerAutoscaler resource")

				hra.Annotations = map[string]string{
					"force-update": "1",
				}

				err = k8sClient.Update(ctx, &hra)

				Expect(err).NotTo(HaveOccurred(), "failed to get test HorizontalRunnerAutoscaler resource")

				Eventually(
					func() int {
						var runnerSets actionsv1alpha1.RunnerReplicaSetList

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
		})
	})
})
