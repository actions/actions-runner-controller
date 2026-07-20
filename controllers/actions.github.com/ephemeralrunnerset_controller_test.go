package actionsgithubcom

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/scaleset"
	prometheusdto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	controllerMetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	fake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
)

const (
	ephemeralRunnerSetTestTimeout  = time.Second * 20
	ephemeralRunnerSetTestInterval = time.Millisecond * 250
)

var registerActionsGithubMetricsForTest sync.Once

func TestPrecomputedConstants(t *testing.T) {
	require.Equal(t, len(failedRunnerBackoff), maxFailures+1)
}

func expectEphemeralRunnerPhase(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, phase v1alpha1.EphemeralRunnerPhase) {
	updated := new(v1alpha1.EphemeralRunner)
	err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
	Expect(err).NotTo(HaveOccurred(), "failed to get ephemeral runner")
	Expect(updated.Status.Phase).To(Equal(phase))
}

func expectEphemeralRunnerPhaseMetric(ephemeralRunner *v1alpha1.EphemeralRunner, phase v1alpha1.EphemeralRunnerPhase, expected float64) {
	metricName := ephemeralRunnerPhaseMetricName(phase)
	Expect(metricName).NotTo(BeEmpty(), "unexpected ephemeral runner phase")

	parsedURL, err := actions.ParseGitHubConfigFromURL(ephemeralRunner.Spec.GitHubConfigURL)
	Expect(err).NotTo(HaveOccurred(), "failed to parse GitHub config URL")

	value, err := ephemeralRunnerPhaseMetricValue(metricName, map[string]string{
		"name":         ephemeralRunner.Labels[LabelKeyGitHubScaleSetName],
		"namespace":    ephemeralRunner.Labels[LabelKeyGitHubScaleSetNamespace],
		"repository":   parsedURL.Repository,
		"organization": parsedURL.Organization,
		"enterprise":   parsedURL.Enterprise,
	})
	Expect(err).NotTo(HaveOccurred(), "failed to gather ephemeral runner phase metrics")
	Expect(value).To(Equal(expected))
}

func ephemeralRunnerPhaseMetricName(phase v1alpha1.EphemeralRunnerPhase) string {
	switch phase {
	case v1alpha1.EphemeralRunnerPhasePending:
		return "gha_controller_pending_ephemeral_runners"
	case v1alpha1.EphemeralRunnerPhaseRunning:
		return "gha_controller_running_ephemeral_runners"
	case v1alpha1.EphemeralRunnerPhaseSucceeded:
		return "gha_controller_succeeded_ephemeral_runners"
	case v1alpha1.EphemeralRunnerPhaseFailed:
		return "gha_controller_failed_ephemeral_runners"
	case v1alpha1.EphemeralRunnerPhaseOutdated:
		return "gha_controller_outdated_ephemeral_runners"
	default:
		return ""
	}
}

func ephemeralRunnerPhaseMetricValue(metricName string, labels map[string]string) (float64, error) {
	metricFamilies, err := controllerMetrics.Registry.Gather()
	if err != nil {
		return 0, err
	}

	for _, metricFamily := range metricFamilies {
		if metricFamily.GetName() != metricName {
			continue
		}

		for _, metric := range metricFamily.GetMetric() {
			if metricHasLabels(metric, labels) {
				if metric.GetGauge() == nil {
					return 0, nil
				}
				return metric.GetGauge().GetValue(), nil
			}
		}
	}

	return 0, nil
}

func metricHasLabels(metric *prometheusdto.Metric, labels map[string]string) bool {
	metricLabels := map[string]string{}
	for _, label := range metric.GetLabel() {
		metricLabels[label.GetName()] = label.GetValue()
	}

	for name, value := range labels {
		if metricLabels[name] != value {
			return false
		}
	}
	return true
}

