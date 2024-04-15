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
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/github/actions/fake"
	"github.com/actions/actions-runner-controller/github/actions/testserver"
)

const (
	autoscalingRunnerSetTestTimeout     = time.Second * 5
	autoscalingRunnerSetTestInterval    = time.Millisecond * 250
	autoscalingRunnerSetTestGitHubToken = "gh_token"
)

var _ = Describe("Test AutoScalingRunnerSet controller", Ordered, func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var controller *AutoscalingRunnerSetReconciler
	var autoscalingNS *corev1.Namespace
	var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	var configSecret *corev1.Secret

	var originalBuildVersion string
	buildVersion := "0.1.0"

	BeforeAll(func() {
		originalBuildVersion = build.Version
		build.Version = buildVersion
	})

	AfterAll(func() {
		build.Version = originalBuildVersion
	})

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller = &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoscalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ActionsClient:                      fake.NewMultiClient(),
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		min := 1
		max := 10
		autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
				Labels: map[string]string{
					LabelKeyKubernetesVersion: buildVersion,
				},
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &max,
				MinRunners:         &min,
				RunnerGroup:        "testgroup",
				Template: corev1.PodTemplateSpec{
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
		}

		err = k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		startManagers(GinkgoT(), mgr)
	})

	Context("When creating a new AutoScalingRunnerSet", func() {
		It("It should create/add all required resources for a new AutoScalingRunnerSet (finalizer, runnerscaleset, ephemeralrunnerset, listener)", func() {
			// Check if finalizer is added
			created := new(v1alpha1.AutoscalingRunnerSet)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo(autoscalingRunnerSetFinalizerName), "AutoScalingRunnerSet should have a finalizer")

			// Check if runner scale set is created on service
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, created)
					if err != nil {
						return "", err
					}

					if _, ok := created.Annotations[runnerScaleSetIdAnnotationKey]; !ok {
						return "", nil
					}

					if _, ok := created.Annotations[AnnotationKeyGitHubRunnerGroupName]; !ok {
						return "", nil
					}

					return fmt.Sprintf("%s_%s", created.Annotations[runnerScaleSetIdAnnotationKey], created.Annotations[AnnotationKeyGitHubRunnerGroupName]), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo("1_testgroup"), "RunnerScaleSet should be created/fetched and update the AutoScalingRunnerSet's annotation")

			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, created)
					if err != nil {
						return "", err
					}

					if _, ok := created.Labels[LabelKeyGitHubOrganization]; !ok {
						return "", nil
					}

					if _, ok := created.Labels[LabelKeyGitHubRepository]; !ok {
						return "", nil
					}

					return fmt.Sprintf("%s/%s", created.Labels[LabelKeyGitHubOrganization], created.Labels[LabelKeyGitHubRepository]), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo("owner/repo"), "RunnerScaleSet should be created/fetched and update the AutoScalingRunnerSet's label")

			// Check if ephemeral runner set is created
			Eventually(
				func() (int, error) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					if err != nil {
						return 0, err
					}

					return len(runnerSetList.Items), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo(1), "Only one EphemeralRunnerSet should be created")

			// Check if listener is created
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, new(v1alpha1.AutoscalingListener))
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			// Check if status is updated
			runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
			err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(BeEquivalentTo(1), "Only one EphemeralRunnerSet should be created")
		})
	})

	Context("When deleting a new AutoScalingRunnerSet", func() {
		It("It should cleanup all resources for a deleting AutoScalingRunnerSet before removing it", func() {
			// Wait till the listener is created
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, new(v1alpha1.AutoscalingListener))
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			// Delete the AutoScalingRunnerSet
			err := k8sClient.Delete(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to delete AutoScalingRunnerSet")

			// Check if the listener is deleted
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, new(v1alpha1.AutoscalingListener))
					if err != nil && errors.IsNotFound(err) {
						return nil
					}

					return fmt.Errorf("listener is not deleted")
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "Listener should be deleted")

			// Check if all the EphemeralRunnerSet is deleted
			Eventually(
				func() error {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					if err != nil {
						return err
					}

					if len(runnerSetList.Items) != 0 {
						return fmt.Errorf("EphemeralRunnerSet is not deleted, count=%v", len(runnerSetList.Items))
					}

					return nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "All EphemeralRunnerSet should be deleted")

			// Check if the AutoScalingRunnerSet is deleted
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, new(v1alpha1.AutoscalingRunnerSet))
					if err != nil && errors.IsNotFound(err) {
						return nil
					}

					return fmt.Errorf("AutoScalingRunnerSet is not deleted")
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "AutoScalingRunnerSet should be deleted")
		})
	})

	Context("When updating a new AutoScalingRunnerSet", func() {
		It("It should re-create EphemeralRunnerSet and Listener as needed when updating AutoScalingRunnerSet", func() {
			// Wait till the listener is created
			listener := new(v1alpha1.AutoscalingListener)
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
			err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(Equal(1), "There should be 1 EphemeralRunnerSet")
			runnerSet := runnerSetList.Items[0]

			// Update the AutoScalingRunnerSet.Spec.Template
			// This should trigger re-creation of EphemeralRunnerSet and Listener
			patched := autoscalingRunnerSet.DeepCopy()
			patched.Spec.Template.Spec.PriorityClassName = "test-priority-class"
			if patched.ObjectMeta.Annotations == nil {
				patched.ObjectMeta.Annotations = make(map[string]string)
			}
			patched.ObjectMeta.Annotations[annotationKeyValuesHash] = "test-hash"
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")
			autoscalingRunnerSet = patched.DeepCopy()

			// We should create a new EphemeralRunnerSet and delete the old one, eventually, we will have only one EphemeralRunnerSet
			Eventually(
				func() (string, error) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					if err != nil {
						return "", err
					}

					if len(runnerSetList.Items) != 1 {
						return "", fmt.Errorf("We should have only 1 EphemeralRunnerSet, but got %v", len(runnerSetList.Items))
					}

					return runnerSetList.Items[0].Annotations[annotationKeyRunnerSpecHash], nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(runnerSet.Annotations[annotationKeyRunnerSpecHash]), "New EphemeralRunnerSet should be created")

			// We should create a new listener
			Eventually(
				func() (string, error) {
					listener := new(v1alpha1.AutoscalingListener)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
					if err != nil {
						return "", err
					}

					return listener.Spec.EphemeralRunnerSetName, nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(runnerSet.Name), "New Listener should be created")

			// Only update the Spec for the AutoScalingListener
			// This should trigger re-creation of the Listener only
			runnerSetList = new(v1alpha1.EphemeralRunnerSetList)
			err = k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(Equal(1), "There should be 1 EphemeralRunnerSet")
			runnerSet = runnerSetList.Items[0]

			listener = new(v1alpha1.AutoscalingListener)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
			Expect(err).NotTo(HaveOccurred(), "failed to get Listener")

			patched = autoscalingRunnerSet.DeepCopy()
			min := 10
			patched.Spec.MinRunners = &min
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")

			// We should not re-create a new EphemeralRunnerSet
			Consistently(
				func() (string, error) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					if err != nil {
						return "", err
					}

					if len(runnerSetList.Items) != 1 {
						return "", fmt.Errorf("We should have only 1 EphemeralRunnerSet, but got %v", len(runnerSetList.Items))
					}

					return string(runnerSetList.Items[0].UID), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo(string(runnerSet.UID)), "New EphemeralRunnerSet should not be created")

			// We should only re-create a new listener
			Eventually(
				func() (string, error) {
					listener := new(v1alpha1.AutoscalingListener)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
					if err != nil {
						return "", err
					}

					return string(listener.UID), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(string(listener.UID)), "New Listener should be created")

			// Only update the values hash for the autoscaling runner set
			// This should trigger re-creation of the Listener only
			runnerSetList = new(v1alpha1.EphemeralRunnerSetList)
			err = k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(Equal(1), "There should be 1 EphemeralRunnerSet")
			runnerSet = runnerSetList.Items[0]

			listener = new(v1alpha1.AutoscalingListener)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
			Expect(err).NotTo(HaveOccurred(), "failed to get Listener")

			patched = autoscalingRunnerSet.DeepCopy()
			patched.ObjectMeta.Annotations[annotationKeyValuesHash] = "hash-changes"
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")

			// We should not re-create a new EphemeralRunnerSet
			Consistently(
				func() (string, error) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					if err != nil {
						return "", err
					}

					if len(runnerSetList.Items) != 1 {
						return "", fmt.Errorf("We should have only 1 EphemeralRunnerSet, but got %v", len(runnerSetList.Items))
					}

					return string(runnerSetList.Items[0].UID), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo(string(runnerSet.UID)), "New EphemeralRunnerSet should not be created")

			// We should only re-create a new listener
			Eventually(
				func() (string, error) {
					listener := new(v1alpha1.AutoscalingListener)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
					if err != nil {
						return "", err
					}

					return string(listener.UID), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(string(listener.UID)), "New Listener should be created")
		})

		It("It should update RunnerScaleSet's runner group on service when it changes", func() {
			updated := new(v1alpha1.AutoscalingRunnerSet)
			// Wait till the listener is created
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, new(v1alpha1.AutoscalingListener))
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			patched := autoscalingRunnerSet.DeepCopy()
			patched.Spec.RunnerGroup = "testgroup2"
			err := k8sClient.Patch(ctx, patched, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")

			// Check if AutoScalingRunnerSet has the new runner group in its annotation
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, updated)
					if err != nil {
						return "", err
					}

					if _, ok := updated.Annotations[AnnotationKeyGitHubRunnerGroupName]; !ok {
						return "", nil
					}

					return updated.Annotations[AnnotationKeyGitHubRunnerGroupName], nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval).Should(BeEquivalentTo("testgroup2"), "AutoScalingRunnerSet should have the new runner group in its annotation")

			// delete the annotation and it should be re-added
			patched = autoscalingRunnerSet.DeepCopy()
			delete(patched.Annotations, AnnotationKeyGitHubRunnerGroupName)
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")

			// Check if AutoScalingRunnerSet still has the runner group in its annotation
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, updated)
					if err != nil {
						return "", err
					}

					if _, ok := updated.Annotations[AnnotationKeyGitHubRunnerGroupName]; !ok {
						return "", nil
					}

					return updated.Annotations[AnnotationKeyGitHubRunnerGroupName], nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo("testgroup2"), "AutoScalingRunnerSet should have the runner group in its annotation")
		})
	})

	Context("When updating an AutoscalingRunnerSet with running or pending jobs", func() {
		It("It should wait for running and pending jobs to finish before applying the update. Update Strategy is set to eventual.", func() {
			// Switch update strategy to eventual (drain jobs )
			controller.UpdateStrategy = UpdateStrategyEventual
			// Wait till the listener is created
			listener := new(v1alpha1.AutoscalingListener)
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "Listener should be created")

			// Wait till the ephemeral runner set is created
			Eventually(
				func() (int, error) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					if err != nil {
						return 0, err
					}

					return len(runnerSetList.Items), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(1), "Only one EphemeralRunnerSet should be created")

			runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
			err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")

			// Emulate running and pending jobs
			runnerSet := runnerSetList.Items[0]
			activeRunnerSet := runnerSet.DeepCopy()
			activeRunnerSet.Status.CurrentReplicas = 6
			activeRunnerSet.Status.FailedEphemeralRunners = 1
			activeRunnerSet.Status.RunningEphemeralRunners = 2
			activeRunnerSet.Status.PendingEphemeralRunners = 3

			desiredStatus := v1alpha1.AutoscalingRunnerSetStatus{
				CurrentRunners:          activeRunnerSet.Status.CurrentReplicas,
				State:                   "",
				PendingEphemeralRunners: activeRunnerSet.Status.PendingEphemeralRunners,
				RunningEphemeralRunners: activeRunnerSet.Status.RunningEphemeralRunners,
				FailedEphemeralRunners:  activeRunnerSet.Status.FailedEphemeralRunners,
			}

			err = k8sClient.Status().Patch(ctx, activeRunnerSet, client.MergeFrom(&runnerSet))
			Expect(err).NotTo(HaveOccurred(), "Failed to patch runner set status")

			Eventually(
				func() (v1alpha1.AutoscalingRunnerSetStatus, error) {
					updated := new(v1alpha1.AutoscalingRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, updated)
					if err != nil {
						return v1alpha1.AutoscalingRunnerSetStatus{}, fmt.Errorf("failed to get AutoScalingRunnerSet: %w", err)
					}
					return updated.Status, nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(desiredStatus), "AutoScalingRunnerSet status should be updated")

			// Patch the AutoScalingRunnerSet image which should trigger
			// the recreation of the Listener and EphemeralRunnerSet
			patched := autoscalingRunnerSet.DeepCopy()
			if patched.ObjectMeta.Annotations == nil {
				patched.ObjectMeta.Annotations = make(map[string]string)
			}
			patched.ObjectMeta.Annotations[annotationKeyValuesHash] = "testgroup2"
			patched.Spec.Template.Spec = corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "runner",
						Image: "ghcr.io/actions/abcd:1.1.1",
					},
				},
			}
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")
			autoscalingRunnerSet = patched.DeepCopy()

			// The EphemeralRunnerSet should not be recreated
			Consistently(
				func() (string, error) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					Expect(err).NotTo(HaveOccurred(), "failed to fetch AutoScalingRunnerSet")
					return runnerSetList.Items[0].Name, nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Equal(activeRunnerSet.Name), "The EphemeralRunnerSet should not be recreated")

			// The listener should not be recreated
			Consistently(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoscalingRunnerSet), Namespace: autoscalingRunnerSet.Namespace}, listener)
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).ShouldNot(Succeed(), "Listener should not be recreated")
		})
	})

	It("Should update Status on EphemeralRunnerSet status Update", func() {
		ars := new(v1alpha1.AutoscalingRunnerSet)
		Eventually(
			func() (bool, error) {
				err := k8sClient.Get(
					ctx,
					client.ObjectKey{
						Name:      autoscalingRunnerSet.Name,
						Namespace: autoscalingRunnerSet.Namespace,
					},
					ars,
				)
				if err != nil {
					return false, err
				}
				return true, nil
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "AutoscalingRunnerSet should be created")

		runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
		Eventually(func() (int, error) {
			err := k8sClient.List(ctx, runnerSetList, client.InNamespace(ars.Namespace))
			if err != nil {
				return 0, err
			}
			return len(runnerSetList.Items), nil
		},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeEquivalentTo(1), "Failed to fetch runner set list")

		runnerSet := runnerSetList.Items[0]
		statusUpdate := runnerSet.DeepCopy()
		statusUpdate.Status.CurrentReplicas = 6
		statusUpdate.Status.FailedEphemeralRunners = 1
		statusUpdate.Status.RunningEphemeralRunners = 2
		statusUpdate.Status.PendingEphemeralRunners = 3

		desiredStatus := v1alpha1.AutoscalingRunnerSetStatus{
			CurrentRunners:          statusUpdate.Status.CurrentReplicas,
			State:                   "",
			PendingEphemeralRunners: statusUpdate.Status.PendingEphemeralRunners,
			RunningEphemeralRunners: statusUpdate.Status.RunningEphemeralRunners,
			FailedEphemeralRunners:  statusUpdate.Status.FailedEphemeralRunners,
		}

		err := k8sClient.Status().Patch(ctx, statusUpdate, client.MergeFrom(&runnerSet))
		Expect(err).NotTo(HaveOccurred(), "Failed to patch runner set status")

		Eventually(
			func() (v1alpha1.AutoscalingRunnerSetStatus, error) {
				updated := new(v1alpha1.AutoscalingRunnerSet)
				err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, updated)
				if err != nil {
					return v1alpha1.AutoscalingRunnerSetStatus{}, fmt.Errorf("failed to get AutoScalingRunnerSet: %w", err)
				}
				return updated.Status, nil
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeEquivalentTo(desiredStatus), "AutoScalingRunnerSet status should be updated")
	})
})

