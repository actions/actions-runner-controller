package actionsgithubcom

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	listenerconfig "github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

const (
	autoscalingListenerTestTimeout     = time.Second * 20
	autoscalingListenerTestInterval    = time.Millisecond * 250
	autoscalingListenerTestGitHubToken = "gh_token"
)

var _ = Describe("Test AutoScalingListener controller", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	var configSecret *corev1.Secret
	var autoscalingListener *v1alpha1.AutoscalingListener

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		min := 1
		max := 10
		autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
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

		err = k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		autoscalingListener = &v1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asl",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingListenerSpec{
				GitHubConfigUrl:               "https://github.com/owner/repo",
				GitHubConfigSecret:            configSecret.Name,
				RunnerScaleSetId:              1,
				AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
			},
		}

		err = k8sClient.Create(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingListener")

		startManagers(GinkgoT(), mgr)
	})

	Context("When creating a new AutoScalingListener", func() {
		It("It should create/add all required resources for a new AutoScalingListener (finalizer, secret, service account, role, rolebinding, pod)", func() {
			config := new(corev1.Secret)
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerConfigName(autoscalingListener), Namespace: configSecret.Namespace}, config)
					if err != nil {
						return err
					}
					return nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(Succeed(), "Config secret should be created")

			// Check if finalizer is added
			created := new(v1alpha1.AutoscalingListener)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(autoscalingListenerFinalizerName), "AutoScalingListener should have a finalizer")

			// Check if secret is created
			mirrorSecret := new(corev1.Secret)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerSecretMirrorName(autoscalingListener), Namespace: autoscalingListener.Namespace}, mirrorSecret)
					if err != nil {
						return "", err
					}
					return string(mirrorSecret.Data["github_token"]), nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(autoscalingListenerTestGitHubToken), "Mirror secret should be created")

			// Check if service account is created
			serviceAccount := new(corev1.ServiceAccount)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerServiceAccountName(autoscalingListener), Namespace: autoscalingListener.Namespace}, serviceAccount)
					if err != nil {
						return "", err
					}
					return serviceAccount.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(scaleSetListenerServiceAccountName(autoscalingListener)), "Service account should be created")

			// Check if role is created
			role := new(rbacv1.Role)
			Eventually(
				func() ([]rbacv1.PolicyRule, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoscalingListener), Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace}, role)
					if err != nil {
						return nil, err
					}

					return role.Rules, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(rulesForListenerRole([]string{autoscalingListener.Spec.EphemeralRunnerSetName})), "Role should be created")

			// Check if rolebinding is created
			roleBinding := new(rbacv1.RoleBinding)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoscalingListener), Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace}, roleBinding)
					if err != nil {
						return "", err
					}

					return roleBinding.RoleRef.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(scaleSetListenerRoleName(autoscalingListener)), "Rolebinding should be created")

			// Check if pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")
		})
	})

	Context("When deleting a new AutoScalingListener", func() {
		It("It should cleanup all resources for a deleting AutoScalingListener before removing it", func() {
			// Waiting for the pod to be created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			// Delete the AutoScalingListener
			err := k8sClient.Delete(ctx, autoscalingListener)
			Expect(err).NotTo(HaveOccurred(), "failed to delete test AutoScalingListener")

			// Cleanup the listener pod
			Eventually(
				func() error {
					podList := new(corev1.PodList)
					err := k8sClient.List(ctx, podList, client.InNamespace(autoscalingListener.Namespace), client.MatchingFields{resourceOwnerKey: autoscalingListener.Name})
					if err != nil {
						return err
					}

					if len(podList.Items) > 0 {
						return fmt.Errorf("pod still exists")
					}

					return nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).ShouldNot(Succeed(), "failed to delete pod")

			// Cleanup the listener role binding
			Eventually(
				func() bool {
					roleBinding := new(rbacv1.RoleBinding)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoscalingListener), Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace}, roleBinding)
					return kerrors.IsNotFound(err)
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeTrue(), "failed to delete role binding")

			// Cleanup the listener role
			Eventually(
				func() bool {
					role := new(rbacv1.Role)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoscalingListener), Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace}, role)
					return kerrors.IsNotFound(err)
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeTrue(), "failed to delete role")

			// Cleanup the listener config
			Eventually(
				func() bool {
					config := new(corev1.Secret)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerConfigName(autoscalingListener), Namespace: autoscalingListener.Namespace}, config)
					return kerrors.IsNotFound(err)
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeTrue(), "failed to delete config secret")

			// Cleanup the listener service account
			Eventually(
				func() error {
					serviceAccountList := new(corev1.ServiceAccountList)
					err := k8sClient.List(ctx, serviceAccountList, client.InNamespace(autoscalingListener.Namespace), client.MatchingFields{resourceOwnerKey: autoscalingListener.Name})
					if err != nil {
						return err
					}

					if len(serviceAccountList.Items) > 0 {
						return fmt.Errorf("service account still exists")
					}

					return nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).ShouldNot(Succeed(), "failed to delete service account")

			// The AutoScalingListener should be deleted
			Eventually(
				func() error {
					listenerList := new(v1alpha1.AutoscalingListenerList)
					err := k8sClient.List(ctx, listenerList, client.InNamespace(autoscalingListener.Namespace), client.MatchingFields{".metadata.name": autoscalingListener.Name})
					if err != nil {
						return err
					}

					if len(listenerList.Items) > 0 {
						return fmt.Errorf("AutoScalingListener still exists")
					}
					return nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).ShouldNot(Succeed(), "failed to delete AutoScalingListener")
		})
	})

	Context("React to changes in the AutoScalingListener", func() {
		It("It should update role to match EphemeralRunnerSet", func() {
			// Waiting for the pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			// Update the AutoScalingListener
			updated := autoscalingListener.DeepCopy()
			updated.Spec.EphemeralRunnerSetName = "test-ers-updated"
			err := k8sClient.Patch(ctx, updated, client.MergeFrom(autoscalingListener))
			Expect(err).NotTo(HaveOccurred(), "failed to update test AutoScalingListener")

			// Check if role is updated with right rules
			role := new(rbacv1.Role)
			Eventually(
				func() ([]rbacv1.PolicyRule, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerRoleName(autoscalingListener), Namespace: autoscalingListener.Spec.AutoscalingRunnerSetNamespace}, role)
					if err != nil {
						return nil, err
					}

					return role.Rules, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(rulesForListenerRole([]string{updated.Spec.EphemeralRunnerSetName})), "Role should be updated")
		})

		It("It should re-create pod whenever listener container is terminated", func() {
			// Waiting for the pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			oldPodUID := string(pod.UID)
			updated := pod.DeepCopy()
			updated.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name: autoscalingListenerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 0,
						},
					},
				},
			}
			err := k8sClient.Status().Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update test pod")

			// Waiting for the new pod is created
			Eventually(
				func() (string, error) {
					pod := new(corev1.Pod)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return string(pod.UID), nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).ShouldNot(BeEquivalentTo(oldPodUID), "Pod should be re-created")
		})

		It("It should update mirror secrets to match secret used by AutoScalingRunnerSet", func() {
			// Waiting for the pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			// Update the secret
			updatedSecret := configSecret.DeepCopy()
			updatedSecret.Data["github_token"] = []byte(autoscalingListenerTestGitHubToken + "_updated")
			err := k8sClient.Update(ctx, updatedSecret)
			Expect(err).NotTo(HaveOccurred(), "failed to update test secret")

			updatedPod := pod.DeepCopy()
			// Ignore status running and consult the container state
			updatedPod.Status.Phase = corev1.PodRunning
			updatedPod.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name: autoscalingListenerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
						},
					},
				},
			}
			err = k8sClient.Status().Update(ctx, updatedPod)
			Expect(err).NotTo(HaveOccurred(), "failed to update test pod to failed")

			// Check if mirror secret is updated with right data
			mirrorSecret := new(corev1.Secret)
			Eventually(
				func() (map[string][]byte, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerSecretMirrorName(autoscalingListener), Namespace: autoscalingListener.Namespace}, mirrorSecret)
					if err != nil {
						return nil, err
					}

					return mirrorSecret.Data, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(BeEquivalentTo(updatedSecret.Data), "Mirror secret should be updated")

			// Check if we re-created a new pod
			Eventually(
				func() error {
					latestPod := new(corev1.Pod)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, latestPod)
					if err != nil {
						return err
					}
					if latestPod.UID == pod.UID {
						return fmt.Errorf("Pod should be recreated")
					}

					return nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).Should(Succeed(), "Pod should be recreated")
		})
	})
})