var _ = Describe("Test EphemeralRunnerSet controller", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet
	var configSecret *corev1.Secret
	var resourceCache *ResourceCache

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)
		resourceCache = newTestResourceCache()

		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache: resourceCache,
				SecretResolver: secretresolver.New(mgr.GetClient(), fake.NewMultiClient(
					fake.WithClient(
						fake.NewClient(
							fake.WithRemoveRunner(nil),
						),
					),
				)),
			},
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		ephemeralRunnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
				Annotations: map[string]string{
					"arc.test/runner-set-annotation": "initial",
				},
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "runner",
									Image: "ghcr.io/actions/runner",
								},
							},
						},
					},
				},
			},
		}

		err = k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create EphemeralRunnerSet")

		startManagers(GinkgoT(), mgr)
	})

	Context("When creating a new EphemeralRunnerSet", func() {
		It("It should create/add all required resources for a new EphemeralRunnerSet (finalizer)", func() {
			// Check if finalizer is added
			created := new(v1alpha1.EphemeralRunnerSet)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(EphemeralRunnerSetFinalizerName), "EphemeralRunnerSet should have a finalizer")

			// Check if the number of ephemeral runners are stay 0
			Consistently(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(0), "No EphemeralRunner should be created")

			// Check if the status is initialized
			Consistently(
				func() (v1alpha1.EphemeralRunnerSetPhase, error) {
					runnerSet := new(v1alpha1.EphemeralRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, runnerSet)
					if err != nil {
						return "", err
					}

					return runnerSet.Status.Phase, nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(v1alpha1.EphemeralRunnerSetPhaseRunning), "EphemeralRunnerSet status should be running")

			// Scaling up the EphemeralRunnerSet
			updated := created.DeepCopy()
			updated.Spec.Replicas = 5
			err := k8sClient.Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Check if the number of ephemeral runners are created
			Eventually(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					// Set status to simulate a configured EphemeralRunner
					refetch := false
					for i, runner := range runnerList.Items {
						if runner.Status.RunnerID == 0 {
							updatedRunner := runner.DeepCopy()
							updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
							updatedRunner.Status.RunnerID = i + 100
							err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runner))
							Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
							refetch = true
						}
					}

					if refetch {
						if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
							return -1, err
						}
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")
		})
	})

	Context("When deleting a new EphemeralRunnerSet", func() {
		It("It should cleanup all resources for a deleting EphemeralRunnerSet before removing it", func() {
			created := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, created)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")
			resourceCache.listenerPod.Upsert(created, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "cached-runner-set-pod", Namespace: created.Namespace}})
			Expect(resourceCacheHasMainObjectEntries(resourceCache, created)).To(BeTrue(), "test setup should cache an EphemeralRunnerSet-owned resource")

			// Scale up the EphemeralRunnerSet
			updated := created.DeepCopy()
			updated.Spec.Replicas = 5
			err = k8sClient.Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Wait for the EphemeralRunnerSet to be scaled up
			Eventually(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					// Set status to simulate a configured EphemeralRunner
					refetch := false
					for i, runner := range runnerList.Items {
						if runner.Status.RunnerID == 0 {
							updatedRunner := runner.DeepCopy()
							updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
							updatedRunner.Status.RunnerID = i + 100
							err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runner))
							Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
							refetch = true
						}
					}

					if refetch {
						if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
							return -1, err
						}
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")

			// Delete the EphemeralRunnerSet
			err = k8sClient.Delete(ctx, created)
			Expect(err).NotTo(HaveOccurred(), "failed to delete EphemeralRunnerSet")

			// Check if all ephemeral runners are deleted
			Eventually(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(0), "All EphemeralRunner should be deleted")

			// Check if the EphemeralRunnerSet is deleted
			Eventually(
				func() error {
					deleted := new(v1alpha1.EphemeralRunnerSet)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, deleted)
					if err != nil {
						if kerrors.IsNotFound(err) {
							return nil
						}

						return err
					}

					return fmt.Errorf("EphemeralRunnerSet is not deleted")
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(Succeed(), "EphemeralRunnerSet should be deleted")

			Eventually(
				func() bool {
					return resourceCacheHasMainObjectEntries(resourceCache, created)
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeFalse(), "EphemeralRunnerSet-owned resources should be removed from cache after deletion")
		})
	})

	Context("When a new EphemeralRunnerSet scale up and down", func() {
		It("Should scale up with patch ID 0", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 5
			updated.Spec.PatchID = 0

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")
		})

		It("propagates updated EphemeralRunnerSet annotations to newly created EphemeralRunners", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 1
			updated.Spec.PatchID = 0
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to scale EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func(g Gomega) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					g.Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunners")
					g.Expect(runnerList.Items).To(HaveLen(1))
					g.Expect(runnerList.Items[0].Annotations["arc.test/runner-set-annotation"]).To(Equal("initial"))
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(Succeed())

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Annotations["arc.test/runner-set-annotation"] = "updated"
			updated.Annotations["arc.test/new-runner-set-annotation"] = "added"
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet annotations")

			Eventually(
				func(g Gomega) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					g.Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunners")
					g.Expect(runnerList.Items).To(HaveLen(2))

					annotationsByValue := map[string]int{}
					var updatedRunnerHasNewAnnotation bool
					for _, runner := range runnerList.Items {
						annotationsByValue[runner.Annotations["arc.test/runner-set-annotation"]]++
						if runner.Annotations["arc.test/runner-set-annotation"] == "updated" && runner.Annotations["arc.test/new-runner-set-annotation"] == "added" {
							updatedRunnerHasNewAnnotation = true
						}
					}
					g.Expect(annotationsByValue["initial"]).To(Equal(1))
					g.Expect(annotationsByValue["updated"]).To(Equal(1))
					g.Expect(updatedRunnerHasNewAnnotation).To(BeTrue())
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(Succeed())
		})

		It("Should scale up when patch ID changes", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 1
			updated.Spec.PatchID = 0

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "1 EphemeralRunner should be created")

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")
		})

		It("Should clean up finished ephemeral runner when scaling down", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Keep the ephemeral runner until the next patch
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "1 EphemeralRunner should be up")

			// The listener was slower to patch the completed, but we should still have 1 running
			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 1
			updated.Spec.PatchID = 2

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "1 Ephemeral runner should be up")
		})

		It("Should keep finished ephemeral runners until patch id changes", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhasePending
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// confirm they are not deleted
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				5*time.Second,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")
		})

		It("Should handle double scale up", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning

			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 3
			updated.Spec.PatchID = 2

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "only the running EphemeralRunner should remain before listener confirms the larger desired count")

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 3
			updated.Spec.PatchID = 3

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			// We should have 3 runners, and have no Succeeded ones after listener confirms.
			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}

					for _, runner := range runnerList.Items {
						if runner.Status.Phase == v1alpha1.EphemeralRunnerPhaseSucceeded {
							return fmt.Errorf("Runner %s is in Succeeded phase", runner.Name)
						}
					}

					if len(runnerList.Items) != 3 {
						return fmt.Errorf("Expected 3 runners, got %d", len(runnerList.Items))
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "3 EphemeralRunner should be created and none should be in Succeeded phase")
		})

		It("Should handle scale down without removing pending runners", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhasePending
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for these statuses to actually be updated
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}
					pending := 0
					succeeded := 0
					for _, runner := range runnerList.Items {
						switch runner.Status.Phase {
						case v1alpha1.EphemeralRunnerPhaseSucceeded:
							succeeded++
						case v1alpha1.EphemeralRunnerPhasePending:
							pending++
						}
					}

					if pending != 1 && succeeded != 1 {
						return fmt.Errorf("Expected 1 runner in Pending and 1 in Succeeded, got %d in Pending and %d in Succeeded", pending, succeeded)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "1 EphemeralRunner should be in Pending and 1 in Succeeded phase")

			// Scale down to 0, while 1 is still pending. This simulates the difference between the desired and actual state
			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 0
			updated.Spec.PatchID = 2

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			// We should have 1 runner up and pending
			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}

					if len(runnerList.Items) != 1 {
						return fmt.Errorf("Expected 1 runner, got %d", len(runnerList.Items))
					}

					if runnerList.Items[0].Status.Phase != v1alpha1.EphemeralRunnerPhasePending {
						return fmt.Errorf("Expected runner to be in Pending, got %s", runnerList.Items[0].Status.Phase)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "1 EphemeralRunner should be created and in Pending phase")

			// Now, the ephemeral runner finally is done and we can scale down to 0
			updatedRunner = runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(0), "2 EphemeralRunner should be created")
		})

		It("Should kill pending and running runners if they are up for some reason and the batch contains no jobs", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			// Put one runner in Pending and one in Running
			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhasePending
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for these statuses to actually be updated
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}

					pending := 0
					running := 0

					for _, runner := range runnerList.Items {
						switch runner.Status.Phase {
						case v1alpha1.EphemeralRunnerPhasePending:
							pending++
						case v1alpha1.EphemeralRunnerPhaseRunning:
							running++

						}
					}

					if pending != 1 && running != 1 {
						return fmt.Errorf("Expected 1 runner in Pending and 1 in Running, got %d in Pending and %d in Running", pending, running)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "1 EphemeralRunner should be in Pending and 1 in Running phase")

			// Scale down to 0 with patch ID 0. This forces the scale down to self correct on empty batch

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 0
			updated.Spec.PatchID = 0

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Consistently(
				func() (int, error) {
					if err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace); err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be up since they don't have an ID yet")

			// Now, let's say ephemeral runner controller patched these ephemeral runners with the registration.

			updatedRunner = runnerList.Items[0].DeepCopy()
			updatedRunner.Status.RunnerID = 1
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.RunnerID = 2
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Now, eventually, they should be deleted
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(0), "0 EphemeralRunner should exist")
		})

		It("Should replace finished ephemeral runners with new ones", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			// Put one runner in Succeeded and one in Running
			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for these statuses to actually be updated

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}

					succeeded := 0
					running := 0

					for _, runner := range runnerList.Items {
						switch runner.Status.Phase {
						case v1alpha1.EphemeralRunnerPhaseSucceeded:
							succeeded++
						case v1alpha1.EphemeralRunnerPhaseRunning:
							running++
						}
					}

					if succeeded != 1 || running != 1 {
						return fmt.Errorf("Expected 1 runner in Succeeded and 1 in Running, got %d in Succeeded and %d in Running", succeeded, running)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "1 EphemeralRunner should be in Succeeded and 1 in Running phase")

			// Now, let's simulate the listener publishing a stale patch before it has
			// accounted for the completed job. The controller should clean up the
			// finished runner but not create a replacement for this patch.

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 2

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Consistently(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				2*time.Second,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "only the running EphemeralRunner should remain before listener confirms replacement")

			// A fresh listener decision with the same desired count confirms that a
			// replacement is still needed.
			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 3

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}

					if len(runnerList.Items) != 2 {
						return fmt.Errorf("Expected 2 runners, got %d", len(runnerList.Items))
					}

					for _, runner := range runnerList.Items {
						if runner.Status.Phase == v1alpha1.EphemeralRunnerPhaseSucceeded {
							return fmt.Errorf("Expected no runners in Succeeded phase, got one")
						}
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "2 EphemeralRunner should be created and none should be in Succeeded phase")
		})

		It("Should not create a replacement when a runner finishes ahead of the listener decrement patch", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 4
			updated.Spec.PatchID = 1

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(4), "4 EphemeralRunner should be created")

			for i := range 3 {
				updatedRunner := runnerList.Items[i].DeepCopy()
				updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
				err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[i]))
				Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
			}

			updatedRunner := runnerList.Items[3].DeepCopy()
			updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[3]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 4
			updated.Spec.PatchID = 2

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			Consistently(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(3), "only the running EphemeralRunners should remain after stale-patch cleanup")

			Consistently(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				12*time.Second,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(3), "EphemeralRunnerSet should not create a replacement before listener decrements")

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 3
			updated.Spec.PatchID = 3

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(3), "EphemeralRunnerSet should converge after listener decrements")
		})

		It("Should delete idle runners, keep busy runners, and create new runners when the spec changes", func() {
			ers := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated := ers.DeepCopy()
			updated.Spec.Replicas = 3
			updated.Spec.PatchID = 0
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(3), "3 EphemeralRunner should be created")

			idleRunnerNames := map[string]struct{}{}
			for i := range 2 {
				idleRunner := runnerList.Items[i].DeepCopy()
				idleRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
				idleRunner.Status.RunnerID = i + 101
				err = k8sClient.Status().Patch(ctx, idleRunner, client.MergeFrom(&runnerList.Items[i]))
				Expect(err).NotTo(HaveOccurred(), "failed to update idle EphemeralRunner")
				idleRunnerNames[idleRunner.Name] = struct{}{}
			}

			busyRunner := runnerList.Items[2].DeepCopy()
			busyRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
			busyRunner.Status.RunnerID = 103
			busyRunner.Status.JobID = "job-1"
			busyRunner.Status.WorkflowRunID = 9001
			err = k8sClient.Status().Patch(ctx, busyRunner, client.MergeFrom(&runnerList.Items[2]))
			Expect(err).NotTo(HaveOccurred(), "failed to update busy EphemeralRunner")

			busyRunnerName := busyRunner.Name

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to re-fetch EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image = "ghcr.io/actions/runner:new"
			updated.Spec.ActionableRevision = ers.Spec.ActionableRevision + 1
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to patch EphemeralRunnerSet with new spec")

			Eventually(
				func() error {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return err
					}

					if len(runnerList.Items) != 3 {
						return fmt.Errorf("expected 3 runners after spec update, got %d", len(runnerList.Items))
					}

					busyRunnerFound := false
					newSpecRunnerCount := 0
					for _, runner := range runnerList.Items {
						if _, ok := idleRunnerNames[runner.Name]; ok {
							return fmt.Errorf("expected idle runner %s to be deleted", runner.Name)
						}

						if runner.Name == busyRunnerName {
							busyRunnerFound = true
							if !runner.HasJob() {
								return fmt.Errorf("expected remaining runner to still be busy")
							}
							if runner.Spec.PodTemplateSpec.Spec.Containers[0].Image != "ghcr.io/actions/runner" {
								return fmt.Errorf("expected busy runner to keep original image, got %s", runner.Spec.PodTemplateSpec.Spec.Containers[0].Image)
							}
							continue
						}

						if len(runner.Spec.PodTemplateSpec.Spec.Containers) == 0 {
							return fmt.Errorf("new runner has empty container spec")
						}

						if runner.Spec.PodTemplateSpec.Spec.Containers[0].Image != "ghcr.io/actions/runner:new" {
							return fmt.Errorf("expected new runner image to be updated, got %s", runner.Spec.PodTemplateSpec.Spec.Containers[0].Image)
						}

						newSpecRunnerCount++
					}

					if !busyRunnerFound {
						return fmt.Errorf("expected busy runner %s to remain", busyRunnerName)
					}

					if newSpecRunnerCount != 2 {
						return fmt.Errorf("expected 2 runners with updated spec, got %d", newSpecRunnerCount)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "busy runner should stay while idle runners are replaced with the updated spec")
		})

		It("Should update status on Ephemeral Runner state changes", func() {
			created := new(v1alpha1.EphemeralRunnerSet)
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, created)
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(Succeed(), "EphemeralRunnerSet should be created")

			// Scale up the EphemeralRunnerSet
			updated := created.DeepCopy()
			updated.Spec.Replicas = 3
			err := k8sClient.Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet replica count")

			runnerList := new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (bool, error) {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return false, err
					}

					if len(runnerList.Items) != 3 {
						return false, err
					}

					var pendingOriginal *v1alpha1.EphemeralRunner
					var runningOriginal *v1alpha1.EphemeralRunner
					var failedOriginal *v1alpha1.EphemeralRunner
					var empty []*v1alpha1.EphemeralRunner
					for _, runner := range runnerList.Items {
						switch runner.Status.RunnerID {
						case 101:
							pendingOriginal = runner.DeepCopy()
						case 102:
							runningOriginal = runner.DeepCopy()
						case 103:
							failedOriginal = runner.DeepCopy()
						default:
							empty = append(empty, runner.DeepCopy())
						}
					}

					refetch := false
					if pendingOriginal == nil { // if NO pending
						refetch = true
						pendingOriginal = empty[0]
						empty = empty[1:]

						pending := pendingOriginal.DeepCopy()
						pending.Status.RunnerID = 101
						pending.Status.Phase = v1alpha1.EphemeralRunnerPhasePending

						err = k8sClient.Status().Patch(ctx, pending, client.MergeFrom(pendingOriginal))
						if err != nil {
							return false, err
						}
					}

					if runningOriginal == nil { // if NO running
						refetch = true
						runningOriginal = empty[0]
						empty = empty[1:]
						running := runningOriginal.DeepCopy()
						running.Status.RunnerID = 102
						running.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning

						err = k8sClient.Status().Patch(ctx, running, client.MergeFrom(runningOriginal))
						if err != nil {
							return false, err
						}
					}

					if failedOriginal == nil { // if NO failed
						refetch = true
						failedOriginal = empty[0]

						failed := pendingOriginal.DeepCopy()
						failed.Status.RunnerID = 103
						failed.Status.Phase = v1alpha1.EphemeralRunnerPhaseFailed

						err = k8sClient.Status().Patch(ctx, failed, client.MergeFrom(failedOriginal))
						if err != nil {
							return false, err
						}
					}

					return !refetch, nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeTrue(), "Failed to eventually update to one pending, one running and one failed")

			desiredStatus := v1alpha1.EphemeralRunnerSetStatus{
				Phase: v1alpha1.EphemeralRunnerSetPhaseRunning,
			}
			Eventually(
				func() (v1alpha1.EphemeralRunnerSetStatus, error) {
					updated := new(v1alpha1.EphemeralRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, updated)
					if err != nil {
						return v1alpha1.EphemeralRunnerSetStatus{}, err
					}
					return updated.Status, nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(desiredStatus), "Status is not eventually updated to the desired one")

			updated = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, updated)
			Expect(err).NotTo(HaveOccurred(), "Failed to fetch ephemeral runner set")

			updatedOriginal := updated.DeepCopy()
			updated.Spec.Replicas = 0

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(updatedOriginal))
			Expect(err).NotTo(HaveOccurred(), "Failed to patch ephemeral runner set with 0 replicas")

			Eventually(
				func() (int, error) {
					runnerList = new(v1alpha1.EphemeralRunnerList)
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}
					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "Failed to eventually scale down")

			desiredStatus = v1alpha1.EphemeralRunnerSetStatus{
				Phase: v1alpha1.EphemeralRunnerSetPhaseRunning,
			}

			Eventually(
				func() (v1alpha1.EphemeralRunnerSetStatus, error) {
					updated := new(v1alpha1.EphemeralRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, updated)
					if err != nil {
						return v1alpha1.EphemeralRunnerSetStatus{}, err
					}
					return updated.Status, nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(desiredStatus), "Status is not eventually updated to the desired one")

			err = k8sClient.Delete(ctx, &runnerList.Items[0])
			Expect(err).To(BeNil(), "Failed to delete failed ephemeral runner")

			desiredStatus = v1alpha1.EphemeralRunnerSetStatus{
				Phase: v1alpha1.EphemeralRunnerSetPhaseRunning,
			}
			Eventually(
				func() (v1alpha1.EphemeralRunnerSetStatus, error) {
					updated := new(v1alpha1.EphemeralRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, updated)
					if err != nil {
						return v1alpha1.EphemeralRunnerSetStatus{}, err
					}
					return updated.Status, nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(desiredStatus), "Status is not eventually updated to the desired one")
		})
	})
})