var _ = Describe("Test AutoScalingController updates", Ordered, func() {
	var originalBuildVersion string
	buildVersion := "0.1.0"

	BeforeAll(func() {
		originalBuildVersion = build.Version
		build.Version = buildVersion
	})

	AfterAll(func() {
		build.Version = originalBuildVersion
	})

	Context("Creating autoscaling runner set with RunnerScaleSetName set", func() {
		var ctx context.Context
		var mgr ctrl.Manager
		var autoscalingNS *corev1.Namespace
		var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
		var configSecret *corev1.Secret

		BeforeEach(func() {
			originalBuildVersion = build.Version
			ctx = context.Background()
			autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
			configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

			controller := &AutoscalingRunnerSetReconciler{
				Client:                             mgr.GetClient(),
				Scheme:                             mgr.GetScheme(),
				Log:                                logf.Log,
				ControllerNamespace:                autoscalingNS.Name,
				DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
				ActionsClient: fake.NewMultiClient(
					fake.WithDefaultClient(
						fake.NewFakeClient(
							fake.WithUpdateRunnerScaleSet(
								&actions.RunnerScaleSet{
									Id:                 1,
									Name:               "testset_update",
									RunnerGroupId:      1,
									RunnerGroupName:    "testgroup",
									Labels:             []actions.Label{{Type: "test", Name: "test"}},
									RunnerSetting:      actions.RunnerSetting{},
									CreatedOn:          time.Now(),
									RunnerJitConfigUrl: "test.test.test",
									Statistics:         nil,
								},
								nil,
							),
						),
						nil,
					),
				),
			}
			err := controller.SetupWithManager(mgr)
			Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

			startManagers(GinkgoT(), mgr)
		})

		It("It should be create AutoScalingRunnerSet and has annotation for the RunnerScaleSetName", func() {
			min := 1
			max := 10
			autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					MaxRunners:         &max,
					MinRunners:         &min,
					RunnerScaleSetName: "testset",
					RunnerGroup:        "testgroup",
					Template: corev1.PodTemplateSpec{
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
			}

			err := k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			// Wait for the AutoScalingRunnerSet to be created with right annotation
			ars := new(v1alpha1.AutoscalingRunnerSet)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, ars)
					if err != nil {
						return "", err
					}

					if val, ok := ars.Annotations[AnnotationKeyGitHubRunnerScaleSetName]; ok {
						return val, nil
					}

					return "", nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(autoscalingRunnerSet.Spec.RunnerScaleSetName), "AutoScalingRunnerSet should have annotation for the RunnerScaleSetName")

			update := autoscalingRunnerSet.DeepCopy()
			update.Spec.RunnerScaleSetName = "testset_update"

			err = k8sClient.Patch(ctx, update, client.MergeFrom(autoscalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to update AutoScalingRunnerSet")

			// Wait for the AutoScalingRunnerSet to be updated with right annotation
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, ars)
					if err != nil {
						return "", err
					}

					if val, ok := ars.Annotations[AnnotationKeyGitHubRunnerScaleSetName]; ok {
						return val, nil
					}

					return "", nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(update.Spec.RunnerScaleSetName), "AutoScalingRunnerSet should have a updated annotation for the RunnerScaleSetName")
		})
	})
})

