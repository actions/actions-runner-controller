package controllers

import (
	"context"
	"github.com/google/go-github/v33/github"
	github3 "github.com/google/go-github/v33/github"
	github2 "github.com/summerwind/actions-runner-controller/github"
	"net/http"
	"net/http/httptest"
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
	workflowRunsFor3Replicas             = `{"total_count": 5, "workflow_runs":[{"status":"queued"}, {"status":"queued"}, {"status":"in_progress"}, {"status":"in_progress"}, {"status":"completed"}]}"`
	workflowRunsFor3Replicas_queued      = `{"total_count": 2, "workflow_runs":[{"status":"queued"}, {"status":"queued"}]}"`
	workflowRunsFor3Replicas_in_progress = `{"total_count": 2, "workflow_runs":[{"status":"in_progress"}, {"status":"in_progress"}]}"`
	workflowRunsFor1Replicas             = `{"total_count": 6, "workflow_runs":[{"status":"queued"}, {"status":"completed"}, {"status":"completed"}, {"status":"completed"}, {"status":"completed"}]}"`
	workflowRunsFor1Replicas_queued      = `{"total_count": 1, "workflow_runs":[{"status":"queued"}]}"`
	workflowRunsFor1Replicas_in_progress = `{"total_count": 0, "workflow_runs":[]}"`
)

var webhookServer *httptest.Server

var ghClient *github2.Client

var fakeRunnerList *fake.RunnersList

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
	responses.ListRunners = fake.DefaultListRunnersHandler()
	responses.ListRepositoryWorkflowRuns = &fake.Handler{
		Status: 200,
		Body:   workflowRunsFor3Replicas,
		Statuses: map[string]string{
			"queued":      workflowRunsFor3Replicas_queued,
			"in_progress": workflowRunsFor3Replicas_in_progress,
		},
	}
	fakeRunnerList = fake.NewRunnersList()
	responses.ListRunners = fakeRunnerList.HandleList()
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

		ghClient = newGithubClient(fakeGithubServer)

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
			Client:        mgr.GetClient(),
			Scheme:        scheme.Scheme,
			Log:           logf.Log,
			GitHubClient:  client,
			Recorder:      mgr.GetEventRecorderFor("horizontalrunnerautoscaler-controller"),
			CacheDuration: 1 * time.Second,
		}
		err = autoscalerController.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		autoscalerWebhook := &HorizontalRunnerAutoscalerGitHubWebhook{
			Client:   mgr.GetClient(),
			Scheme:   scheme.Scheme,
			Log:      logf.Log,
			Recorder: mgr.GetEventRecorderFor("horizontalrunnerautoscaler-controller"),
		}
		err = autoscalerWebhook.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup autoscaler webhook")

		mux := http.NewServeMux()
		mux.HandleFunc("/", autoscalerWebhook.Handle)

		webhookServer = httptest.NewServer(mux)

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(stopCh)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		close(stopCh)

		fakeGithubServer.Close()
		webhookServer.Close()

		err := k8sClient.Delete(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	})

	return &testEnvironment{Namespace: ns, Responses: responses}
}

var _ = Context("INTEGRATION: Inside of a new namespace", func() {
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
						ScaleDownDelaySecondsAfterScaleUp: intPtr(1),
						Metrics:                           nil,
						ScaleUpTriggers: []actionsv1alpha1.ScaleUpTrigger{
							{
								GitHubEvent: &actionsv1alpha1.GitHubEventScaleUpTriggerSpec{
									PullRequest: &actionsv1alpha1.PullRequestSpec{
										Types:    []string{"created"},
										Branches: []string{"main"},
									},
								},
								Amount:   1,
								Duration: metav1.Duration{Duration: time.Minute},
							},
						},
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

			{
				var runnerList actionsv1alpha1.RunnerList

				err := k8sClient.List(ctx, &runnerList, client.InNamespace(ns.Name))
				if err != nil {
					logf.Log.Error(err, "list runners")
				}

				for i, r := range runnerList.Items {
					fakeRunnerList.Add(&github3.Runner{
						ID:     github.Int64(int64(i)),
						Name:   github.String(r.Name),
						OS:     github.String("linux"),
						Status: github.String("online"),
						Busy:   github.Bool(false),
					})
				}

				rs, err := ghClient.ListRunners(context.Background(), "", "", "test/valid")
				Expect(err).NotTo(HaveOccurred(), "verifying list fake runners response")
				Expect(len(rs)).To(Equal(3), "count of fake list runners")
			}

			// Scale-down to 1 replica
			{
				time.Sleep(time.Second)

				responses.ListRepositoryWorkflowRuns.Body = workflowRunsFor1Replicas
				responses.ListRepositoryWorkflowRuns.Statuses["queued"] = workflowRunsFor1Replicas_queued
				responses.ListRepositoryWorkflowRuns.Statuses["in_progress"] = workflowRunsFor1Replicas_in_progress

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
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1), "runners after HRA force update for scale-down")
			}

			{
				resp, err := sendWebhook(webhookServer, "pull_request", &github.PullRequestEvent{
					PullRequest: &github.PullRequest{
						Base: &github.PullRequestBranch{
							Ref: github.String("main"),
						},
					},
					Repo: &github.Repository{
						Name: github.String("test/valid"),
						Organization: &github.Organization{
							Name: github.String("test"),
						},
					},
					Action: github.String("created"),
				})

				Expect(err).NotTo(HaveOccurred(), "failed to send pull_request event")

				Expect(resp.StatusCode).To(Equal(200))
			}

			// Scale-up to 2 replicas
			{
				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() int {
						err := k8sClient.List(ctx, &runnerSets, client.InNamespace(ns.Name))
						if err != nil {
							logf.Log.Error(err, "list runner sets")
						}

						return len(runnerSets.Items)
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1), "runner sets after webhook")

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
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(2), "runners after webhook")
			}
		})
	})
})