var _ = Describe("EphemeralRunner phase metrics", func() {
	var ctx context.Context
	var autoscalingNS *corev1.Namespace
	var mgr ctrl.Manager
	var configSecret *corev1.Secret
	var controller *EphemeralRunnerReconciler
	var ephemeralRunner *v1alpha1.EphemeralRunner
	var request ctrl.Request

	BeforeEach(func() {
		registerActionsGithubMetricsForTest.Do(func() {
			metrics.RegisterMetrics()
		})

		ephemeralRunnerPhaseMetrics.Lock()
		ephemeralRunnerPhaseMetrics.phases = map[types.NamespacedName]v1alpha1.EphemeralRunnerPhase{}
		ephemeralRunnerPhaseMetrics.Unlock()

		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller = &EphemeralRunnerReconciler{
			Client:         k8sClient,
			Scheme:         mgr.GetScheme(),
			Log:            logf.Log,
			PublishMetrics: true,
			ResourceBuilder: ResourceBuilder{
				ResourceCache: newTestResourceCache(),
				SecretResolver: secretresolver.New(k8sClient, fake.NewMultiClient(
					fake.WithClient(
						fake.NewClient(
							fake.WithGenerateJitRunnerConfig(
								&scaleset.RunnerScaleSetJitRunnerConfig{
									Runner:           &scaleset.RunnerReference{ID: 100, Name: "test-runner"},
									EncodedJITConfig: "fake-jit-config",
								},
								nil,
							),
						),
					),
				)),
			},
		}

		ephemeralRunner = &v1alpha1.EphemeralRunner{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-runner",
				Namespace: autoscalingNS.Name,
				Labels: map[string]string{
					LabelKeyGitHubScaleSetName:      "test-scale-set",
					LabelKeyGitHubScaleSetNamespace: autoscalingNS.Name,
				},
			},
			Spec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				RunnerScaleSetID:   100,
				PodTemplateSpec: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  v1alpha1.EphemeralRunnerContainerName,
								Image: "ghcr.io/actions/runner",
							},
						},
					},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunner)
		Expect(err).NotTo(HaveOccurred(), "failed to create ephemeral runner")

		request = ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ephemeralRunner.Namespace, Name: ephemeralRunner.Name}}
	})

	It("publishes pending, running, and succeeded phase transitions", func() {
		_, err := controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile ephemeral runner")

		pod := new(corev1.Pod)
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed(), "expected ephemeral runner pod to be created")

		podPending := pod.DeepCopy()
		podPending.Status.Phase = corev1.PodPending
		podPending.Status.ContainerStatuses = []corev1.ContainerStatus{
			{
				Name:  v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{},
			},
		}
		err = k8sClient.Status().Patch(ctx, podPending, client.MergeFrom(pod))
		Expect(err).NotTo(HaveOccurred(), "failed to patch pod to pending")

		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile pending pod")
		expectEphemeralRunnerPhase(ctx, ephemeralRunner, v1alpha1.EphemeralRunnerPhasePending)
		expectEphemeralRunnerPhaseMetric(ephemeralRunner, v1alpha1.EphemeralRunnerPhasePending, 1)
		expectEphemeralRunnerPhaseMetric(ephemeralRunner, v1alpha1.EphemeralRunnerPhaseRunning, 0)

		podRunning := podPending.DeepCopy()
		podRunning.Status.Phase = corev1.PodRunning
		podRunning.Status.ContainerStatuses = []corev1.ContainerStatus{
			{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
				},
			},
		}
		err = k8sClient.Status().Patch(ctx, podRunning, client.MergeFrom(podPending))
		Expect(err).NotTo(HaveOccurred(), "failed to patch pod to running")

		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile running pod")
		expectEphemeralRunnerPhase(ctx, ephemeralRunner, v1alpha1.EphemeralRunnerPhaseRunning)
		expectEphemeralRunnerPhaseMetric(ephemeralRunner, v1alpha1.EphemeralRunnerPhasePending, 0)
		expectEphemeralRunnerPhaseMetric(ephemeralRunner, v1alpha1.EphemeralRunnerPhaseRunning, 1)

		podSucceeded := podRunning.DeepCopy()
		podSucceeded.Status.Phase = corev1.PodSucceeded
		podSucceeded.Status.ContainerStatuses = []corev1.ContainerStatus{
			{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
				},
			},
		}
		err = k8sClient.Status().Patch(ctx, podSucceeded, client.MergeFrom(podRunning))
		Expect(err).NotTo(HaveOccurred(), "failed to patch pod to succeeded")

		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred(), "failed to reconcile succeeded pod")
		expectEphemeralRunnerPhase(ctx, ephemeralRunner, v1alpha1.EphemeralRunnerPhaseSucceeded)
		expectEphemeralRunnerPhaseMetric(ephemeralRunner, v1alpha1.EphemeralRunnerPhaseRunning, 0)
		expectEphemeralRunnerPhaseMetric(ephemeralRunner, v1alpha1.EphemeralRunnerPhaseSucceeded, 1)
	})
})

