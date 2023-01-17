package actionsgithubcom

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

const (
	autoScalingListenerTestTimeout     = time.Second * 5
	autoScalingListenerTestInterval    = time.Millisecond * 250
	autoScalingListenerTestGitHubToken = "gh_token"
)

var _ = Describe("Test AutoScalingListener controller", func() {
	var ctx context.Context
	var cancel context.CancelFunc
	autoScalingNS := new(corev1.Namespace)
	autoScalingRunnerSet := new(actionsv1alpha1.AutoscalingRunnerSet)
	configSecret := new(corev1.Secret)
	autoScalingListener := new(actionsv1alpha1.AutoscalingListener)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.TODO())
		autoScalingNS = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling-listener" + RandStringRunes(5)},
		}

		err := k8sClient.Create(ctx, autoScalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace for AutoScalingRunnerSet")

		configSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "github-config-secret",
				Namespace: autoScalingNS.Name,
			},
			Data: map[string][]byte{
				"github_token": []byte(autoScalingListenerTestGitHubToken),
			},
		}

		err = k8sClient.Create(ctx, configSecret)
		Expect(err).NotTo(HaveOccurred(), "failed to create config secret")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Namespace:          autoScalingNS.Name,
			MetricsBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
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

		autoScalingListener = &actionsv1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asl",
				Namespace: autoScalingNS.Name,
			},
			Spec: actionsv1alpha1.AutoscalingListenerSpec{
				GitHubConfigUrl:               "https://github.com/owner/repo",
				GitHubConfigSecret:            configSecret.Name,
				RunnerScaleSetId:              1,
				AutoscalingRunnerSetNamespace: autoScalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoScalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
			},
		}

		err = k8sClient.Create(ctx, autoScalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingListener")

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

	Context("When creating a new AutoScalingListener", func() {
		It("It should create/add all required resources for a new AutoScalingListener (finalizer, secret, service account, role, rolebinding, pod)", func() {
			// Check if finalizer is added
			created := new(actionsv1alpha1.AutoscalingListener)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingListener.Name, Namespace: autoScalingListener.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(autoscalingListenerFinalizerName), "AutoScalingListener should have a finalizer")

			// Check if secret is created
			mirrorSecret := new(corev1.Secret)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerSecretMirrorName(autoScalingListener), Namespace: autoScalingListener.Namespace}, mirrorSecret)
					if err != nil {
						return "", err
					}
					return string(mirrorSecret.Data["github_token"]), nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(autoScalingListenerTestGitHubToken), "Mirror secret should be created")

			// Check if service account is created
			serviceAccount := new(corev1.ServiceAccount)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerServiceAccountName(autoScalingListener), Namespace: autoScalingListener.Namespace}, serviceAccount)
					if err != nil {
						return "", err
					}
					return serviceAccount.Name, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(scaleSetListenerServiceAccountName(autoScalingListener)), "Service account should be created")

			// Check if role is created
			role := new(rbacv1.Role)
			Eventually(
				func() ([]rbacv1.PolicyRule, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoScalingListener), Namespace: autoScalingListener.Spec.AutoscalingRunnerSetNamespace}, role)
					if err != nil {
						return nil, err
					}

					return role.Rules, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(rulesForListenerRole([]string{autoScalingListener.Spec.EphemeralRunnerSetName})), "Role should be created")

			// Check if rolebinding is created
			roleBinding := new(rbacv1.RoleBinding)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoScalingListener), Namespace: autoScalingListener.Spec.AutoscalingRunnerSetNamespace}, roleBinding)
					if err != nil {
						return "", err
					}

					return roleBinding.RoleRef.Name, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(scaleSetListenerRoleName(autoScalingListener)), "Rolebinding should be created")

			// Check if pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingListener.Name, Namespace: autoScalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(autoScalingListener.Name), "Pod should be created")
		})
	})

	Context("When deleting a new AutoScalingListener", func() {
		It("It should cleanup all resources for a deleting AutoScalingListener before removing it", func() {
			// Waiting for the pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingListener.Name, Namespace: autoScalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(autoScalingListener.Name), "Pod should be created")

			// Delete the AutoScalingListener
			err := k8sClient.Delete(ctx, autoScalingListener)
			Expect(err).NotTo(HaveOccurred(), "failed to delete test AutoScalingListener")

			// Cleanup the listener pod
			Eventually(
				func() error {
					podList := new(corev1.PodList)
					err := k8sClient.List(ctx, podList, client.InNamespace(autoScalingListener.Namespace), client.MatchingFields{autoscalingRunnerSetOwnerKey: autoScalingListener.Name})
					if err != nil {
						return err
					}

					if len(podList.Items) > 0 {
						return fmt.Errorf("pod still exists")
					}

					return nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).ShouldNot(Succeed(), "failed to delete pod")

			// Cleanup the listener service account
			Eventually(
				func() error {
					serviceAccountList := new(corev1.ServiceAccountList)
					err := k8sClient.List(ctx, serviceAccountList, client.InNamespace(autoScalingListener.Namespace), client.MatchingFields{autoscalingRunnerSetOwnerKey: autoScalingListener.Name})
					if err != nil {
						return err
					}

					if len(serviceAccountList.Items) > 0 {
						return fmt.Errorf("service account still exists")
					}

					return nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).ShouldNot(Succeed(), "failed to delete service account")

			// The AutoScalingListener should be deleted
			Eventually(
				func() error {
					listenerList := new(actionsv1alpha1.AutoscalingListenerList)
					err := k8sClient.List(ctx, listenerList, client.InNamespace(autoScalingListener.Namespace), client.MatchingFields{".metadata.name": autoScalingListener.Name})
					if err != nil {
						return err
					}

					if len(listenerList.Items) > 0 {
						return fmt.Errorf("AutoScalingListener still exists")
					}
					return nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).ShouldNot(Succeed(), "failed to delete AutoScalingListener")
		})
	})

	Context("React to changes in the AutoScalingListener", func() {
		It("It should update role to match EphemeralRunnerSet", func() {
			// Waiting for the pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingListener.Name, Namespace: autoScalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(autoScalingListener.Name), "Pod should be created")

			// Update the AutoScalingListener
			updated := autoScalingListener.DeepCopy()
			updated.Spec.EphemeralRunnerSetName = "test-ers-updated"
			err := k8sClient.Patch(ctx, updated, client.MergeFrom(autoScalingListener))
			Expect(err).NotTo(HaveOccurred(), "failed to update test AutoScalingListener")

			// Check if role is updated with right rules
			role := new(rbacv1.Role)
			Eventually(
				func() ([]rbacv1.PolicyRule, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoScalingListener), Namespace: autoScalingListener.Spec.AutoscalingRunnerSetNamespace}, role)
					if err != nil {
						return nil, err
					}

					return role.Rules, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(rulesForListenerRole([]string{updated.Spec.EphemeralRunnerSetName})), "Role should be updated")
		})

		It("It should update mirror secrets to match secret used by AutoScalingRunnerSet", func() {
			// Waiting for the pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingListener.Name, Namespace: autoScalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(autoScalingListener.Name), "Pod should be created")

			// Update the secret
			updatedSecret := configSecret.DeepCopy()
			updatedSecret.Data["github_token"] = []byte(autoScalingListenerTestGitHubToken + "_updated")
			err := k8sClient.Update(ctx, updatedSecret)
			Expect(err).NotTo(HaveOccurred(), "failed to update test secret")

			updatedPod := pod.DeepCopy()
			updatedPod.Status.Phase = corev1.PodFailed
			err = k8sClient.Status().Update(ctx, updatedPod)
			Expect(err).NotTo(HaveOccurred(), "failed to update test pod to failed")

			// Check if mirror secret is updated with right data
			mirrorSecret := new(corev1.Secret)
			Eventually(
				func() (map[string][]byte, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerSecretMirrorName(autoScalingListener), Namespace: autoScalingListener.Namespace}, mirrorSecret)
					if err != nil {
						return nil, err
					}

					return mirrorSecret.Data, nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(BeEquivalentTo(updatedSecret.Data), "Mirror secret should be updated")

			// Check if we re-created a new pod
			Eventually(
				func() error {
					latestPod := new(corev1.Pod)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoScalingListener.Name, Namespace: autoScalingListener.Namespace}, latestPod)
					if err != nil {
						return err
					}
					if latestPod.UID == pod.UID {
						return fmt.Errorf("Pod should be recreated")
					}

					return nil
				},
				autoScalingListenerTestTimeout,
				autoScalingListenerTestInterval).Should(Succeed(), "Pod should be recreated")
		})
	})
})
