package actionsgithubcom

import (
	"context"
	"net/http"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"

	"github.com/actions/actions-runner-controller/github/actions/fake"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	gh_token    = "gh_token"
	timeout     = time.Second * 30
	interval    = time.Millisecond * 250
	runnerImage = "ghcr.io/actions/actions-runner:latest"
)

func newExampleRunner(name, namespace, configSecretName string) *v1alpha1.EphemeralRunner {
	return &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.EphemeralRunnerSpec{
			GitHubConfigUrl:    "https://github.com/owner/repo",
			GitHubConfigSecret: configSecretName,
			RunnerScaleSetId:   1,
			PodTemplateSpec: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    EphemeralRunnerContainerName,
							Image:   runnerImage,
							Command: []string{"/runner/run.sh"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "runner",
									MountPath: "/runner",
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:    "setup",
							Image:   runnerImage,
							Command: []string{"sh", "-c", "cp -r /actions-runner/* /runner/"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "runner",
									MountPath: "/runner",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "runner",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

}

var _ = Describe("EphemeralRunner", func() {

	Describe("Resource manipulation", func() {
		var ctx context.Context
		var cancel context.CancelFunc

		autoScalingNS := new(corev1.Namespace)
		configSecret := new(corev1.Secret)

		controller := new(EphemeralRunnerReconciler)
		ephemeralRunner := new(v1alpha1.EphemeralRunner)

		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			autoScalingNS = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "testns-autoscaling-runner" + RandStringRunes(5),
				},
			}
			err := k8sClient.Create(ctx, autoScalingNS)
			Expect(err).To(BeNil(), "failed to create test namespace for EphemeralRunner")

			configSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "github-config-secret",
					Namespace: autoScalingNS.Name,
				},
				Data: map[string][]byte{
					"github_token": []byte(gh_token),
				},
			}

			err = k8sClient.Create(ctx, configSecret)
			Expect(err).To(BeNil(), "failed to create config secret")

			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Namespace:          autoScalingNS.Name,
				MetricsBindAddress: "0",
			})
			Expect(err).To(BeNil(), "failed to create manager")

			controller = &EphemeralRunnerReconciler{
				Client:        mgr.GetClient(),
				Scheme:        mgr.GetScheme(),
				Log:           logf.Log,
				ActionsClient: fake.NewMultiClient(),
			}

			err = controller.SetupWithManager(mgr)
			Expect(err).To(BeNil(), "failed to setup controller")

			ephemeralRunner = newExampleRunner("test-runner", autoScalingNS.Name, configSecret.Name)
			err = k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil(), "failed to create ephemeral runner")

			go func() {
				defer GinkgoRecover()

				err := mgr.Start(ctx)
				Expect(err).To(BeNil(), "failed to start manager")
			}()
		})

		AfterEach(func() {
			defer cancel()

			err := k8sClient.Delete(ctx, autoScalingNS)
			Expect(err).To(BeNil(), "failed to delete test namespace for EphemeralRunner")
		})

		It("It should create/add all required resources for EphemeralRunner (finalizer, jit secret)", func() {
			created := new(v1alpha1.EphemeralRunner)
			// Check if finalizer is added
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, created)
					if err != nil {
						return "", err
					}
					if len(created.Finalizers) == 0 {
						return "", nil
					}
					return created.Finalizers[0], nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(ephemeralRunnerFinalizerName))

			Eventually(
				func() (bool, error) {
					secret := new(corev1.Secret)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, secret); err != nil {
						return false, err
					}

					_, ok := secret.Data[jitTokenKey]
					return ok, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (string, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(ephemeralRunner.Name))
		})

		It("It should re-create pod on failure", func() {
			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}).Should(BeEquivalentTo(true))

			err := k8sClient.Delete(ctx, pod)
			Expect(err).To(BeNil(), "failed to delete pod")

			pod = new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))
		})

		It("It should clean up resources when deleted", func() {
			// wait for pod to be created
			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}).Should(BeEquivalentTo(true))

			// create runner-linked pod
			runnerLinkedPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runner-linked-pod",
					Namespace: ephemeralRunner.Namespace,
					Labels: map[string]string{
						"runner-pod": ephemeralRunner.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "runner-linked-container",
							Image: "ubuntu:latest",
						},
					},
				},
			}

			err := k8sClient.Create(ctx, runnerLinkedPod)
			Expect(err).To(BeNil(), "failed to create runner linked pod")
			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: runnerLinkedPod.Name, Namespace: runnerLinkedPod.Namespace}, pod); err != nil {
						return false, nil
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			// create runner linked secret
			runnerLinkedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runner-linked-secret",
					Namespace: ephemeralRunner.Namespace,
					Labels: map[string]string{
						"runner-pod": ephemeralRunner.Name,
					},
				},
				Data: map[string][]byte{"test": []byte("test")},
			}

			err = k8sClient.Create(ctx, runnerLinkedSecret)
			Expect(err).To(BeNil(), "failed to create runner linked secret")
			Eventually(
				func() (bool, error) {
					secret := new(corev1.Secret)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: runnerLinkedSecret.Name, Namespace: runnerLinkedSecret.Namespace}, secret); err != nil {
						return false, nil
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			err = k8sClient.Delete(ctx, ephemeralRunner)
			Expect(err).To(BeNil(), "failed to delete ephemeral runner")

			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err == nil {
						return false, nil
					}
					return kerrors.IsNotFound(err), nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (bool, error) {
					secret := new(corev1.Secret)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, secret)
					if err == nil {
						return false, nil
					}
					return kerrors.IsNotFound(err), nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: runnerLinkedPod.Name, Namespace: runnerLinkedPod.Namespace}, pod)
					if err == nil {
						return false, nil
					}
					return kerrors.IsNotFound(err), nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (bool, error) {
					secret := new(corev1.Secret)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: runnerLinkedSecret.Name, Namespace: runnerLinkedSecret.Namespace}, secret)
					if err == nil {
						return false, nil
					}
					return kerrors.IsNotFound(err), nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (bool, error) {
					secret := new(corev1.Secret)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, secret)
					if err == nil {
						return false, nil
					}
					return kerrors.IsNotFound(err), nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (bool, error) {
					updated := new(v1alpha1.EphemeralRunner)
					err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
					if err == nil {
						return false, nil
					}
					return kerrors.IsNotFound(err), nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

		})

		It("It should eventually have runner id set", func() {
			Eventually(
				func() (int, error) {
					updatedEphemeralRunner := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updatedEphemeralRunner)
					if err != nil {
						return 0, err
					}
					return updatedEphemeralRunner.Status.RunnerId, nil
				},
				timeout,
				interval,
			).Should(BeNumerically(">", 0))
		})

		It("It should patch the ephemeral runner non terminating status", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			for _, phase := range []corev1.PodPhase{corev1.PodRunning, corev1.PodPending} {
				podCopy := pod.DeepCopy()
				pod.Status.Phase = phase
				// set container state to force status update
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name:  EphemeralRunnerContainerName,
					State: corev1.ContainerState{},
				})
				err := k8sClient.Status().Patch(ctx, pod, client.MergeFrom(podCopy))
				Expect(err).To(BeNil(), "failed to patch pod status")

				Eventually(
					func() (corev1.PodPhase, error) {
						updated := new(v1alpha1.EphemeralRunner)
						err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
						if err != nil {
							return "", err
						}
						return updated.Status.Phase, nil
					},
					timeout,
					interval,
				).Should(BeEquivalentTo(phase))
			}
		})

		It("It should not update phase if container state does not exist", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			pod.Status.Phase = corev1.PodRunning
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "failed to patch pod status")

			Consistently(
				func() (corev1.PodPhase, error) {
					updated := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated); err != nil {
						return corev1.PodUnknown, err
					}
					return updated.Status.Phase, nil
				},
				timeout,
			).Should(BeEquivalentTo(""))
		})

		It("It should not re-create pod indefinitely", func() {
			pod := new(corev1.Pod)
			failures := 0
			for i := 0; i < 6; i++ {
				Eventually(
					func() (bool, error) {
						if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
							return false, err
						}
						return true, nil
					},
					timeout,
					interval,
				).Should(BeEquivalentTo(true))

				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
						},
					},
				})
				err := k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "Failed to update pod status")

				failures++

				updated := new(v1alpha1.EphemeralRunner)
				Eventually(func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
					if err != nil {
						return false, err
					}
					return len(updated.Status.Failures) == failures, nil
				}, timeout, interval).Should(BeEquivalentTo(true))
			}

			Eventually(func() (bool, error) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				if err == nil {
					return false, nil
				}
				return kerrors.IsNotFound(err), nil
			}, timeout, interval).Should(BeEquivalentTo(true))
		})

		It("It should re-create pod on eviction", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			pod.Status.Phase = corev1.PodFailed
			pod.Status.Reason = "Evicted"
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:  EphemeralRunnerContainerName,
				State: corev1.ContainerState{},
			})
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "failed to patch pod status")

			updated := new(v1alpha1.EphemeralRunner)
			Eventually(func() (bool, error) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
				if err != nil {
					return false, err
				}
				return len(updated.Status.Failures) == 1, nil
			}, timeout, interval).Should(BeEquivalentTo(true))

			// should re-create after failure
			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

		})

		It("It should re-create pod on exit status 0, but runner exists within the service", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (bool, error) {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
					},
				},
			})
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "failed to update pod status")

			updated := new(v1alpha1.EphemeralRunner)
			Eventually(func() (bool, error) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
				if err != nil {
					return false, err
				}
				return len(updated.Status.Failures) == 1, nil
			}, timeout, interval).Should(BeEquivalentTo(true))

			// should re-create after failure
			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))
		})

		It("It should not set the phase to succeeded without pod termination status", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (bool, error) {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return false, err
					}
					return true, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(true))

			// first set phase to running
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{
						StartedAt: metav1.Now(),
					},
				},
			})
			pod.Status.Phase = corev1.PodRunning
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil())

			Eventually(
				func() (corev1.PodPhase, error) {
					updated := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated); err != nil {
						return "", err
					}
					return updated.Status.Phase, nil
				},
				timeout,
				interval,
			).Should(BeEquivalentTo(corev1.PodRunning))

			// set phase to succeeded
			pod.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil())

			Consistently(
				func() (corev1.PodPhase, error) {
					updated := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated); err != nil {
						return "", err
					}
					return updated.Status.Phase, nil
				},
				timeout,
			).Should(BeEquivalentTo(corev1.PodRunning))
		})
	})

	Describe("Checking the API", func() {
		var ctx context.Context
		var cancel context.CancelFunc

		autoScalingNS := new(corev1.Namespace)
		configSecret := new(corev1.Secret)

		var mgr manager.Manager

		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			autoScalingNS = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "testns-autoscaling-runner" + RandStringRunes(5),
				},
			}
			err := k8sClient.Create(ctx, autoScalingNS)
			Expect(err).To(BeNil(), "failed to create test namespace for EphemeralRunner")

			configSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "github-config-secret",
					Namespace: autoScalingNS.Name,
				},
				Data: map[string][]byte{
					"github_token": []byte(gh_token),
				},
			}

			err = k8sClient.Create(ctx, configSecret)
			Expect(err).To(BeNil(), "failed to create config secret")

			mgr, err = ctrl.NewManager(cfg, ctrl.Options{
				Namespace:          autoScalingNS.Name,
				MetricsBindAddress: "0",
			})
			Expect(err).To(BeNil(), "failed to create manager")

		})

		AfterEach(func() {
			defer cancel()

			err := k8sClient.Delete(ctx, autoScalingNS)
			Expect(err).To(BeNil(), "failed to delete test namespace for EphemeralRunner")
		})

		It("It should set the Phase to Succeeded", func() {
			controller := &EphemeralRunnerReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
				Log:    logf.Log,
				ActionsClient: fake.NewMultiClient(
					fake.WithDefaultClient(
						fake.NewFakeClient(
							fake.WithGetRunner(
								nil,
								&actions.ActionsError{
									StatusCode:    http.StatusNotFound,
									ExceptionName: "AgentNotFoundException",
								},
							),
						),
						nil,
					),
				),
			}

			err := controller.SetupWithManager(mgr)
			Expect(err).To(BeNil(), "failed to setup controller")

			go func() {
				defer GinkgoRecover()

				err := mgr.Start(ctx)
				Expect(err).To(BeNil(), "failed to start manager")
			}()

			ephemeralRunner := newExampleRunner("test-runner", autoScalingNS.Name, configSecret.Name)

			err = k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil())

			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}, timeout, interval).Should(BeEquivalentTo(true))

			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
					},
				},
			})
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "failed to update pod status")

			updated := new(v1alpha1.EphemeralRunner)
			Eventually(func() (corev1.PodPhase, error) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
				if err != nil {
					return "", nil
				}
				return updated.Status.Phase, nil
			}, timeout, interval).Should(BeEquivalentTo(corev1.PodSucceeded))
		})
	})
})
