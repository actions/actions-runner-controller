package actionsgithubcom

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/fake"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
)

const (
	ephemeralRunnerSetTestTimeout     = time.Second * 10
	ephemeralRunnerSetTestInterval    = time.Millisecond * 250
	ephemeralRunnerSetTestGitHubToken = "gh_token"
)

var _ = Describe("Test EphemeralRunnerSet controller", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var ephemeralRunnerSet *actionsv1alpha1.EphemeralRunnerSet
	var configSecret *corev1.Secret

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &EphemeralRunnerSetReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			Log:           logf.Log,
			ActionsClient: fake.NewMultiClient(),
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		ephemeralRunnerSet = &actionsv1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: actionsv1alpha1.EphemeralRunnerSetSpec{
				EphemeralRunnerSpec: actionsv1alpha1.EphemeralRunnerSpec{
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
			created := new(actionsv1alpha1.EphemeralRunnerSet)
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
					runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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
					runnerSet := new(actionsv1alpha1.EphemeralRunnerSet)
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
					runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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
					runnerSet := new(actionsv1alpha1.EphemeralRunnerSet)
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
			created := new(actionsv1alpha1.EphemeralRunnerSet)
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
					runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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
					runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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
					deleted := new(actionsv1alpha1.EphemeralRunnerSet)
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
		It("Should scale only on patch ID change", func() {
			created := new(actionsv1alpha1.EphemeralRunnerSet)
			err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, created)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")

			patchID := 1

			// Scale up the EphemeralRunnerSet
			updated := created.DeepCopy()
			updated.Spec.Replicas = 5
			updated.Spec.PatchID = patchID
			err = k8sClient.Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Wait for the EphemeralRunnerSet to be scaled up
			runnerList := new(actionsv1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
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
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(5), "5 EphemeralRunner should be created")

			// Mark one of the EphemeralRunner as finished
			finishedRunner := runnerList.Items[4].DeepCopy()
			finishedRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, finishedRunner, client.MergeFrom(&runnerList.Items[4]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Wait for the finished EphemeralRunner to be set to succeeded
			Eventually(
				func() error {
					runnerList := new(actionsv1alpha1.EphemeralRunnerList)
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return err
					}

					for _, runner := range runnerList.Items {
						if runner.Name != finishedRunner.Name {
							continue
						}

						if runner.Status.Phase != corev1.PodSucceeded {
							return fmt.Errorf("EphemeralRunner is not finished")
						}
						// found pod succeeded
						return nil
					}

					return errors.New("Finished ephemeral runner is not found")
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(Succeed(), "Finished EphemeralRunner should be deleted")

			// After one ephemeral runner is finished, simulate job done patch
			patchID++
			original := new(actionsv1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, original)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")
			updated = original.DeepCopy()
			updated.Spec.PatchID = patchID
			updated.Spec.Replicas = 4
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(original))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Only finished ephemeral runner should be deleted
			runnerList = new(actionsv1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
					err := k8sClient.List(ctx, runnerList, client.InNamespace(ephemeralRunnerSet.Namespace))
					if err != nil {
						return -1, err
					}

					for _, runner := range runnerList.Items {
						if runner.Status.Phase == corev1.PodSucceeded {
							return -1, fmt.Errorf("Finished EphemeralRunner should be deleted")
						}
					}

					return len(runnerList.Items), nil
				},
				ephemeralRunnerSetTestTimeout,
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(4), "4 EphemeralRunner should be created")

			// Scaling down the EphemeralRunnerSet
			patchID++
			original = new(actionsv1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, original)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")
			updated = original.DeepCopy()
			updated.Spec.PatchID = patchID
			updated.Spec.Replicas = 3
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(original))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Wait for the EphemeralRunnerSet to be scaled down
			runnerList = new(actionsv1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
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
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(3), "3 EphemeralRunner should be created")

			// We will not scale down runner that is running jobs
			runningRunner := runnerList.Items[0].DeepCopy()
			runningRunner.Status.JobRequestId = 1000
			err = k8sClient.Status().Patch(ctx, runningRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			runningRunner = runnerList.Items[1].DeepCopy()
			runningRunner.Status.JobRequestId = 1001
			err = k8sClient.Status().Patch(ctx, runningRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			// Scale down to 1 while 2 are running
			patchID++
			original = new(actionsv1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, original)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")
			updated = original.DeepCopy()
			updated.Spec.PatchID = patchID
			updated.Spec.Replicas = 1
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(original))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Wait for the EphemeralRunnerSet to be scaled down to 2 since we still have 2 runner running jobs
			runnerList = new(actionsv1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
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
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			// We will not scale down failed runner
			failedRunner := runnerList.Items[0].DeepCopy()
			failedRunner.Status.Phase = corev1.PodFailed
			err = k8sClient.Status().Patch(ctx, failedRunner, client.MergeFrom(&runnerList.Items[0]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			runnerList = new(actionsv1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
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
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(2), "2 EphemeralRunner should be created")

			// We will scale down to 0 when the running job is completed and the failed runner is deleted
			runningRunner = runnerList.Items[1].DeepCopy()
			runningRunner.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Patch(ctx, runningRunner, client.MergeFrom(&runnerList.Items[1]))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunner")

			err = k8sClient.Delete(ctx, &runnerList.Items[0])
			Expect(err).NotTo(HaveOccurred(), "failed to delete EphemeralRunner")

			// Scale down to 0 while 1 ephemeral runner is failed
			patchID++
			original = new(actionsv1alpha1.EphemeralRunnerSet)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunnerSet.Name, Namespace: ephemeralRunnerSet.Namespace}, original)
			Expect(err).NotTo(HaveOccurred(), "failed to get EphemeralRunnerSet")
			updated = original.DeepCopy()
			updated.Spec.PatchID = patchID
			updated.Spec.Replicas = 0
			err = k8sClient.Patch(ctx, updated, client.MergeFrom(original))
			Expect(err).NotTo(HaveOccurred(), "failed to update EphemeralRunnerSet")

			// Wait for the EphemeralRunnerSet to be scaled down to 0
			runnerList = new(actionsv1alpha1.EphemeralRunnerList)
			Eventually(
				func() (int, error) {
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
				ephemeralRunnerSetTestInterval,
			).Should(BeEquivalentTo(0), "0 EphemeralRunner should be created")
		})

		It("Should update status on Ephemeral Runner state changes", func() {
			created := new(actionsv1alpha1.EphemeralRunnerSet)
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

			runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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
					runnerList = new(actionsv1alpha1.EphemeralRunnerList)
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
	var ephemeralRunnerSet *actionsv1alpha1.EphemeralRunnerSet
	var configSecret *corev1.Secret

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &EphemeralRunnerSetReconciler{
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			Log:           logf.Log,
			ActionsClient: actions.NewMultiClient(logr.Discard()),
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

		ephemeralRunnerSet = &actionsv1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: actionsv1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: actionsv1alpha1.EphemeralRunnerSpec{
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
			runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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
				runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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

		ephemeralRunnerSet = &actionsv1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: actionsv1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: actionsv1alpha1.EphemeralRunnerSpec{
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

		runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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

		runnerSet := new(actionsv1alpha1.EphemeralRunnerSet)
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
			interval,
		).Should(BeEquivalentTo(true))
	})
})

var _ = Describe("Test EphemeralRunnerSet controller with custom root CA", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var ephemeralRunnerSet *actionsv1alpha1.EphemeralRunnerSet
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
			Client:        mgr.GetClient(),
			Scheme:        mgr.GetScheme(),
			Log:           logf.Log,
			ActionsClient: actions.NewMultiClient(logr.Discard()),
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

		ephemeralRunnerSet = &actionsv1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: actionsv1alpha1.EphemeralRunnerSetSpec{
				Replicas: 1,
				EphemeralRunnerSpec: actionsv1alpha1.EphemeralRunnerSpec{
					GitHubConfigUrl:    server.ConfigURLForOrg("my-org"),
					GitHubConfigSecret: configSecret.Name,
					GitHubServerTLS: &actionsv1alpha1.GitHubServerTLSConfig{
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

		runnerList := new(actionsv1alpha1.EphemeralRunnerList)
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

		currentRunnerSet := new(actionsv1alpha1.EphemeralRunnerSet)
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