var _ = Describe("Test AutoscalingController creation failures", Ordered, func() {
	var originalBuildVersion string
	buildVersion := "0.1.0"

	BeforeAll(func() {
		originalBuildVersion = build.Version
		build.Version = buildVersion
	})

	AfterAll(func() {
		build.Version = originalBuildVersion
	})
	Context("When autoscaling runner set creation fails on the client", func() {
		var ctx context.Context
		var mgr ctrl.Manager
		var autoscalingNS *corev1.Namespace

		BeforeEach(func() {
			ctx = context.Background()
			autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)

			controller := &AutoscalingRunnerSetReconciler{
				Client:                             mgr.GetClient(),
				Scheme:                             mgr.GetScheme(),
				Log:                                logf.Log,
				ControllerNamespace:                autoscalingNS.Name,
				DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
				ActionsClient:                      fake.NewMultiClient(),
			}
			err := controller.SetupWithManager(mgr)
			Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

			startManagers(GinkgoT(), mgr)
		})

		It("It should be able to clean up if annotation related to scale set id does not exist", func() {
			min := 1
			max := 10
			autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl: "https://github.com/owner/repo",
					MaxRunners:      &max,
					MinRunners:      &min,
					RunnerGroup:     "testgroup",
					Template: corev1.PodTemplateSpec{
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
			}

			err := k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			// wait for the finalizer to be added
			ars := new(v1alpha1.AutoscalingRunnerSet)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, ars)
					if err != nil {
						return "", err
					}
					if len(ars.Finalizers) == 0 {
						return "", nil
					}
					return ars.Finalizers[0], nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(autoscalingRunnerSetFinalizerName), "AutoScalingRunnerSet should have a finalizer")

			ars.ObjectMeta.Annotations = make(map[string]string)
			err = k8sClient.Update(ctx, ars)
			Expect(err).NotTo(HaveOccurred(), "Update autoscaling runner set without annotation should be successful")

			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, ars)
					if err != nil {
						return false, err
					}
					return len(ars.ObjectMeta.Annotations) == 0, nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(true), "Autoscaling runner set should be updated with empty annotations")

			err = k8sClient.Delete(ctx, ars)
			Expect(err).NotTo(HaveOccurred(), "Delete autoscaling runner set should be successful")

			Eventually(
				func() (bool, error) {
					updated := new(v1alpha1.AutoscalingRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, updated)
					if err == nil {
						return false, nil
					}
					if !errors.IsNotFound(err) {
						return false, err
					}

					return !controllerutil.ContainsFinalizer(updated, autoscalingRunnerSetFinalizerName), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(BeEquivalentTo(true), "Finalizer and resource should eventually be deleted")
		})
	})
})