var _ = Describe("Test AutoScalingListener customization", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	var configSecret *corev1.Secret
	var autoscalingListener *v1alpha1.AutoscalingListener

	var runAsUser int64 = 1001
	const sidecarContainerName = "sidecar"

	listenerPodTemplate := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            autoscalingListenerContainerName,
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: &runAsUser,
					},
				},
				{
					Name:            sidecarContainerName,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Image:           "busybox",
				},
			},
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser: &runAsUser,
			},
		},
	}

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		min := 1
		max := 10
		autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
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

		err = k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		autoscalingListener = &v1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asltest",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingListenerSpec{
				GitHubConfigUrl:               "https://github.com/owner/repo",
				GitHubConfigSecret:            configSecret.Name,
				RunnerScaleSetId:              1,
				AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
				Template:                      &listenerPodTemplate,
			},
		}

		err = k8sClient.Create(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingListener")

		startManagers(GinkgoT(), mgr)
	})

	Context("When creating a new AutoScalingListener", func() {
		It("It should create customized pod with applied configuration", func() {
			// Check if finalizer is added
			created := new(v1alpha1.AutoscalingListener)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeEquivalentTo(autoscalingListenerFinalizerName), "AutoScalingListener should have a finalizer")

			// Check if config is created
			config := new(corev1.Secret)
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerConfigName(autoscalingListener), Namespace: autoscalingListener.Namespace}, config)
					return err
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(Succeed(), "Config secret should be created")

			// Check if pod is created
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			Expect(pod.Spec.SecurityContext.RunAsUser).To(Equal(&runAsUser), "Pod should have the correct security context")

			Expect(pod.Spec.Containers[0].Name).To(Equal(autoscalingListenerContainerName), "Pod should have the correct container name")
			Expect(pod.Spec.Containers[0].SecurityContext.RunAsUser).To(Equal(&runAsUser), "Pod should have the correct security context")
			Expect(pod.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullAlways), "Pod should have the correct image pull policy")

			Expect(pod.Spec.Containers[1].Name).To(Equal(sidecarContainerName), "Pod should have the correct container name")
			Expect(pod.Spec.Containers[1].Image).To(Equal("busybox"), "Pod should have the correct image")
			Expect(pod.Spec.Containers[1].ImagePullPolicy).To(Equal(corev1.PullIfNotPresent), "Pod should have the correct image pull policy")
		})
	})

	Context("When AutoscalingListener pod has interuptions", func() {
		It("Should re-create pod when it is deleted", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			Expect(len(pod.Spec.Containers)).To(Equal(2), "Pod should have 2 containers")
			oldPodUID := string(pod.UID)

			err := k8sClient.Delete(ctx, pod)
			Expect(err).NotTo(HaveOccurred(), "failed to delete pod")

			pod = new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return string(pod.UID), nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).ShouldNot(BeEquivalentTo(oldPodUID), "Pod should be created")
		})

		It("Should re-create pod when the listener pod is terminated", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).Should(BeEquivalentTo(autoscalingListener.Name), "Pod should be created")

			updated := pod.DeepCopy()
			oldPodUID := string(pod.UID)
			updated.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name: autoscalingListenerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
						},
					},
				},
				{
					Name: sidecarContainerName,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			}
			err := k8sClient.Status().Update(ctx, updated)
			Expect(err).NotTo(HaveOccurred(), "failed to update pod status")

			pod = new(corev1.Pod)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace}, pod)
					if err != nil {
						return "", err
					}

					return string(pod.UID), nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval,
			).ShouldNot(BeEquivalentTo(oldPodUID), "Pod should be created")
		})
	})
})

