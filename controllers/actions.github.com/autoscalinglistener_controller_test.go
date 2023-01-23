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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

const (
	autoscalingListenerTestTimeout     = time.Second * 5
	autoscalingListenerTestInterval    = time.Millisecond * 250
	autoscalingListenerTestGitHubToken = "gh_token"
)

var _ = Describe("Test AutoScalingListener controller", func() {
	var ctx context.Context
	var cancel context.CancelFunc
	autoscalingNS := new(corev1.Namespace)
	autoscalingRunnerSet := new(v1alpha1.AutoscalingRunnerSet)
	configSecret := new(corev1.Secret)
	autoscalingListener := new(v1alpha1.AutoscalingListener)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.TODO())
		autoscalingNS = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling-listener" + RandStringRunes(5)},
		}

		err := k8sClient.Create(ctx, autoscalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace for AutoScalingRunnerSet")

		configSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "github-config-secret",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"github_token": []byte(autoscalingListenerTestGitHubToken),
			},
		}

		err = k8sClient.Create(ctx, configSecret)
		Expect(err).NotTo(HaveOccurred(), "failed to create config secret")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Namespace:          autoscalingNS.Name,
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

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		defer cancel()

		err := k8sClient.Delete(ctx, autoscalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace for AutoScalingRunnerSet")
	})

	Context("When creating a new AutoScalingListener", func() {
		It("It should create/add all required resources for a new AutoScalingListener (finalizer, secret, service account, role, rolebinding, pod)", func() {
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

			// Delete the AutoScalingListener
			err := k8sClient.Delete(ctx, autoscalingListener)
			Expect(err).NotTo(HaveOccurred(), "failed to delete test AutoScalingListener")

			// Cleanup the listener pod
			Eventually(
				func() error {
					podList := new(corev1.PodList)
					err := k8sClient.List(ctx, podList, client.InNamespace(autoscalingListener.Namespace), client.MatchingFields{autoscalingRunnerSetOwnerKey: autoscalingListener.Name})
					if err != nil {
						return err
					}

					if len(podList.Items) > 0 {
						return fmt.Errorf("pod still exists")
					}

					return nil
				},
				autoscalingListenerTestTimeout,
				autoscalingListenerTestInterval).ShouldNot(Succeed(), "failed to delete pod")

			// Cleanup the listener service account
			Eventually(
				func() error {
					serviceAccountList := new(corev1.ServiceAccountList)
					err := k8sClient.List(ctx, serviceAccountList, client.InNamespace(autoscalingListener.Namespace), client.MatchingFields{autoscalingRunnerSetOwnerKey: autoscalingListener.Name})
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
			updatedPod.Status.Phase = corev1.PodFailed
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

var _ = Describe("Test AutoScalingListener controller without proxy", func() {
	var ctx context.Context
	var cancel context.CancelFunc
	autoscalingNS := new(corev1.Namespace)
	autoscalingRunnerSet := new(v1alpha1.AutoscalingRunnerSet)
	configSecret := new(corev1.Secret)
	autoscalingListener := new(v1alpha1.AutoscalingListener)

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
		ctx, cancel = context.WithCancel(context.TODO())
		autoscalingNS = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-autoscaling-listener" + RandStringRunes(5)},
		}

		err := k8sClient.Create(ctx, autoscalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace for AutoScalingRunnerSet")

		configSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "github-config-secret",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"github_token": []byte(autoscalingListenerTestGitHubToken),
			},
		}

		err = k8sClient.Create(ctx, configSecret)
		Expect(err).NotTo(HaveOccurred(), "failed to create config secret")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Namespace:          autoscalingNS.Name,
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

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		defer cancel()

		err := k8sClient.Delete(ctx, autoscalingNS)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace for AutoScalingRunnerSet")
	})

	It("It should create pod with proxy environment variables set", func() {
		proxy := &v1alpha1.ProxyConfig{
			HTTP: &v1alpha1.ProxyServerConfig{
				Url: "http://localhost:8080",
			},
			HTTPS: &v1alpha1.ProxyServerConfig{
				Url: "https://localhost:8443",
			},
			NoProxy: []string{
				"http://localhost:8088",
				"https://localhost:8088",
			},
		}

		createRunnerSetAndListener(proxy)

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

		expectedValues := map[string]string{
			EnvVarHTTPProxy:  "http://localhost:8080",
			EnvVarHTTPSProxy: "https://localhost:8443",
			EnvVarNoProxy:    "http://localhost:8088,https://localhost:8088",
		}

		envFrequency := map[string]int{}

		for i := range pod.Spec.Containers {
			c := &pod.Spec.Containers[i]
			if c.Name == autoscalingListenerContainerName {
				for _, env := range c.Env {
					switch env.Name {
					case EnvVarHTTPProxy:
						envFrequency[EnvVarHTTPProxy]++
						Expect(env.Value).To(BeEquivalentTo(expectedValues[EnvVarHTTPProxy]))
					case EnvVarHTTPSProxy:
						envFrequency[EnvVarHTTPSProxy]++
						Expect(env.Value).To(BeEquivalentTo(expectedValues[EnvVarHTTPSProxy]))
					case EnvVarNoProxy:
						envFrequency[EnvVarNoProxy]++
						Expect(env.Value).To(BeEquivalentTo(expectedValues[EnvVarNoProxy]))
					}
				}
				break
			}
		}

		for _, name := range []string{EnvVarHTTPProxy, EnvVarHTTPSProxy, EnvVarNoProxy} {
			frequency := envFrequency[name]
			Expect(frequency).To(BeEquivalentTo(1), fmt.Sprintf("expected %s env variable frequency to be 1, got %d", name, frequency))
		}
	})

	It("It should create proxy environment variables with username:password set", func() {
		httpSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "httpsecret",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"username": []byte("testuser"),
				"password": []byte("testpassword"),
			},
			Type: corev1.SecretTypeOpaque,
		}
		err := k8sClient.Create(ctx, httpSecret)
		Expect(err).To(BeNil(), "failed to create http secret")

		httpsSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "httpssecret",
				Namespace: autoscalingNS.Name,
			},
			Data: map[string][]byte{
				"username": []byte("testuser"),
				"password": []byte("testpassword"),
			},
			Type: corev1.SecretTypeOpaque,
		}

		err = k8sClient.Create(ctx, httpsSecret)
		Expect(err).To(BeNil(), "failed to create https secret")

		proxy := &v1alpha1.ProxyConfig{
			HTTP: &v1alpha1.ProxyServerConfig{
				Url:                 "http://localhost:8080",
				CredentialSecretRef: httpSecret.Name,
			},
			HTTPS: &v1alpha1.ProxyServerConfig{
				Url:                 "https://localhost:8443",
				CredentialSecretRef: httpsSecret.Name,
			},
			NoProxy: []string{
				"http://localhost:8088",
				"https://localhost:8088",
			},
		}
		createRunnerSetAndListener(proxy)

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

		expectedValues := map[string]string{
			EnvVarHTTPProxy:  fmt.Sprintf("http://%s:%s@localhost:8080", httpSecret.Data["username"], httpSecret.Data["password"]),
			EnvVarHTTPSProxy: fmt.Sprintf("https://%s:%s@localhost:8443", httpsSecret.Data["username"], httpsSecret.Data["password"]),
			EnvVarNoProxy:    "http://localhost:8088,https://localhost:8088",
		}

		envFrequency := map[string]int{}
		for i := range pod.Spec.Containers {
			c := &pod.Spec.Containers[i]
			if c.Name == autoscalingListenerContainerName {
				for _, env := range c.Env {
					switch env.Name {
					case EnvVarHTTPProxy:
						envFrequency[EnvVarHTTPProxy]++
						Expect(env.Value).To(BeEquivalentTo(expectedValues[EnvVarHTTPProxy]))
					case EnvVarHTTPSProxy:
						envFrequency[EnvVarHTTPSProxy]++
						Expect(env.Value).To(BeEquivalentTo(expectedValues[EnvVarHTTPSProxy]))
					case EnvVarNoProxy:
						envFrequency[EnvVarNoProxy]++
						Expect(env.Value).To(BeEquivalentTo(expectedValues[EnvVarNoProxy]))
					}
				}
				break
			}
		}

		for _, name := range []string{EnvVarHTTPProxy, EnvVarHTTPSProxy, EnvVarNoProxy} {
			frequency := envFrequency[name]
			Expect(frequency).To(BeEquivalentTo(1), fmt.Sprintf("expected %s env variable frequency to be 1, got %d", name, frequency))
		}
	})
})