var _ = Describe("Test client optional configuration", Ordered, func() {
	var originalBuildVersion string
	buildVersion := "0.1.0"

	BeforeAll(func() {
		originalBuildVersion = build.Version
		build.Version = buildVersion
	})

	AfterAll(func() {
		build.Version = originalBuildVersion
	})
	Context("When specifying a proxy", func() {
		var ctx context.Context
		var mgr ctrl.Manager
		var autoscalingNS *corev1.Namespace
		var configSecret *corev1.Secret
		var controller *AutoscalingRunnerSetReconciler

		BeforeEach(func() {
			ctx = context.Background()
			autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
			configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

			controller = &AutoscalingRunnerSetReconciler{
				Client:                             mgr.GetClient(),
				Scheme:                             mgr.GetScheme(),
				Log:                                logf.Log,
				ControllerNamespace:                autoscalingNS.Name,
				DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
				ActionsClient:                      actions.NewMultiClient(logr.Discard()),
			}

			err := controller.SetupWithManager(mgr)
			Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

			startManagers(GinkgoT(), mgr)
		})

		It("should be able to make requests to a server using a proxy", func() {
			serverSuccessfullyCalled := false
			proxy := testserver.New(GinkgoT(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serverSuccessfullyCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			min := 1
			max := 10
			autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:    "http://example.com/org/repo",
					GitHubConfigSecret: configSecret.Name,
					MaxRunners:         &max,
					MinRunners:         &min,
					RunnerGroup:        "testgroup",
					Proxy: &v1alpha1.ProxyConfig{
						HTTP: &v1alpha1.ProxyServerConfig{
							Url: proxy.URL,
						},
					},
					Template: corev1.PodTemplateSpec{
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
			}

			err := k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			// wait for server to be called
			Eventually(
				func() (bool, error) {
					return serverSuccessfullyCalled, nil
				},
				autoscalingRunnerSetTestTimeout,
				1*time.Nanosecond,
			).Should(BeTrue(), "server was not called")
		})

		It("should be able to make requests to a server using a proxy with user info", func() {
			serverSuccessfullyCalled := false
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header := r.Header.Get("Proxy-Authorization")
				Expect(header).NotTo(BeEmpty())

				header = strings.TrimPrefix(header, "Basic ")
				decoded, err := base64.StdEncoding.DecodeString(header)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(decoded)).To(Equal("test:password"))

				serverSuccessfullyCalled = true
				w.WriteHeader(http.StatusOK)
			}))
			GinkgoT().Cleanup(func() {
				proxy.Close()
			})

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

			min := 1
			max := 10
			autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:    "http://example.com/org/repo",
					GitHubConfigSecret: configSecret.Name,
					MaxRunners:         &max,
					MinRunners:         &min,
					RunnerGroup:        "testgroup",
					Proxy: &v1alpha1.ProxyConfig{
						HTTP: &v1alpha1.ProxyServerConfig{
							Url:                 proxy.URL,
							CredentialSecretRef: "proxy-credentials",
						},
					},
					Template: corev1.PodTemplateSpec{
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
			}

			err = k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			// wait for server to be called
			Eventually(
				func() (bool, error) {
					return serverSuccessfullyCalled, nil
				},
				autoscalingRunnerSetTestTimeout,
				1*time.Nanosecond,
			).Should(BeTrue(), "server was not called")
		})
	})

	Context("When specifying a configmap for root CAs", func() {
		var ctx context.Context
		var mgr ctrl.Manager
		var autoscalingNS *corev1.Namespace
		var configSecret *corev1.Secret
		var rootCAConfigMap *corev1.ConfigMap
		var controller *AutoscalingRunnerSetReconciler

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

			controller = &AutoscalingRunnerSetReconciler{
				Client:                             mgr.GetClient(),
				Scheme:                             mgr.GetScheme(),
				Log:                                logf.Log,
				ControllerNamespace:                autoscalingNS.Name,
				DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
				ActionsClient:                      fake.NewMultiClient(),
			}
			err = controller.SetupWithManager(mgr)
			Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

			startManagers(GinkgoT(), mgr)
		})

		It("should be able to make requests to a server using root CAs", func() {
			controller.ActionsClient = actions.NewMultiClient(logr.Discard())

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

			min := 1
			max := 10
			autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:    server.ConfigURLForOrg("my-org"),
					GitHubConfigSecret: configSecret.Name,
					GitHubServerTLS: &v1alpha1.GitHubServerTLSConfig{
						CertificateFrom: &v1alpha1.TLSCertificateSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: rootCAConfigMap.Name,
								},
								Key: "rootCA.crt",
							},
						},
					},
					MaxRunners:  &max,
					MinRunners:  &min,
					RunnerGroup: "testgroup",
					Template: corev1.PodTemplateSpec{
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
			}

			err = k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			// wait for server to be called
			Eventually(
				func() (bool, error) {
					return serverSuccessfullyCalled, nil
				},
				autoscalingRunnerSetTestTimeout,
				1*time.Nanosecond,
			).Should(BeTrue(), "server was not called")
		})

		It("it creates a listener referencing the right configmap for TLS", func() {
			min := 1
			max := 10
			autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					GitHubServerTLS: &v1alpha1.GitHubServerTLSConfig{
						CertificateFrom: &v1alpha1.TLSCertificateSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: rootCAConfigMap.Name,
								},
								Key: "rootCA.crt",
							},
						},
					},
					MaxRunners:  &max,
					MinRunners:  &min,
					RunnerGroup: "testgroup",
					Template: corev1.PodTemplateSpec{
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
			}

			err := k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			Eventually(
				func(g Gomega) {
					listener := new(v1alpha1.AutoscalingListener)
					err := k8sClient.Get(
						ctx,
						client.ObjectKey{
							Name:      scaleSetListenerName(autoscalingRunnerSet),
							Namespace: autoscalingRunnerSet.Namespace,
						},
						listener,
					)
					g.Expect(err).NotTo(HaveOccurred(), "failed to get listener")

					g.Expect(listener.Spec.GitHubServerTLS).NotTo(BeNil(), "listener does not have TLS config")
					g.Expect(listener.Spec.GitHubServerTLS).To(BeEquivalentTo(autoscalingRunnerSet.Spec.GitHubServerTLS), "listener does not have TLS config")
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "tls config is incorrect")
		})

		It("it creates an ephemeral runner set referencing the right configmap for TLS", func() {
			min := 1
			max := 10
			autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-asrs",
					Namespace: autoscalingNS.Name,
					Labels: map[string]string{
						LabelKeyKubernetesVersion: buildVersion,
					},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					GitHubConfigUrl:    "https://github.com/owner/repo",
					GitHubConfigSecret: configSecret.Name,
					GitHubServerTLS: &v1alpha1.GitHubServerTLSConfig{
						CertificateFrom: &v1alpha1.TLSCertificateSource{
							ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: rootCAConfigMap.Name,
								},
								Key: "rootCA.crt",
							},
						},
					},
					MaxRunners:  &max,
					MinRunners:  &min,
					RunnerGroup: "testgroup",
					Template: corev1.PodTemplateSpec{
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
			}

			err := k8sClient.Create(ctx, autoscalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

			Eventually(
				func(g Gomega) {
					runnerSetList := new(v1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoscalingRunnerSet.Namespace))
					g.Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
					g.Expect(runnerSetList.Items).To(HaveLen(1), "expected 1 EphemeralRunnerSet to be created")

					runnerSet := &runnerSetList.Items[0]

					g.Expect(runnerSet.Spec.EphemeralRunnerSpec.GitHubServerTLS).NotTo(BeNil(), "expected EphemeralRunnerSpec.GitHubServerTLS to be set")
					g.Expect(runnerSet.Spec.EphemeralRunnerSpec.GitHubServerTLS).To(BeEquivalentTo(autoscalingRunnerSet.Spec.GitHubServerTLS), "EphemeralRunnerSpec does not have TLS config")
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed())
		})
	})
})