var _ = Describe("Test AutoScalingListener controller with proxy", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	var configSecret *corev1.Secret
	var autoscalingListener *v1alpha1.AutoscalingListener

	createRunnerSetAndListener := func(proxy *v1alpha1.ProxyConfig) {
		min := 1
		max := 10
		autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &max,
				MinRunners:         &min,
				Proxy:              proxy,
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

		autoscalingListener = &v1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asl",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingListenerSpec{
				GitHubConfigUrl:               "https://github.com/owner/repo",
				GitHubConfigSecret:            configSecret.Name,
				RunnerScaleSetId:              1,
				AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
				Proxy:                         proxy,
			},
		}

		err = k8sClient.Create(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingListener")
	}

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)
	})

	It("should create a secret in the listener namespace containing proxy details, use it to populate env vars on the pod and should delete it as part of cleanup", func() {
		proxyCredentials := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "proxy-credentials",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"username": []byte("test"),
				"password": []byte("password"),
			},
		}

		err := k8sClient.Create(ctx, proxyCredentials)
		Expect(err).NotTo(HaveOccurred(), "failed to create proxy credentials secret")

		proxy := &v1alpha1.ProxyConfig{
			HTTP: &v1alpha1.ProxyServerConfig{
				Url:                 "http://localhost:8080",
				CredentialSecretRef: "proxy-credentials",
			},
			HTTPS: &v1alpha1.ProxyServerConfig{
				Url:                 "https://localhost:8443",
				CredentialSecretRef: "proxy-credentials",
			},
			NoProxy: []string{
				"example.com",
				"example.org",
			},
		}

		createRunnerSetAndListener(proxy)

		var proxySecret corev1.Secret
		Eventually(
			func(g Gomega) {
				err := k8sClient.Get(
					ctx,
					types.NamespacedName{Name: proxyListenerSecretName(autoscalingListener), Namespace: autoscalingNS.Name},
					&proxySecret,
				)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get secret")
				expected, err := autoscalingListener.Spec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
					var secret corev1.Secret
					err := k8sClient.Get(ctx, types.NamespacedName{Name: s, Namespace: autoscalingNS.Name}, &secret)
					if err != nil {
						return nil, err
					}
					return &secret, nil
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to convert proxy config to secret data")
				g.Expect(proxySecret.Data).To(Equal(expected))
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(Succeed(), "failed to create secret with proxy details")

		// wait for listener pod to be created
		Eventually(
			func(g Gomega) {
				pod := new(corev1.Pod)
				err := k8sClient.Get(
					ctx,
					client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace},
					pod,
				)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get pod")

				g.Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
					Name: "http_proxy",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: proxyListenerSecretName(autoscalingListener)},
							Key:                  "http_proxy",
						},
					},
				}), "http_proxy environment variable not found")

				g.Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
					Name: "https_proxy",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: proxyListenerSecretName(autoscalingListener)},
							Key:                  "https_proxy",
						},
					},
				}), "https_proxy environment variable not found")

				g.Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
					Name: "no_proxy",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: proxyListenerSecretName(autoscalingListener)},
							Key:                  "no_proxy",
						},
					},
				}), "no_proxy environment variable not found")
			},
			autoscalingListenerTestTimeout,
			autoscalingListenerTestInterval).Should(Succeed(), "failed to create listener pod with proxy details")

		// Delete the AutoScalingListener
		err = k8sClient.Delete(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test AutoScalingListener")

		Eventually(
			func(g Gomega) {
				var proxySecret corev1.Secret
				err := k8sClient.Get(
					ctx,
					types.NamespacedName{Name: proxyListenerSecretName(autoscalingListener), Namespace: autoscalingNS.Name},
					&proxySecret,
				)
				g.Expect(kerrors.IsNotFound(err)).To(BeTrue())
			},
			autoscalingListenerTestTimeout,
			autoscalingListenerTestInterval).Should(Succeed(), "failed to delete secret with proxy details")
	})
})