var _ = Describe("Test EphemeralRunnerSet actionable revision cleanup", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var configSecret *corev1.Secret

	newRunner := func(name string, ers *v1alpha1.EphemeralRunnerSet) *v1alpha1.EphemeralRunner {
		controllerRef := true
		return &v1alpha1.EphemeralRunner{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ers.Namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "EphemeralRunnerSet",
					Name:       ers.Name,
					UID:        ers.UID,
					Controller: &controllerRef,
				}},
			},
			Spec: ers.Spec.EphemeralRunnerSpec,
		}
	}

	waitForCachedActionableRevision := func(controller *EphemeralRunnerSetReconciler, key types.NamespacedName, specRevision, appliedRevision int64) {
		Eventually(func(g Gomega) {
			cached := new(v1alpha1.EphemeralRunnerSet)
			g.Expect(controller.Get(ctx, key, cached)).To(Succeed())
			g.Expect(cached.Spec.ActionableRevision).To(Equal(specRevision))
			g.Expect(cached.Status.AppliedActionableRevision).To(Equal(appliedRevision))
			g.Expect(cached.Finalizers).To(ContainElement(EphemeralRunnerSetFinalizerName))
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed())
	}

	waitForCachedRunningRunners := func(controller *EphemeralRunnerSetReconciler, namespace, owner string, expected int) {
		Eventually(func(g Gomega) {
			runners := new(v1alpha1.EphemeralRunnerList)
			g.Expect(controller.List(ctx, runners, client.InNamespace(namespace), client.MatchingFields{resourceOwnerKey: owner})).To(Succeed())
			state := newEphemeralRunnersByStates(runners)
			g.Expect(state.running).To(HaveLen(expected))
			for _, runner := range state.running {
				g.Expect(runner.Status.RunnerID).NotTo(BeZero())
			}
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)
		startManagers(GinkgoT(), mgr)
	})

	It("does not clean up runners on initial creation without an actionable revision", func() {
		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache: newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), fake.NewMultiClient(
					fake.WithClient(fake.NewClient(fake.WithRemoveRunner(nil))),
				)),
			},
		}

		ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-actionable-revision-initial", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec:    corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}}}},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred())

		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}}
		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		pendingRunner := newRunner("runner-pending-initial", ephemeralRunnerSet)
		err = k8sClient.Create(ctx, pendingRunner)
		Expect(err).NotTo(HaveOccurred())

		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		Consistently(func() error {
			runner := new(v1alpha1.EphemeralRunner)
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: autoscalingNS.Name, Name: pendingRunner.Name}, runner)
		}, time.Second, ephemeralRunnerSetTestInterval).Should(Succeed())

		Consistently(func() int64 {
			updatedSet := new(v1alpha1.EphemeralRunnerSet)
			if err := k8sClient.Get(ctx, request.NamespacedName, updatedSet); err != nil {
				return -1
			}
			return updatedSet.Status.AppliedActionableRevision
		}, time.Second, ephemeralRunnerSetTestInterval).Should(Equal(int64(0)))
	})

	It("deletes runner-a-idle, keeps runner-b-busy, and advances applied actionable revision 3 to 4", func() {
		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache: newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), fake.NewMultiClient(
					fake.WithClient(fake.NewClient(fake.WithRemoveRunner(nil))),
				)),
			},
		}

		ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-actionable-revision-success", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				ActionableRevision: 3,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec:    corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}}}},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred())

		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}}
		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := new(v1alpha1.EphemeralRunnerSet)
		err = k8sClient.Get(ctx, request.NamespacedName, current)
		Expect(err).NotTo(HaveOccurred())

		statusUpdated := current.DeepCopy()
		statusUpdated.Status.AppliedActionableRevision = 3
		statusUpdated.Status.Phase = v1alpha1.EphemeralRunnerSetPhaseRunning
		err = k8sClient.Status().Patch(ctx, statusUpdated, client.MergeFrom(current))
		Expect(err).NotTo(HaveOccurred())

		idleRunner := newRunner("runner-a-idle", statusUpdated)
		err = k8sClient.Create(ctx, idleRunner)
		Expect(err).NotTo(HaveOccurred())

		idleCurrent := new(v1alpha1.EphemeralRunner)
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(idleRunner), idleCurrent)
		Expect(err).NotTo(HaveOccurred())
		idleUpdated := idleCurrent.DeepCopy()
		idleUpdated.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
		idleUpdated.Status.RunnerID = 101
		err = k8sClient.Status().Patch(ctx, idleUpdated, client.MergeFrom(idleCurrent))
		Expect(err).NotTo(HaveOccurred())

		busyRunner := newRunner("runner-b-busy", statusUpdated)
		err = k8sClient.Create(ctx, busyRunner)
		Expect(err).NotTo(HaveOccurred())

		busyCurrent := new(v1alpha1.EphemeralRunner)
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(busyRunner), busyCurrent)
		Expect(err).NotTo(HaveOccurred())
		busyUpdated := busyCurrent.DeepCopy()
		busyUpdated.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
		busyUpdated.Status.RunnerID = 102
		busyUpdated.Status.JobID = "job-1"
		busyUpdated.Status.WorkflowRunID = 9001
		err = k8sClient.Status().Patch(ctx, busyUpdated, client.MergeFrom(busyCurrent))
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Get(ctx, request.NamespacedName, current)
		Expect(err).NotTo(HaveOccurred())
		specUpdated := current.DeepCopy()
		specUpdated.Spec.ActionableRevision = 4
		err = k8sClient.Patch(ctx, specUpdated, client.MergeFrom(current))
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			_, err := controller.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			runner := new(v1alpha1.EphemeralRunner)
			return kerrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Namespace: autoscalingNS.Name, Name: "runner-a-idle"}, runner))
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(BeTrue())

		Consistently(func() error {
			runner := new(v1alpha1.EphemeralRunner)
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: autoscalingNS.Name, Name: "runner-b-busy"}, runner); err != nil {
				return err
			}
			if runner.Status.RunnerID != 102 {
				return fmt.Errorf("expected busy runner ID 102, got %d", runner.Status.RunnerID)
			}
			if !runner.HasJob() {
				return fmt.Errorf("expected runner-b-busy to keep its assigned job")
			}
			return nil
		}, time.Second, ephemeralRunnerSetTestInterval).Should(Succeed())

		Eventually(func() int64 {
			_, err := controller.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			updatedSet := new(v1alpha1.EphemeralRunnerSet)
			if err := k8sClient.Get(ctx, request.NamespacedName, updatedSet); err != nil {
				return 0
			}
			return updatedSet.Status.AppliedActionableRevision
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Equal(int64(4)))
	})

	It("keeps applied actionable revision at 3 when cleanup fails", func() {
		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache: newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), fake.NewMultiClient(
					fake.WithClient(fake.NewClient(fake.WithRemoveRunner(fmt.Errorf("remove failed")))),
				)),
			},
		}

		ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-actionable-revision-error", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				ActionableRevision: 3,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec:    corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}}}},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred())

		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}}
		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := new(v1alpha1.EphemeralRunnerSet)
		err = k8sClient.Get(ctx, request.NamespacedName, current)
		Expect(err).NotTo(HaveOccurred())

		statusUpdated := current.DeepCopy()
		statusUpdated.Status.AppliedActionableRevision = 3
		err = k8sClient.Status().Patch(ctx, statusUpdated, client.MergeFrom(current))
		Expect(err).NotTo(HaveOccurred())

		idleRunner := newRunner("runner-a-idle", statusUpdated)
		err = k8sClient.Create(ctx, idleRunner)
		Expect(err).NotTo(HaveOccurred())

		idleCurrent := new(v1alpha1.EphemeralRunner)
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(idleRunner), idleCurrent)
		Expect(err).NotTo(HaveOccurred())
		idleUpdated := idleCurrent.DeepCopy()
		idleUpdated.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
		idleUpdated.Status.RunnerID = 101
		err = k8sClient.Status().Patch(ctx, idleUpdated, client.MergeFrom(idleCurrent))
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Get(ctx, request.NamespacedName, current)
		Expect(err).NotTo(HaveOccurred())
		specUpdated := current.DeepCopy()
		specUpdated.Spec.ActionableRevision = 4
		err = k8sClient.Patch(ctx, specUpdated, client.MergeFrom(current))
		Expect(err).NotTo(HaveOccurred())

		waitForCachedActionableRevision(controller, request.NamespacedName, 4, 3)
		waitForCachedRunningRunners(controller, autoscalingNS.Name, ephemeralRunnerSet.Name, 1)

		_, err = controller.Reconcile(ctx, request)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("remove failed"))

		Consistently(func() int64 {
			updatedSet := new(v1alpha1.EphemeralRunnerSet)
			if err := k8sClient.Get(ctx, request.NamespacedName, updatedSet); err != nil {
				return 0
			}
			return updatedSet.Status.AppliedActionableRevision
		}, time.Second, ephemeralRunnerSetTestInterval).Should(Equal(int64(3)))
	})

	It("deletes unregistered pending runner during actionable revision cleanup after restart with no cache", func() {
		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache:  newTestResourceCache(), // fresh empty cache simulating restart
				SecretResolver: secretresolver.New(mgr.GetClient(), fake.NewMultiClient()),
			},
		}

		ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-restart-no-cache", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				ActionableRevision: 4, // spec has been bumped
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec:    corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner:updated"}}}},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred())

		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}}
		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		current := new(v1alpha1.EphemeralRunnerSet)
		err = k8sClient.Get(ctx, request.NamespacedName, current)
		Expect(err).NotTo(HaveOccurred())

		statusUpdated := current.DeepCopy()
		statusUpdated.Status.AppliedActionableRevision = 3 // status is behind
		err = k8sClient.Status().Patch(ctx, statusUpdated, client.MergeFrom(current))
		Expect(err).NotTo(HaveOccurred())

		pendingRunner := newRunner("runner-restart-pending", statusUpdated)
		err = k8sClient.Create(ctx, pendingRunner)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			cachedSet := new(v1alpha1.EphemeralRunnerSet)
			err := controller.Get(ctx, request.NamespacedName, cachedSet)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cachedSet.Status.AppliedActionableRevision).To(Equal(int64(3)))

			cachedRunner := new(v1alpha1.EphemeralRunner)
			err = controller.Get(ctx, types.NamespacedName{Namespace: autoscalingNS.Name, Name: "runner-restart-pending"}, cachedRunner)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cachedRunner.Status.RunnerID).To(BeZero())
			g.Expect(cachedRunner.Status.Phase).To(BeEmpty())
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed())

		// Reconcile with fresh cache (simulating restart). Actionable revision cleanup deletes pending runners.
		Eventually(func() bool {
			_, err := controller.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			runner := new(v1alpha1.EphemeralRunner)
			return kerrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Namespace: autoscalingNS.Name, Name: "runner-restart-pending"}, runner))
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(BeTrue())

		// AppliedActionableRevision should advance after cleanup completes.
		Eventually(func() int64 {
			_, err := controller.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			updatedSet := new(v1alpha1.EphemeralRunnerSet)
			if err := k8sClient.Get(ctx, request.NamespacedName, updatedSet); err != nil {
				return 0
			}
			return updatedSet.Status.AppliedActionableRevision
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Equal(int64(4)))
	})

	It("preserves AppliedActionableRevision during status-only phase updates", func() {
		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache: newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), fake.NewMultiClient(
					fake.WithClient(fake.NewClient()),
				)),
			},
		}

		// Setup: Create ERS with an actionable revision
		ephemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{Name: "test-preserve-applied-revision", Namespace: autoscalingNS.Name},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				ActionableRevision: 5,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					PodTemplateSpec:    corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}}}},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred())

		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}}
		_, err = controller.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		// Set AppliedActionableRevision to 5
		current := new(v1alpha1.EphemeralRunnerSet)
		err = k8sClient.Get(ctx, request.NamespacedName, current)
		Expect(err).NotTo(HaveOccurred())

		statusUpdated := current.DeepCopy()
		statusUpdated.Status.AppliedActionableRevision = 5
		statusUpdated.Status.Phase = v1alpha1.EphemeralRunnerSetPhaseRunning
		err = k8sClient.Status().Patch(ctx, statusUpdated, client.MergeFrom(current))
		Expect(err).NotTo(HaveOccurred())

		// Create a runner that will cause phase change (outdated runner)
		ephemeralRunner := &v1alpha1.EphemeralRunner{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-runner-outdated",
				Namespace: autoscalingNS.Name,
				Labels: map[string]string{
					LabelKeyGitHubScaleSetName:      ephemeralRunnerSet.Name,
					LabelKeyGitHubScaleSetNamespace: ephemeralRunnerSet.Namespace,
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.GroupVersion.String(),
						Kind:               "EphemeralRunnerSet",
						Name:               ephemeralRunnerSet.Name,
						UID:                ephemeralRunnerSet.UID,
						Controller:         func(b bool) *bool { return &b }(true),
						BlockOwnerDeletion: func(b bool) *bool { return &b }(true),
					},
				},
			},
			Spec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				RunnerScaleSetID:   100,
				PodTemplateSpec:    corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner:old"}}}},
			},
		}
		err = k8sClient.Create(ctx, ephemeralRunner)
		Expect(err).NotTo(HaveOccurred())

		runnerStatusUpdated := ephemeralRunner.DeepCopy()
		runnerStatusUpdated.Status.Phase = v1alpha1.EphemeralRunnerPhaseOutdated
		runnerStatusUpdated.Status.RunnerID = 123
		runnerStatusUpdated.Status.JobRequestID = 456
		err = k8sClient.Status().Patch(ctx, runnerStatusUpdated, client.MergeFrom(ephemeralRunner))
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			cachedSet := new(v1alpha1.EphemeralRunnerSet)
			err := controller.Get(ctx, request.NamespacedName, cachedSet)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cachedSet.Status.AppliedActionableRevision).To(Equal(int64(5)))

			cachedRunner := new(v1alpha1.EphemeralRunner)
			err = controller.Get(ctx, types.NamespacedName{Namespace: autoscalingNS.Name, Name: "test-runner-outdated"}, cachedRunner)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cachedRunner.Status.Phase).To(Equal(v1alpha1.EphemeralRunnerPhaseOutdated))
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed())

		// Verify: Phase changed to Outdated, but AppliedActionableRevision preserved
		Eventually(func(g Gomega) {
			_, err := controller.Reconcile(ctx, request)
			g.Expect(err).NotTo(HaveOccurred())

			updatedSet := new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, request.NamespacedName, updatedSet)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(updatedSet.Status.Phase).To(Equal(v1alpha1.EphemeralRunnerSetPhaseOutdated), "phase should change to Outdated")
			g.Expect(updatedSet.Status.AppliedActionableRevision).To(Equal(int64(5)), "AppliedActionableRevision should be preserved")
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed())
	})
})