var _ = Describe("Test external permissions cleanup", Ordered, func() {
	var originalBuildVersion string
	buildVersion := "0.1.0"

	BeforeAll(func() {
		originalBuildVersion = build.Version
		build.Version = buildVersion
	})

	AfterAll(func() {
		build.Version = originalBuildVersion
	})

	It("Should clean up kubernetes mode permissions", func() {
		ctx := context.Background()
		autoscalingNS, mgr := createNamespace(GinkgoT(), k8sClient)

		configSecret := createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoscalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ActionsClient:                      fake.NewMultiClient(),
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)

		min := 1
		max := 10
		autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
				Labels: map[string]string{
					"app.kubernetes.io/name":  "gha-runner-scale-set",
					LabelKeyKubernetesVersion: buildVersion,
				},
				Annotations: map[string]string{
					AnnotationKeyKubernetesModeRoleBindingName:    "kube-mode-role-binding",
					AnnotationKeyKubernetesModeRoleName:           "kube-mode-role",
					AnnotationKeyKubernetesModeServiceAccountName: "kube-mode-service-account",
				},
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &max,
				MinRunners:         &min,
				RunnerGroup:        "testgroup",
				Template: corev1.PodTemplateSpec{
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
		}

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyKubernetesModeRoleName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
		}

		err = k8sClient.Create(ctx, role)
		Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes mode role")

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyKubernetesModeServiceAccountName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
		}

		err = k8sClient.Create(ctx, serviceAccount)
		Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes mode service account")

		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyKubernetesModeRoleBindingName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      serviceAccount.Name,
					Namespace: serviceAccount.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				// Kind is the type of resource being referenced
				Kind: "Role",
				Name: role.Name,
			},
		}

		err = k8sClient.Create(ctx, roleBinding)
		Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes mode role binding")

		err = k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		Eventually(
			func() (string, error) {
				created := new(v1alpha1.AutoscalingRunnerSet)
				err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, created)
				if err != nil {
					return "", err
				}
				if len(created.Finalizers) == 0 {
					return "", nil
				}
				return created.Finalizers[0], nil
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeEquivalentTo(autoscalingRunnerSetFinalizerName), "AutoScalingRunnerSet should have a finalizer")

		err = k8sClient.Delete(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to delete autoscaling runner set")

		err = k8sClient.Delete(ctx, roleBinding)
		Expect(err).NotTo(HaveOccurred(), "failed to delete kubernetes mode role binding")

		err = k8sClient.Delete(ctx, role)
		Expect(err).NotTo(HaveOccurred(), "failed to delete kubernetes mode role")

		err = k8sClient.Delete(ctx, serviceAccount)
		Expect(err).NotTo(HaveOccurred(), "failed to delete kubernetes mode service account")

		Eventually(
			func() bool {
				r := new(rbacv1.RoleBinding)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      roleBinding.Name,
					Namespace: roleBinding.Namespace,
				}, r)

				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "Expected role binding to be cleaned up")

		Eventually(
			func() bool {
				r := new(rbacv1.Role)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      role.Name,
					Namespace: role.Namespace,
				}, r)

				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "Expected role to be cleaned up")
	})

	It("Should clean up manager permissions and no-permission service account", func() {
		ctx := context.Background()
		autoscalingNS, mgr := createNamespace(GinkgoT(), k8sClient)

		controller := &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoscalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ActionsClient:                      fake.NewMultiClient(),
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)

		min := 1
		max := 10
		autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
				Labels: map[string]string{
					"app.kubernetes.io/name":  "gha-runner-scale-set",
					LabelKeyKubernetesVersion: buildVersion,
				},
				Annotations: map[string]string{
					AnnotationKeyManagerRoleName:                "manager-role",
					AnnotationKeyManagerRoleBindingName:         "manager-role-binding",
					AnnotationKeyGitHubSecretName:               "gh-secret-name",
					AnnotationKeyNoPermissionServiceAccountName: "no-permission-sa",
				},
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl: "https://github.com/owner/repo",
				MaxRunners:      &max,
				MinRunners:      &min,
				RunnerGroup:     "testgroup",
				Template: corev1.PodTemplateSpec{
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
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyGitHubSecretName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
			Data: map[string][]byte{
				"github_token": []byte(defaultGitHubToken),
			},
		}

		err = k8sClient.Create(context.Background(), secret)
		Expect(err).NotTo(HaveOccurred(), "failed to create github secret")

		autoscalingRunnerSet.Spec.GitHubConfigSecret = secret.Name

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyManagerRoleName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
		}

		err = k8sClient.Create(ctx, role)
		Expect(err).NotTo(HaveOccurred(), "failed to create manager role")

		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyManagerRoleBindingName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     role.Name,
			},
		}

		err = k8sClient.Create(ctx, roleBinding)
		Expect(err).NotTo(HaveOccurred(), "failed to create manager role binding")

		noPermissionServiceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:       autoscalingRunnerSet.Annotations[AnnotationKeyNoPermissionServiceAccountName],
				Namespace:  autoscalingRunnerSet.Namespace,
				Finalizers: []string{AutoscalingRunnerSetCleanupFinalizerName},
			},
		}

		err = k8sClient.Create(ctx, noPermissionServiceAccount)
		Expect(err).NotTo(HaveOccurred(), "failed to create no permission service account")

		err = k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		Eventually(
			func() (string, error) {
				created := new(v1alpha1.AutoscalingRunnerSet)
				err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingRunnerSet.Name, Namespace: autoscalingRunnerSet.Namespace}, created)
				if err != nil {
					return "", err
				}
				if len(created.Finalizers) == 0 {
					return "", nil
				}
				return created.Finalizers[0], nil
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeEquivalentTo(autoscalingRunnerSetFinalizerName), "AutoScalingRunnerSet should have a finalizer")

		err = k8sClient.Delete(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to delete autoscaling runner set")

		err = k8sClient.Delete(ctx, noPermissionServiceAccount)
		Expect(err).NotTo(HaveOccurred(), "failed to delete no permission service account")

		err = k8sClient.Delete(ctx, secret)
		Expect(err).NotTo(HaveOccurred(), "failed to delete GitHub secret")

		err = k8sClient.Delete(ctx, roleBinding)
		Expect(err).NotTo(HaveOccurred(), "failed to delete manager role binding")

		err = k8sClient.Delete(ctx, role)
		Expect(err).NotTo(HaveOccurred(), "failed to delete manager role")

		Eventually(
			func() bool {
				r := new(corev1.ServiceAccount)
				err := k8sClient.Get(
					ctx,
					types.NamespacedName{
						Name:      noPermissionServiceAccount.Name,
						Namespace: noPermissionServiceAccount.Namespace,
					},
					r,
				)
				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "Expected no permission service account to be cleaned up")

		Eventually(
			func() bool {
				r := new(corev1.Secret)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secret.Name,
					Namespace: secret.Namespace,
				}, r)

				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "Expected role binding to be cleaned up")

		Eventually(
			func() bool {
				r := new(rbacv1.RoleBinding)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      roleBinding.Name,
					Namespace: roleBinding.Namespace,
				}, r)

				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "Expected role binding to be cleaned up")

		Eventually(
			func() bool {
				r := new(rbacv1.Role)
				err := k8sClient.Get(
					ctx,
					types.NamespacedName{
						Name:      role.Name,
						Namespace: role.Namespace,
					},
					r,
				)

				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue(), "Expected role to be cleaned up")
	})
})