var _ = Describe("Test AutoScalingListener controller with template modification", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	var configSecret *corev1.Secret
	var autoscalingListener *v1alpha1.AutoscalingListener

	createRunnerSetAndListener := func(listenerTemplate *corev1.PodTemplateSpec) {
		min := 1
		max := 10
		autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
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
				ListenerTemplate: listenerTemplate,
			},
		}

		err := k8sClient.Create(ctx, autoscalingRunnerSet)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		autoscalingListener = &v1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asl",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingListenerSpec{
				GitHubConfigUrl:               "https://github.com/owner/repo",
				GitHubConfigSecret:            configSecret.Name,
				RunnerScaleSetId:              1,
				AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
				Template:                      listenerTemplate,
			},
		}

		err = k8sClient.Create(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingListener")
	}

	BeforeEach(func() {
		ctx = context.Background()
		autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
		configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
		}
		err := controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		startManagers(GinkgoT(), mgr)
	})

	It("Should create listener pod with modified spec", func() {
		runAsUser1001 := int64(1001)
		runAsUser1000 := int64(1000)
		tmpl := &corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"test-annotation-key": "test-annotation-value",
				},
				Labels: map[string]string{
					"test-label-key": "test-label-value",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:            autoscalingListenerContainerName,
						ImagePullPolicy: corev1.PullAlways,
						SecurityContext: &corev1.SecurityContext{
							RunAsUser: &runAsUser1001,
						},
					},
					{
						Name:            "sidecar",
						ImagePullPolicy: corev1.PullIfNotPresent,
						Image:           "busybox",
					},
				},
				SecurityContext: &corev1.PodSecurityContext{
					RunAsUser: &runAsUser1000,
				},
			},
		}

		createRunnerSetAndListener(tmpl)

		// wait for listener pod to be created
		Eventually(
			func(g Gomega) {
				pod := new(corev1.Pod)
				err := k8sClient.Get(
					ctx,
					client.ObjectKey{Name: autoscalingListener.Name, Namespace: autoscalingListener.Namespace},
					pod,
				)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get pod")

				g.Expect(pod.ObjectMeta.Annotations).To(HaveKeyWithValue("test-annotation-key", "test-annotation-value"), "pod annotations should be copied from runner set template")
				g.Expect(pod.ObjectMeta.Labels).To(HaveKeyWithValue("test-label-key", "test-label-value"), "pod labels should be copied from runner set template")
			},
			autoscalingListenerTestTimeout,
			autoscalingListenerTestInterval).Should(Succeed(), "failed to create listener pod with proxy details")

		// Delete the AutoScalingListener
		err := k8sClient.Delete(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test AutoScalingListener")

		Eventually(
			func(g Gomega) {
				var proxySecret corev1.Secret
				err := k8sClient.Get(
					ctx,
					types.NamespacedName{Name: proxyListenerSecretName(autoscalingListener), Namespace: autoscalingNS.Name},
					&proxySecret,
				)
				g.Expect(kerrors.IsNotFound(err)).To(BeTrue())
			},
			autoscalingListenerTestTimeout,
			autoscalingListenerTestInterval).Should(Succeed(), "failed to delete secret with proxy details")
	})
})