var _ = Describe("Test EphemeralRunnerSet controller with proxy settings", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet
	var configSecret *corev1.Secret

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache:  newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), multiclient.NewScaleset()),
			},
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)
	})

	It("should create a proxy secret and delete the proxy secreat after the runner-set is deleted", func() {
		secretCredentials := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "proxy-credentials",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"username": []byte("username"),
				"password": []byte("password"),
			},
		}

		err := k8sClient.Create(ctx, secretCredentials)
		Expect(err).NotTo(HaveOccurred(), "failed to create secret credentials")

		ephemeralRunnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "http://example.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					Proxy: &v1alpha1.ProxyConfig{
						HTTP: &v1alpha1.ProxyServerConfig{
							Url:                 "http://proxy.example.com",
							CredentialSecretRef: secretCredentials.Name,
						},
						HTTPS: &v1alpha1.ProxyServerConfig{
							Url:                 "https://proxy.example.com",
							CredentialSecretRef: secretCredentials.Name,
						},
						NoProxy: []string{"example.com", "example.org"},
					},
					PodTemplateSpec: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "runner",
									Image: "ghcr.io/actions/runner",
								},
							},
						},
					},
				},
			},
		}

		err = k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create EphemeralRunnerSet")

		Eventually(
			func(g Gomega) {
				// Compiled / flattened proxy secret should exist at this point
				actualProxySecret := &corev1.Secret{}
				err = k8sClient.Get(ctx, client.ObjectKey{
					Namespace: autoscalingNS.Name,
					Name:      proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
				}, actualProxySecret)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get compiled / flattened proxy secret")

				secretFetcher := func(name string) (*corev1.Secret, error) {
					secret := &corev1.Secret{}
					err = k8sClient.Get(ctx, client.ObjectKey{
						Namespace: autoscalingNS.Name,
						Name:      name,
					}, secret)
					return secret, err
				}

				// Assert that the proxy secret is created with the correct values
				expectedData, err := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(secretFetcher)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get proxy secret data")
				g.Expect(actualProxySecret.Data).To(Equal(expectedData))
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(Succeed(), "compiled / flattened proxy secret should exist")

		Eventually(func(g Gomega) {
			runnerList := new(v1alpha1.EphemeralRunnerList)
			err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
			g.Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunners")

			for _, runner := range runnerList.Items {
				g.Expect(runner.Spec.ProxySecretRef).To(Equal(proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet)))
			}
		}, ephemeralRunnerSetTestTimeout, ephemeralRunnerSetTestInterval).Should(Succeed(), "EphemeralRunners should have a reference to the proxy secret")

		// patch ephemeral runner set to have 0 replicas
		patch := client.MergeFrom(ephemeralRunnerSet.DeepCopy())
		ephemeralRunnerSet.Spec.Replicas = 0
		err = k8sClient.Patch(ctx, ephemeralRunnerSet, patch)
		Expect(err).NotTo(HaveOccurred(), "failed to patch EphemeralRunnerSet")

		// Set pods to PodSucceeded to simulate an actual EphemeralRunner stopping
		Eventually(
			func(g Gomega) (int, error) {
				runnerList := new(v1alpha1.EphemeralRunnerList)
				err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
				if err != nil {
					return -1, err
				}

				// Set status to simulate a configured EphemeralRunner
				refetch := false
				for i, runner := range runnerList.Items {
					if runner.Status.RunnerID == 0 {
						updatedRunner := runner.DeepCopy()
						updatedRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
						updatedRunner.Status.RunnerID = i + 100
						err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runner))
						Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
						refetch = true
					}
				}

				if refetch {
					err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
					if err != nil {
						return -1, err
					}
				}

				return len(runnerList.Items), nil
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(BeEquivalentTo(1), "1 EphemeralRunner should exist")

		// Delete the EphemeralRunnerSet
		err = k8sClient.Delete(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to delete EphemeralRunnerSet")

		Eventually(
			func(g Gomega) (int, error) {
				runnerList := new(v1alpha1.EphemeralRunnerList)
				err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
				if err != nil {
					return -1, err
				}
				return len(runnerList.Items), nil
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(BeEquivalentTo(0), "EphemeralRunners should be deleted")

		// Assert that the proxy secret is deleted
		Eventually(
			func(g Gomega) {
				proxySecret := &corev1.Secret{}
				err = k8sClient.Get(ctx, client.ObjectKey{
					Namespace: autoscalingNS.Name,
					Name:      proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
				}, proxySecret)
				g.Expect(err).To(HaveOccurred(), "proxy secret should be deleted")
				g.Expect(kerrors.IsNotFound(err)).To(BeTrue(), "proxy secret should be deleted")
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(Succeed(), "proxy secret should be deleted")
	})

	It("should configure the actions client to use proxy details", func() {
		secretCredentials := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "proxy-credentials",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"username": []byte("test"),
				"password": []byte("password"),
			},
		}

		err := k8sClient.Create(ctx, secretCredentials)
		Expect(err).NotTo(HaveOccurred(), "failed to create secret credentials")

		proxySuccessfulllyCalled := false
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Proxy-Authorization")
			Expect(header).NotTo(BeEmpty())

			header = strings.TrimPrefix(header, "Basic ")
			decoded, err := base64.StdEncoding.DecodeString(header)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(decoded)).To(Equal("test:password"))

			proxySuccessfulllyCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		GinkgoT().Cleanup(func() {
			proxy.Close()
		})

		ephemeralRunnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "http://example.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetID:   100,
					Proxy: &v1alpha1.ProxyConfig{
						HTTP: &v1alpha1.ProxyServerConfig{
							Url:                 proxy.URL,
							CredentialSecretRef: "proxy-credentials",
						},
					},
					PodTemplateSpec: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "runner",
									Image: "ghcr.io/actions/runner",
								},
							},
						},
					},
				},
			},
		}

		err = k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create EphemeralRunnerSet")

		runnerList := new(v1alpha1.EphemeralRunnerList)
		Eventually(
			func() (int, error) {
				err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
				if err != nil {
					return -1, err
				}

				return len(runnerList.Items), nil
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(BeEquivalentTo(1), "failed to create ephemeral runner")

		runner := runnerList.Items[0].DeepCopy()
		runner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
		runner.Status.RunnerID = 100
		err = k8sClient.Status().Patch(ctx, runner, client.MergeFrom(&runnerList.Items[0]))
		Expect(err).NotTo(HaveOccurred(), "failed to update ephemeral runner status")

		runnerSet := new(v1alpha1.EphemeralRunnerSet)
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}, runnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

		updatedRunnerSet := runnerSet.DeepCopy()
		updatedRunnerSet.Spec.Replicas = 0
		err = k8sClient.Patch(ctx, updatedRunnerSet, client.MergeFrom(runnerSet))
		Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

		Eventually(
			func() bool {
				return proxySuccessfulllyCalled
			},
			2*time.Second,
			ephemeralRunnerInterval,
		).Should(BeEquivalentTo(true))
	})
})