var _ = Describe("Test resource version and build version mismatch", func() {
	It("Should delete and recreate the autoscaling runner set to match the build version", func() {
		ctx := context.Background()
		autoscalingNS, mgr := createNamespace(GinkgoT(), k8sClient)

		configSecret := createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoscalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ActionsClient:                      fake.NewMultiClient(),
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		originalVersion := build.Version
		defer func() {
			build.Version = originalVersion
		}()
		build.Version = "0.2.0"

		min := 1
		max := 10
		autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
				Labels: map[string]string{
					"app.kubernetes.io/name":    "gha-runner-scale-set",
					"app.kubernetes.io/version": "0.1.0",
				},
				Annotations: map[string]string{
					AnnotationKeyKubernetesModeRoleBindingName:    "kube-mode-role-binding",
					AnnotationKeyKubernetesModeRoleName:           "kube-mode-role",
					AnnotationKeyKubernetesModeServiceAccountName: "kube-mode-service-account",
				},
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &max,
				MinRunners:         &min,
				RunnerGroup:        "testgroup",
				Template: corev1.PodTemplateSpec{
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
		}

		// create autoscaling runner set before starting a manager
		err = k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred())

		startManagers(GinkgoT(), mgr)

		Eventually(
			func() bool {
				ars := new(v1alpha1.AutoscalingRunnerSet)
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: autoscalingRunnerSet.Name}, ars)
				return errors.IsNotFound(err)
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(BeTrue())
	})
})
