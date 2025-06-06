package actionsgithubcom

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/fake"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
)

const (
	ephemeralRunnerSetTestTimeout     = time.Second * 20
	ephemeralRunnerSetTestInterval    = time.Millisecond * 250
	ephemeralRunnerSetTestGitHubToken = "gh_token"
)

func TestPrecomputedConstants(t *testing.T) {
	require.Equal(t, len(failedRunnerBackoff), maxFailures+1)
}

var _ = Describe("Test EphemeralRunnerSet controller", func() {
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
				SecretResolver: &SecretResolver{
					k8sClient:   mgr.GetClient(),
					multiClient: fake.NewMultiClient(),
				},
			},
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		ephemeralRunnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigUrl:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetId:   100,
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
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(ephemeralRunnerSetFinalizerName), "EphemeralRunnerSet should have a finalizer")

			// Check if the number of ephemeral runners are stay 0
			Consistently(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(0), "No EphemeralRunner should be created")

			// Check if the status stay 0
			Consistently(
				func() (int, error) {
					runnerSet := new(v1alpha1.EphemeralRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, runnerSet)
					if err != nil {
						return -1, err
					}

					return int(runnerSet.Status.CurrentReplicas), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(0), "EphemeralRunnerSet status should be 0")

			// Scaling up the EphemeralRunnerSet
			updated := created.DeepCopy()
			updated.Spec.Replicas = 5
			err := k8sClient.Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Check if the number of ephemeral runners are created
			Eventually(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					// Set status to simulate a configured EphemeralRunner
					refetch := false
					for i, runner := range runnerList.Items {
						if runner.Status.RunnerId == 0 {
							updatedRunner := runner.DeepCopy()
							updatedRunner.Status.Phase = corev1.PodRunning
							updatedRunner.Status.RunnerId = i + 100
							err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runner))
							Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
							refetch = true
						}
					}

					if refetch {
						err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
						if err != nil {
							return -1, err
						}
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")

			// Check if the status is updated
			Eventually(
				func() (int, error) {
					runnerSet := new(v1alpha1.EphemeralRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, runnerSet)
					if err != nil {
						return -1, err
					}

					return int(runnerSet.Status.CurrentReplicas), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(5), "EphemeralRunnerSet status should be 5")
		})
	})

	Context("When deleting a new EphemeralRunnerSet", func() {
		It("It should cleanup all resources for a deleting EphemeralRunnerSet before removing it", func() {
			created := new(v1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, created)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			// Scale up the EphemeralRunnerSet
			updated := created.DeepCopy()
			updated.Spec.Replicas = 5
			err = k8sClient.Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Wait for the EphemeralRunnerSet to be scaled up
			Eventually(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					// Set status to simulate a configured EphemeralRunner
					refetch := false
					for i, runner := range runnerList.Items {
						if runner.Status.RunnerId == 0 {
							updatedRunner := runner.DeepCopy()
							updatedRunner.Status.Phase = corev1.PodRunning
							updatedRunner.Status.RunnerId = i + 100
							err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runner))
							Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
							refetch = true
						}
					}

					if refetch {
						err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
						if err != nil {
							return -1, err
						}
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")

			// Delete the EphemeralRunnerSet
			err = k8sClient.Delete(ctx, created)
			Expect(err).NotTo(HaveOccurred(), "failed to delete EphemeralRunnerSet")

			// Check if all ephemeral runners are deleted
			Eventually(
				func() (int, error) {
					runnerList := new(v1alpha1.EphemeralRunnerList)
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(0), "All EphemeralRunner should be deleted")

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
				ephemeralRunnerSetTestInterval).Should(Succeed(), "EphemeralRunnerSet should be deleted")
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodRunning
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Keep the ephemeral runner until the next patch
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodPending
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// confirm they are not deleted
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Consistently(
				func() (int, error) {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodRunning

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
			// We should have 3 runners, and have no Succeeded ones
			Eventually(
				func() error {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}

					if len(runnerList.Items) != 3 {
						return fmt.Errorf("Expected 3 runners, got %d", len(runnerList.Items))
					}

					for _, runner := range runnerList.Items {
						if runner.Status.Phase == corev1.PodSucceeded {
							return fmt.Errorf("Runner %s is in Succeeded phase", runner.Name)
						}
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			updatedRunner := runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodPending
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for these statuses to actually be updated
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}
					pending := 0
					succeeded := 0
					for _, runner := range runnerList.Items {
						switch runner.Status.Phase {
						case corev1.PodSucceeded:
							succeeded++
						case corev1.PodPending:
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}

					if len(runnerList.Items) != 1 {
						return fmt.Errorf("Expected 1 runner, got %d", len(runnerList.Items))
					}

					if runnerList.Items[0].Status.Phase != corev1.PodPending {
						return fmt.Errorf("Expected runner to be in Pending, got %s", runnerList.Items[0].Status.Phase)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "1 EphemeralRunner should be created and in Pending phase")

			// Now, the ephemeral runner finally is done and we can scale down to 0
			updatedRunner = runnerList.Items[0].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			Eventually(
				func() (int, error) {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
			updatedRunner.Status.Phase = corev1.PodPending
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodRunning
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for these statuses to actually be updated
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}

					pending := 0
					running := 0

					for _, runner := range runnerList.Items {
						switch runner.Status.Phase {
						case corev1.PodPending:
							pending++
						case corev1.PodRunning:
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be up since they don't have an ID yet")

			// Now, let's say ephemeral runner controller patched these ephemeral runners with the registration.

			updatedRunner = runnerList.Items[0].DeepCopy()
			updatedRunner.Status.RunnerId = 1
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.RunnerId = 2
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Now, eventually, they should be deleted
			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
			updatedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			updatedRunner = runnerList.Items[1].DeepCopy()
			updatedRunner.Status.Phase = corev1.PodRunning
			err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for these statuses to actually be updated

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}

					succeeded := 0
					running := 0

					for _, runner := range runnerList.Items {
						switch runner.Status.Phase {
						case corev1.PodSucceeded:
							succeeded++
						case corev1.PodRunning:
							running++
						}
					}

					if succeeded != 1 && running != 1 {
						return fmt.Errorf("Expected 1 runner in Succeeded and 1 in Running, got %d in Succeeded and %d in Running", succeeded, running)
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "1 EphemeralRunner should be in Succeeded and 1 in Running phase")

			// Now, let's simulate replacement. The desired count is still 2.
			// This simulates that we got 1 job assigned, and 1 job completed.

			ers = new(v1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, ers)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			updated = ers.DeepCopy()
			updated.Spec.Replicas = 2
			updated.Spec.PatchID = 2

			err = k8sClient.Patch(ctx, updated, client.MergeFrom(ers))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			runnerList = new(v1alpha1.EphemeralRunnerList)
			Eventually(
				func() error {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}

					if len(runnerList.Items) != 2 {
						return fmt.Errorf("Expected 2 runners, got %d", len(runnerList.Items))
					}

					for _, runner := range runnerList.Items {
						if runner.Status.Phase == corev1.PodSucceeded {
							return fmt.Errorf("Expected no runners in Succeeded phase, got one")
						}
					}

					return nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeNil(), "2 EphemeralRunner should be created and none should be in Succeeded phase")
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
						switch runner.Status.RunnerId {
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
						pending.Status.RunnerId = 101
						pending.Status.Phase = corev1.PodPending

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
						running.Status.RunnerId = 102
						running.Status.Phase = corev1.PodRunning

						err = k8sClient.Status().Patch(ctx, running, client.MergeFrom(runningOriginal))
						if err != nil {
							return false, err
						}
					}

					if failedOriginal == nil { // if NO failed
						refetch = true
						failedOriginal = empty[0]

						failed := pendingOriginal.DeepCopy()
						failed.Status.RunnerId = 103
						failed.Status.Phase = corev1.PodFailed

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
				CurrentReplicas:         3,
				PendingEphemeralRunners: 1,
				RunningEphemeralRunners: 1,
				FailedEphemeralRunners:  1,
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
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}
					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "Failed to eventually scale down")

			desiredStatus = v1alpha1.EphemeralRunnerSetStatus{
				CurrentReplicas:         1,
				PendingEphemeralRunners: 0,
				RunningEphemeralRunners: 0,
				FailedEphemeralRunners:  1,
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

			desiredStatus = v1alpha1.EphemeralRunnerSetStatus{} // empty
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
				SecretResolver: &SecretResolver{
					k8sClient:   mgr.GetClient(),
					multiClient: actions.NewMultiClient(logr.Discard()),
				},
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
					GitHubConfigUrl:    "http://example.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetId:   100,
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

		Eventually(func(g Gomega) {
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
			err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
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
				err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
				if err != nil {
					return -1, err
				}

				// Set status to simulate a configured EphemeralRunner
				refetch := false
				for i, runner := range runnerList.Items {
					if runner.Status.RunnerId == 0 {
						updatedRunner := runner.DeepCopy()
						updatedRunner.Status.Phase = corev1.PodSucceeded
						updatedRunner.Status.RunnerId = i + 100
						err = k8sClient.Status().Patch(ctx, updatedRunner, client.MergeFrom(&runner))
						Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")
						refetch = true
					}
				}

				if refetch {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}
				}

				return len(runnerList.Items), nil
			},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval).Should(BeEquivalentTo(1), "1 EphemeralRunner should exist")

		// Delete the EphemeralRunnerSet
		err = k8sClient.Delete(ctx, ephemeralRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to delete EphemeralRunnerSet")

		// Assert that the proxy secret is deleted
		Eventually(func(g Gomega) {
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
					GitHubConfigUrl:    "http://example.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					RunnerScaleSetId:   100,
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
		Eventually(func() (int, error) {
			err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
			if err != nil {
				return -1, err
			}

			return len(runnerList.Items), nil
		},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(BeEquivalentTo(1), "failed to create ephemeral runner")

		runner := runnerList.Items[0].DeepCopy()
		runner.Status.Phase = corev1.PodRunning
		runner.Status.RunnerId = 100
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
				SecretResolver: &SecretResolver{
					k8sClient:   mgr.GetClient(),
					multiClient: actions.NewMultiClient(logr.Discard()),
				},
			},
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)
	})

	It("should be able to make requests to a server using root CAs", func() {
		certsFolder := filepath.Join(
			"../../",
			"github",
			"actions",
			"testdata",
		)
		certPath := filepath.Join(certsFolder, "server.crt")
		keyPath := filepath.Join(certsFolder, "server.key")

		serverSuccessfullyCalled := false
		server := testserver.NewUnstarted(GinkgoT(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverSuccessfullyCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		Expect(err).NotTo(HaveOccurred(), "failed to load server cert")

		server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
		server.StartTLS()

		ephemeralRunnerSet = &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigUrl:    server.ConfigURLForOrg("my-org"),
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
					RunnerScaleSetId: 100,
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
		Eventually(func() (int, error) {
			err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
			if err != nil {
				return -1, err
			}

			return len(runnerList.Items), nil
		},
			ephemeralRunnerSetTestTimeout,
			ephemeralRunnerSetTestInterval,
		).Should(BeEquivalentTo(1), "failed to create ephemeral runner")

		runner := runnerList.Items[0].DeepCopy()
		Expect(runner.Spec.GitHubServerTLS).NotTo(BeNil(), "runner tls config should not be nil")
		Expect(runner.Spec.GitHubServerTLS).To(BeEquivalentTo(ephemeralRunnerSet.Spec.EphemeralRunnerSpec.GitHubServerTLS), "runner tls config should be correct")

		runner.Status.Phase = corev1.PodRunning
		runner.Status.RunnerId = 100
		err = k8sClient.Status().Patch(ctx, runner, client.MergeFrom(&runnerList.Items[0]))
		Expect(err).NotTo(HaveOccurred(), "failed to update ephemeral runner status")

		currentRunnerSet := new(v1alpha1.EphemeralRunnerSet)
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: ephemeralRunnerSet.Namespace, Name: ephemeralRunnerSet.Name}, currentRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

		updatedRunnerSet := currentRunnerSet.DeepCopy()
		updatedRunnerSet.Spec.Replicas = 0
		err = k8sClient.Patch(ctx, updatedRunnerSet, client.MergeFrom(currentRunnerSet))
		Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

		// wait for server to be called
		Eventually(
			func() bool {
				return serverSuccessfullyCalled
			},
			autoscalingRunnerSetTestTimeout,
			1*time.Nanosecond,
		).Should(BeTrue(), "server was not called")
	})
})