var _ = Describe("Test EphemeralRunnerSet controller with custom root CA", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet
	var configSecret *corev1.Secret
	var rootCAConfigMap *corev1.ConfigMap

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		cert, err := os.ReadFile(filepath.Join(
			"../../",
			"github",
			"actions",
			"testdata",
			"rootCA.crt",
		))
		Expect(err).NotTo(HaveOccurred(), "failed to read root CA cert")
		rootCAConfigMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "root-ca-configmap",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string]string{
				"rootCA.crt": string(cert),
			},
		}
		err = k8sClient.Create(ctx, rootCAConfigMap)
		Expect(err).NotTo(HaveOccurred(), "failed to create configmap with root CAs")

		controller := &EphemeralRunnerSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
			ResourceBuilder: ResourceBuilder{
				ResourceCache:  newTestResourceCache(),
				SecretResolver: secretresolver.New(mgr.GetClient(), multiclient.NewScaleset()),
			},
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)
	})

	It("should be able to make requests to a server using root CAs", func() {
		ephemeralRunnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL:    "https://github.example.com/api/v3",
					GitHubConfigSecret: configSecret.Name,
					GitHubServerTLS: &v1alpha1.TLSConfig{
						CertificateFrom: &v1alpha1.TLSCertificateSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: rootCAConfigMap.Name,
								},
								Key: "rootCA.crt",
							},
						},
					},
					RunnerScaleSetID: 100,
					PodTemplateSpec: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "runner",
									Image: "ghcr.io/actions/runner",
								},
							},
						},
					},
				},
			},
		}

		err := k8sClient.Create(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create EphemeralRunnerSet")

		runnerList := new(v1alpha1.EphemeralRunnerList)
		Eventually(
			func() (int, error) {
				err := listEphemeralRunnersAndRemoveFinalizers(ctx, k8sClient, runnerList, ephemeralRunnerSet.Namespace)
				if err != nil {
					return -1, err
				}

				return len(runnerList.Items), nil
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(BeEquivalentTo(1), "failed to create ephemeral runner")

		// Verify that the TLS configuration is properly propagated to the runner
		runner := runnerList.Items[0].DeepCopy()
		Expect(runner.Spec.GitHubServerTLS).NotTo(BeNil(), "runner tls config should not be nil")
		Expect(runner.Spec.GitHubServerTLS).To(BeEquivalentTo(ephemeralRunnerSet.Spec.EphemeralRunnerSpec.GitHubServerTLS), "runner tls config should be correct")
	})
})

// helper function to remove ephemeral runners since in the test, ephemeral runner reconciler is not started
func listEphemeralRunnersAndRemoveFinalizers(ctx context.Context, k8sClient client.Client, list *v1alpha1.EphemeralRunnerList, namespace string) error {
	err := k8sClient.List(ctx, list, client.InNamespace(namespace))
	if err != nil {
		return err
	}

	// Since we are not starting ephemeral runner reconciler, ignore
	liveItems := make([]v1alpha1.EphemeralRunner, 0)
	for _, item := range list.Items {
		if !item.DeletionTimestamp.IsZero() {
			original := item.DeepCopy()
			item.Finalizers = []string{}
			if err := k8sClient.Patch(ctx, &item, client.MergeFrom(original)); err != nil {
				return err
			}
			continue
		}
		liveItems = append(liveItems, item)
	}
	list.Items = liveItems
	return nil
}
