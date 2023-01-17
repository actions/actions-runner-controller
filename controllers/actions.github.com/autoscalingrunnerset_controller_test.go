package actionsgithubcom

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions/fake"
)

const (
	autoScalingRunnerSetTestTimeout     = time.Second * 5
	autoScalingRunnerSetTestInterval    = time.Millisecond * 250
	autoScalingRunnerSetTestGitHubToken = "gh_token"
)

var _ = Describe("Test AutoScalingRunnerSet controller", func() {
	var ctx context.Context
	var cancel context.CancelFunc
	autoScalingNS := new(corev1.Namespace)
	autoScalingRunnerSet := new(actionsv1alpha1.AutoscalingRunnerSet)
	configSecret := new(corev1.Secret)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.TODO())
		autoScalingNS = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling" + RandStringRunes(5)},
		}

		err := k8sClient.Create(ctx, autoScalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace for AutoScalingRunnerSet")

		configSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "github-config-secret",
				Namespace: autoScalingNS.Name,
			},
			Data: map[string][]byte{
				"github_token": []byte(autoScalingRunnerSetTestGitHubToken),
			},
		}

		err = k8sClient.Create(ctx, configSecret)
		Expect(err).NotTo(HaveOccurred(), "failed to create config secret")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Namespace:          autoScalingNS.Name,
			MetricsBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		controller := &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoScalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ActionsClient:                      fake.NewMultiClient(),
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		min := 1
		max := 10
		autoScalingRunnerSet = &actionsv1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoScalingNS.Name,
			},
			Spec: actionsv1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &max,
				MinRunners:         &min,
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

		err = k8sClient.Create(ctx, autoScalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		defer cancel()

		err := k8sClient.Delete(ctx, autoScalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace for AutoScalingRunnerSet")
	})

	Context("When creating a new AutoScalingRunnerSet", func() {
		It("It should create/add all required resources for a new AutoScalingRunnerSet (finalizer, runnerscaleset, ephemeralrunnerset, listener)", func() {
			// Check if finalizer is added
			created := new(actionsv1alpha1.AutoscalingRunnerSet)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingRunnerSet.Name, Namespace: autoScalingRunnerSet.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(BeEquivalentTo(autoscalingRunnerSetFinalizerName), "AutoScalingRunnerSet should have a finalizer")

			// Check if runner scale set is created on service
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingRunnerSet.Name, Namespace: autoScalingRunnerSet.Namespace}, created)
					if err != nil {
						return "", err
					}

					if _, ok := created.Annotations[runnerScaleSetIdKey]; !ok {
						return "", nil
					}

					return created.Annotations[runnerScaleSetIdKey], nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(BeEquivalentTo("1"), "RunnerScaleSet should be created/fetched and update the AutoScalingRunnerSet's annotation")

			// Check if ephemeral runner set is created
			Eventually(
				func() (int, error) {
					runnerSetList := new(actionsv1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
					if err != nil {
						return 0, err
					}

					return len(runnerSetList.Items), nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(BeEquivalentTo(1), "Only one EphemeralRunnerSet should be created")

			// Check if listener is created
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, new(actionsv1alpha1.AutoscalingListener))
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			// Check if status is updated
			runnerSetList := new(actionsv1alpha1.EphemeralRunnerSetList)
			err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(BeEquivalentTo(1), "Only one EphemeralRunnerSet should be created")
			runnerSet := runnerSetList.Items[0]
			statusUpdate := runnerSet.DeepCopy()
			statusUpdate.Status.CurrentReplicas = 100
			err = k8sClient.Status().Patch(ctx, statusUpdate, client.MergeFrom(&runnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch EphemeralRunnerSet status")

			Eventually(
				func() (int, error) {
					updated := new(actionsv1alpha1.AutoscalingRunnerSet)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingRunnerSet.Name, Namespace: autoScalingRunnerSet.Namespace}, updated)
					if err != nil {
						return 0, fmt.Errorf("failed to get AutoScalingRunnerSet: %w", err)
					}
					return updated.Status.CurrentRunners, nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(BeEquivalentTo(100), "AutoScalingRunnerSet status should be updated")
		})
	})

	Context("When deleting a new AutoScalingRunnerSet", func() {
		It("It should cleanup all resources for a deleting AutoScalingRunnerSet before removing it", func() {
			// Wait till the listener is created
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, new(actionsv1alpha1.AutoscalingListener))
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			// Delete the AutoScalingRunnerSet
			err := k8sClient.Delete(ctx, autoScalingRunnerSet)
			Expect(err).NotTo(HaveOccurred(), "failed to delete AutoScalingRunnerSet")

			// Check if the listener is deleted
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, new(actionsv1alpha1.AutoscalingListener))
					if err != nil && errors.IsNotFound(err) {
						return nil
					}

					return fmt.Errorf("listener is not deleted")
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(Succeed(), "Listener should be deleted")

			// Check if all the EphemeralRunnerSet is deleted
			Eventually(
				func() error {
					runnerSetList := new(actionsv1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
					if err != nil {
						return err
					}

					if len(runnerSetList.Items) != 0 {
						return fmt.Errorf("EphemeralRunnerSet is not deleted, count=%v", len(runnerSetList.Items))
					}

					return nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(Succeed(), "All EphemeralRunnerSet should be deleted")

			// Check if the AutoScalingRunnerSet is deleted
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingRunnerSet.Name, Namespace: autoScalingRunnerSet.Namespace}, new(actionsv1alpha1.AutoscalingRunnerSet))
					if err != nil && errors.IsNotFound(err) {
						return nil
					}

					return fmt.Errorf("AutoScalingRunnerSet is not deleted")
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(Succeed(), "AutoScalingRunnerSet should be deleted")
		})
	})

	Context("When updating a new AutoScalingRunnerSet", func() {
		It("It should re-create EphemeralRunnerSet and Listener as needed when updating AutoScalingRunnerSet", func() {
			// Wait till the listener is created
			listener := new(actionsv1alpha1.AutoscalingListener)
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, listener)
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(Succeed(), "Listener should be created")

			runnerSetList := new(actionsv1alpha1.EphemeralRunnerSetList)
			err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(Equal(1), "There should be 1 EphemeralRunnerSet")
			runnerSet := runnerSetList.Items[0]

			// Update the AutoScalingRunnerSet.Spec.Template
			// This should trigger re-creation of EphemeralRunnerSet and Listener
			patched := autoScalingRunnerSet.DeepCopy()
			patched.Spec.Template.Spec.PriorityClassName = "test-priority-class"
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoScalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")
			autoScalingRunnerSet = patched.DeepCopy()

			// We should create a new EphemeralRunnerSet and delete the old one, eventually, we will have only one EphemeralRunnerSet
			Eventually(
				func() (string, error) {
					runnerSetList := new(actionsv1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
					if err != nil {
						return "", err
					}

					if len(runnerSetList.Items) != 1 {
						return "", fmt.Errorf("We should have only 1 EphemeralRunnerSet, but got %v", len(runnerSetList.Items))
					}

					return runnerSetList.Items[0].Labels[LabelKeyRunnerSpecHash], nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(runnerSet.Labels[LabelKeyRunnerSpecHash]), "New EphemeralRunnerSet should be created")

			// We should create a new listener
			Eventually(
				func() (string, error) {
					listener := new(actionsv1alpha1.AutoscalingListener)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, listener)
					if err != nil {
						return "", err
					}

					return listener.Spec.EphemeralRunnerSetName, nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(runnerSet.Name), "New Listener should be created")

			// Only update the Spec for the AutoScalingListener
			// This should trigger re-creation of the Listener only
			runnerSetList = new(actionsv1alpha1.EphemeralRunnerSetList)
			err = k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
			Expect(err).NotTo(HaveOccurred(), "failed to list EphemeralRunnerSet")
			Expect(len(runnerSetList.Items)).To(Equal(1), "There should be 1 EphemeralRunnerSet")
			runnerSet = runnerSetList.Items[0]

			listener = new(actionsv1alpha1.AutoscalingListener)
			err = k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, listener)
			Expect(err).NotTo(HaveOccurred(), "failed to get Listener")

			patched = autoScalingRunnerSet.DeepCopy()
			min := 10
			patched.Spec.MinRunners = &min
			err = k8sClient.Patch(ctx, patched, client.MergeFrom(autoScalingRunnerSet))
			Expect(err).NotTo(HaveOccurred(), "failed to patch AutoScalingRunnerSet")

			// We should not re-create a new EphemeralRunnerSet
			Consistently(
				func() (string, error) {
					runnerSetList := new(actionsv1alpha1.EphemeralRunnerSetList)
					err := k8sClient.List(ctx, runnerSetList, client.InNamespace(autoScalingRunnerSet.Namespace))
					if err != nil {
						return "", err
					}

					if len(runnerSetList.Items) != 1 {
						return "", fmt.Errorf("We should have only 1 EphemeralRunnerSet, but got %v", len(runnerSetList.Items))
					}

					return string(runnerSetList.Items[0].UID), nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).Should(BeEquivalentTo(string(runnerSet.UID)), "New EphemeralRunnerSet should not be created")

			// We should only re-create a new listener
			Eventually(
				func() (string, error) {
					listener := new(actionsv1alpha1.AutoscalingListener)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(autoScalingRunnerSet), Namespace: autoScalingRunnerSet.Namespace}, listener)
					if err != nil {
						return "", err
					}

					return string(listener.UID), nil
				},
				autoScalingRunnerSetTestTimeout,
				autoScalingRunnerSetTestInterval).ShouldNot(BeEquivalentTo(string(listener.UID)), "New Listener should be created")
		})
	})
})