var _ = Describe("Test GitHub Server TLS configuration", func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var autoscalingNS *corev1.Namespace
	var autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet
	var configSecret *corev1.Secret
	var autoscalingListener *v1alpha1.AutoscalingListener
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

		controller := &AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Log:    logf.Log,
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		min := 1
		max := 10
		autoscalingRunnerSet = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
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
				MaxRunners: &max,
				MinRunners: &min,
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

		autoscalingListener = &v1alpha1.AutoscalingListener{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asl",
				Namespace: autoscalingNS.Name,
			},
			Spec: v1alpha1.AutoscalingListenerSpec{
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
				RunnerScaleSetId:              1,
				AutoscalingRunnerSetNamespace: autoscalingRunnerSet.Namespace,
				AutoscalingRunnerSetName:      autoscalingRunnerSet.Name,
				EphemeralRunnerSetName:        "test-ers",
				MaxRunners:                    10,
				MinRunners:                    1,
				Image:                         "ghcr.io/owner/repo",
			},
		}

		err = k8sClient.Create(ctx, autoscalingListener)
		Expect(err).NotTo(HaveOccurred(), "failed to create AutoScalingListener")

		startManagers(GinkgoT(), mgr)
	})

	Context("When creating a new AutoScalingListener", func() {
		It("It should set the certificates in the config of the pod", func() {
			config := new(corev1.Secret)
			Eventually(
				func(g Gomega) {
					err := k8sClient.Get(
						ctx,
						client.ObjectKey{
							Name:      scaleSetListenerConfigName(autoscalingListener),
							Namespace: autoscalingListener.Namespace,
						},
						config,
					)

					g.Expect(err).NotTo(HaveOccurred(), "failed to get pod")

					g.Expect(config.Data["config.json"]).ToNot(BeEmpty(), "listener configuration file should not be empty")

					var listenerConfig listenerconfig.Config
					err = json.Unmarshal(config.Data["config.json"], &listenerConfig)
					g.Expect(err).NotTo(HaveOccurred(), "failed to parse listener configuration file")

					cert, err := os.ReadFile(filepath.Join(
						"../../",
						"github",
						"actions",
						"testdata",
						"rootCA.crt",
					))
					g.Expect(err).NotTo(HaveOccurred(), "failed to read rootCA.crt")

					g.Expect(listenerConfig.ServerRootCA).To(
						BeEquivalentTo(string(cert)),
						"GITHUB_SERVER_ROOT_CA should be the rootCA.crt",
					)
				}).
				WithTimeout(autoscalingRunnerSetTestTimeout).
				WithPolling(autoscalingListenerTestInterval).
				Should(Succeed(), "failed to create pod with volume and env variable")
		})
	})
})
