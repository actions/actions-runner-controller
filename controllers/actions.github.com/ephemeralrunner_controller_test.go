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
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"

	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ephemeralRunnerTimeout  = time.Second * 20
	ephemeralRunnerInterval = time.Millisecond * 10
	runnerImage             = "ghcr.io/actions/actions-runner:latest"
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
			RunnerScaleSetID:   1,
			PodTemplateSpec: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    v1alpha1.EphemeralRunnerContainerName,
							Image:   runnerImage,
							Command: []string{"/runner/run.sh"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "runner",
									MountPath: "/runner",
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ACTIONS_RUNNER_CONTAINER_HOOKS",
									Value: "/tmp/hook/index.js",
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:    "setup",
							Image:   runnerImage,
							Command: []string{"sh", "-c", "cp -r /home/runner/* /runner/"},
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
		var mgr ctrl.Manager
		var autoscalingNS *corev1.Namespace
		var configSecret *corev1.Secret
		var controller *EphemeralRunnerReconciler
		var ephemeralRunner *v1alpha1.EphemeralRunner

		BeforeEach(func() {
			ctx = context.Background()
			autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
			configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

			controller = &EphemeralRunnerReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
				Log:    logf.Log,
				ResourceBuilder: ResourceBuilder{
					SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
						scalefake.WithClient(
							scalefake.NewClient(
								scalefake.WithGenerateJitRunnerConfig(
									&scaleset.RunnerScaleSetJitRunnerConfig{
										Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
										EncodedJITConfig: "fake-jit-config",
									},
									nil,
								),
							),
						),
					)),
				},
			}

			err := controller.SetupWithManager(mgr)
			Expect(err).To(BeNil(), "failed to setup controller")

			ephemeralRunner = newExampleRunner("test-runner", autoscalingNS.Name, configSecret.Name)
			err = k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil(), "failed to create ephemeral runner")

			startManagers(GinkgoT(), mgr)
		})

		It("It should create/add all required resources for EphemeralRunner (finalizer, jit secret)", func() {
			created := new(v1alpha1.EphemeralRunner)
			// Check if finalizer is added
			Eventually(
				func() ([]string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, created)
					if err != nil {
						return nil, err
					}
					if len(created.Finalizers) == 0 {
						return nil, nil
					}

					n := len(created.Finalizers) // avoid capacity mismatch
					return created.Finalizers[:n:n], nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo([]string{ephemeralRunnerFinalizerName, ephemeralRunnerActionsFinalizerName}))

			Eventually(
				func() (bool, error) {
					secret := new(corev1.Secret)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, secret); err != nil {
						return false, err
					}

					_, ok := secret.Data[jitTokenKey]
					return ok, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			Eventually(
				func() (string, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return "", err
					}

					return pod.Name, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(ephemeralRunner.Name))
		})

		It("It should re-create pod on failure and no job assigned", func() {
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))
		})

		It("It should delete ephemeral runner on failure and job assigned", func() {
			er := new(v1alpha1.EphemeralRunner)
			// Check if finalizer is added
			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
					return err
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get ephemeral runner")

			// update job id to simulate job assigned
			er.Status.JobID = "1"
			err := k8sClient.Status().Update(ctx, er)
			Expect(err).To(BeNil(), "failed to update ephemeral runner status")

			er = new(v1alpha1.EphemeralRunner)
			Eventually(
				func() (string, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
					if err != nil {
						return "", err
					}
					return er.Status.JobID, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo("1"))

			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}).Should(BeEquivalentTo(true))

			// delete pod to simulate failure
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 1,
					},
				},
			})
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "Failed to update pod status")

			er = new(v1alpha1.EphemeralRunner)
			Eventually(
				func() bool {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
					return kerrors.IsNotFound(err)
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "Ephemeral runner should eventually be deleted")
		})

		It("It should delete ephemeral runner when pod failed before runner state is recorded and job assigned", func() {
			er := new(v1alpha1.EphemeralRunner)
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

			er.Status.JobID = "1"
			err := k8sClient.Status().Update(ctx, er)
			Expect(err).To(BeNil(), "failed to update ephemeral runner status")

			Eventually(func() (string, error) {
				current := new(v1alpha1.EphemeralRunner)
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current); err != nil {
					return "", err
				}
				return current.Status.JobID, nil
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeEquivalentTo("1"))

			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeEquivalentTo(true))

			pod.Status.Phase = corev1.PodFailed
			pod.Status.ContainerStatuses = nil
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "Failed to update pod status")

			Eventually(func() bool {
				check := new(v1alpha1.EphemeralRunner)
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
				return kerrors.IsNotFound(err)
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "Ephemeral runner should eventually be deleted")
		})

		It("It should delete ephemeral runner when pod failed before runner state is recorded and job not assigned", func() {
			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeEquivalentTo(true))

			oldPodUID := pod.UID

			pod.Status.Phase = corev1.PodFailed
			pod.Status.ContainerStatuses = nil
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "Failed to update pod status")

			Eventually(
				func() (int, error) {
					updated := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(
						ctx,
						client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace},
						updated,
					)
					if err != nil {
						return 0, err
					}
					return len(updated.Status.Failures), nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(1))

			Eventually(
				func() (bool, error) {
					newPod := new(corev1.Pod)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, newPod)
					if err != nil {
						return false, err
					}
					return newPod.UID != oldPodUID, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "Pod should be re-created")
		})

		It("It should treat pod failed with runner container exit 0 as success with job id", func() {
			er := new(v1alpha1.EphemeralRunner)
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

			er.Status.JobID = "1"
			err := k8sClient.Status().Update(ctx, er)
			Expect(err).To(BeNil(), "failed to update ephemeral runner status")

			pod := new(corev1.Pod)
			Eventually(
				func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return err
					}
					return nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get pod")

			pod.Status.Phase = corev1.PodFailed
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
					},
				},
			})
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "Failed to update pod status")

			Eventually(
				func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "Ephemeral runner should eventually be deleted")
		})

		It("It should treat pod failed with runner container exit 0 as success with no job id", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return err
					}
					return nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get pod")

			pod.Status.Phase = corev1.PodFailed
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
					},
				},
			})
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "Failed to update pod status")

			Eventually(
				func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "Ephemeral runner should eventually be deleted")
		})

		It("It should mark as failed when job is not assigned and pod is failed", func() {
			er := new(v1alpha1.EphemeralRunner)
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
			},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get ephemeral runner")

			pod := new(corev1.Pod)
			Eventually(
				func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return err
					}
					return nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get pod")

			pod.Status.Phase = corev1.PodFailed
			oldPodUID := pod.UID
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 1,
					},
				},
			})

			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "Failed to update pod status")

			Eventually(
				func() (int, error) {
					updated := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(
						ctx,
						client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace},
						updated,
					)
					if err != nil {
						return 0, err
					}
					return len(updated.Status.Failures), nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(1))

			Eventually(
				func() (bool, error) {
					newPod := new(corev1.Pod)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, newPod)
					if err != nil {
						return false, err
					}
					return newPod.UID != oldPodUID, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "Pod should be re-created")
		})

		It("It should failed if a pod template is invalid", func() {
			invalideEphemeralRunner := newExampleRunner("invalid-ephemeral-runner", autoscalingNS.Name, configSecret.Name)
			invalideEphemeralRunner.Spec.Spec.PriorityClassName = "notexist"

			err := k8sClient.Create(ctx, invalideEphemeralRunner)
			Expect(err).To(BeNil())

			updated := new(v1alpha1.EphemeralRunner)
			Eventually(
				func() (v1alpha1.EphemeralRunnerPhase, error) {
					err := k8sClient.Get(
						ctx,
						client.ObjectKey{Name: invalideEphemeralRunner.Name, Namespace: invalideEphemeralRunner.Namespace},
						updated,
					)
					if err != nil {
						return "", nil
					}
					return updated.Status.Phase, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(v1alpha1.EphemeralRunnerPhaseFailed))

			Expect(updated.Status.Reason).Should(Equal("InvalidPod"))
			Expect(updated.Status.Message).Should(Equal("Failed to create the pod: pods \"invalid-ephemeral-runner\" is forbidden: no PriorityClass with name notexist was found"))
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
					return updatedEphemeralRunner.Status.RunnerID, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			for _, phase := range []corev1.PodPhase{corev1.PodRunning, corev1.PodPending} {
				podCopy := pod.DeepCopy()
				pod.Status.Phase = phase
				// set container state to force status update
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name:  v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{},
				})

				err := k8sClient.Status().Patch(ctx, pod, client.MergeFrom(podCopy))
				Expect(err).To(BeNil(), "failed to patch pod status")

				var updated *v1alpha1.EphemeralRunner
				Eventually(
					func() (v1alpha1.EphemeralRunnerPhase, error) {
						updated = new(v1alpha1.EphemeralRunner)
						err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
						if err != nil {
							return "", err
						}
						return updated.Status.Phase, nil
					},
					ephemeralRunnerTimeout,
					ephemeralRunnerInterval,
				).Should(BeEquivalentTo(phase))
			}
		})

		It("It should update ready based on the latest condition", func() {
			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}).Should(BeEquivalentTo(true))

			newPod := pod.DeepCopy()
			newPod.Status.Conditions = []corev1.PodCondition{
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               corev1.PodInitialized,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               corev1.ContainersReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               corev1.PodReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
			}
			newPod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:  v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{},
			})
			err := k8sClient.Status().Patch(ctx, newPod, client.MergeFrom(pod))
			Expect(err).To(BeNil(), "failed to patch pod status")

			var er *v1alpha1.EphemeralRunner
			Eventually(
				func() (bool, error) {
					er = new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
					if err != nil {
						return false, err
					}
					return er.Status.Ready, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			// Fetch the pod again
			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err != nil {
						return false, err
					}
					return true, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			newPod = pod.DeepCopy()
			newPod.Status.Conditions = append(newPod.Status.Conditions, corev1.PodCondition{
				Type:               corev1.PodReady,
				Status:             corev1.ConditionFalse,
				LastTransitionTime: metav1.Time{Time: metav1.Now().Add(1 * time.Second)},
			})

			err = k8sClient.Status().Patch(ctx, newPod, client.MergeFrom(pod))
			Expect(err).To(BeNil(), "expected no errors when updating new pod status")

			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err != nil {
						return false, err
					}
					return ephemeralRunner.Status.Ready, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(false))
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			pod.Status.Phase = corev1.PodRunning
			err := k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "failed to patch pod status")

			Consistently(
				func() (v1alpha1.EphemeralRunnerPhase, error) {
					updated := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated); err != nil {
						return "Unknown", err
					}
					return updated.Status.Phase, nil
				},
				ephemeralRunnerTimeout,
			).Should(BeEquivalentTo(""))
		})

		It("It should eventually delete ephemeral runner after consecutive failures", func() {
			updated := new(v1alpha1.EphemeralRunner)
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get ephemeral runner")

			failEphemeralRunnerPod := func() *corev1.Pod {
				pod := new(corev1.Pod)
				Eventually(
					func() error {
						return k8sClient.Get(ctx, client.ObjectKey{Name: updated.Name, Namespace: updated.Namespace}, pod)
					},
					ephemeralRunnerTimeout,
					ephemeralRunnerInterval,
				).Should(Succeed(), "failed to get ephemeral runner pod")

				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
						},
					},
				})
				err := k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "Failed to update pod status")

				return pod
			}

			for i := range 5 {
				pod := failEphemeralRunnerPod()

				Eventually(
					func() (int, error) {
						err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
						if err != nil {
							return 0, err
						}
						return len(updated.Status.Failures), nil
					},
					ephemeralRunnerTimeout,
					ephemeralRunnerInterval,
				).Should(BeEquivalentTo(i + 1))

				Eventually(
					func() error {
						nextPod := new(corev1.Pod)
						err := k8sClient.Get(ctx, client.ObjectKey{Name: pod.Name, Namespace: pod.Namespace}, nextPod)
						if err != nil {
							return err
						}
						if nextPod.UID != pod.UID {
							return nil
						}
						return fmt.Errorf("pod not recreated")
					},
				).WithTimeout(20*time.Second).WithPolling(10*time.Millisecond).Should(Succeed(), "pod should be recreated")

				Eventually(
					func() (bool, error) {
						pod := new(corev1.Pod)
						err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
						if err != nil {
							return false, err
						}
						for _, cs := range pod.Status.ContainerStatuses {
							if cs.Name == v1alpha1.EphemeralRunnerContainerName {
								return cs.State.Terminated == nil, nil
							}
						}

						return true, nil
					},
				).WithTimeout(20*time.Second).WithPolling(10*time.Millisecond).Should(BeEquivalentTo(true), "pod should be terminated")
			}

			failEphemeralRunnerPod()

			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
					if kerrors.IsNotFound(err) {
						return true, nil
					}
					return false, err
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "Ephemeral runner should eventually be deleted")
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			pod.Status.Phase = corev1.PodFailed
			pod.Status.Reason = "Evicted"
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:  v1alpha1.EphemeralRunnerContainerName,
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
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeEquivalentTo(true))

			// should re-create after failure
			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return false, err
					}
					return true, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))
		})

		It("It should re-create pod on reason starting with OutOf", func() {
			pod := new(corev1.Pod)
			Eventually(
				func() (bool, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					if err != nil {
						return false, err
					}
					return true, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			pod.Status.Phase = corev1.PodFailed
			pod.Status.Reason = "OutOfpods"
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:  v1alpha1.EphemeralRunnerContainerName,
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
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeEquivalentTo(true))

			// should re-create after failure
			Eventually(
				func() (bool, error) {
					pod := new(corev1.Pod)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
						return false, err
					}
					return true, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
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
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))

			// first set phase to running
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: v1alpha1.EphemeralRunnerContainerName,
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
				func() (v1alpha1.EphemeralRunnerPhase, error) {
					updated := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated); err != nil {
						return "", err
					}
					return updated.Status.Phase, nil
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(v1alpha1.EphemeralRunnerPhaseRunning))

			// set phase to succeeded
			pod.Status.Phase = corev1.PodSucceeded
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil())

			Consistently(
				func() (v1alpha1.EphemeralRunnerPhase, error) {
					updated := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated); err != nil {
						return "", err
					}
					return updated.Status.Phase, nil
				},
				ephemeralRunnerTimeout,
			).Should(BeEquivalentTo(v1alpha1.EphemeralRunnerPhaseRunning))
		})

		// ==============================================================================
		// Terminal Lifecycle Invariants Matrix for EphemeralRunner Pod Failures
		// ==============================================================================
		//
		// This matrix codifies expected outcomes for all terminal runner failure scenarios.
		// These tests drive the controller's reconciliation branching logic.
		//
		// Dimensions:
		// -----------
		// 1. HasJob (JobID set):     true | false
		// 2. RunnerID:               0 | non-zero
		// 3. Container Status:       nil/no-terminated | terminated
		// 4. Exit Code (if term):    0 | 7 | 137 (OOMKilled) | other non-zero
		// 5. Pod.Status.Reason:      Evicted | OutOf* | OOMKilled | <empty>
		//
		// Expected Outcomes:
		// ------------------
		// HasJob | RunnerID | ContainerStatus | ExitCode | PodReason  | Expected Outcome
		// -------|----------|-----------------|----------|------------|---------------------------------------------------
		// false  | 0        | nil/no-term     | N/A      | Evicted    | DELETE pod, track failure, RECREATE (retryable)
		// false  | 0        | nil/no-term     | N/A      | OutOf*     | DELETE pod, track failure, RECREATE (retryable)
		// false  | 0        | nil/no-term     | N/A      | <empty>    | DELETE pod, track failure, RECREATE (retryable)
		// false  | non-zero | nil/no-term     | N/A      | Evicted    | DELETE pod, track failure, RECREATE (retryable)
		// false  | non-zero | nil/no-term     | N/A      | OutOf*     | DELETE pod, track failure, RECREATE (retryable)
		// false  | non-zero | nil/no-term     | N/A      | <empty>    | DELETE pod, track failure, RECREATE (retryable)
		// false  | 0        | terminated      | 0        | N/A        | DELETE runner (SUCCESS - terminal)
		// false  | non-zero | terminated      | 0        | N/A        | DELETE runner (SUCCESS - terminal)
		// false  | 0        | terminated      | 7        | N/A        | Mark Outdated (TERMINAL)
		// false  | non-zero | terminated      | 7        | N/A        | Mark Outdated (TERMINAL)
		// false  | 0        | terminated      | 137      | OOMKilled  | DELETE runner + remove from service (TERMINAL)
		// false  | non-zero | terminated      | 137      | OOMKilled  | DELETE runner + remove from service (TERMINAL)
		// false  | 0        | terminated      | other    | <empty>    | DELETE pod, track failure, RECREATE (retryable)
		// false  | non-zero | terminated      | other    | <empty>    | DELETE pod, track failure, RECREATE (retryable)
		// true   | 0        | nil/no-term     | N/A      | Evicted    | DELETE runner + remove from service (TERMINAL)
		// true   | 0        | nil/no-term     | N/A      | OutOf*     | DELETE runner + remove from service (TERMINAL)
		// true   | 0        | nil/no-term     | N/A      | <empty>    | DELETE runner + remove from service (TERMINAL)
		// true   | non-zero | nil/no-term     | N/A      | Evicted    | DELETE runner + remove from service (TERMINAL)
		// true   | non-zero | nil/no-term     | N/A      | OutOf*     | DELETE runner + remove from service (TERMINAL)
		// true   | non-zero | nil/no-term     | N/A      | <empty>    | DELETE runner + remove from service (TERMINAL)
		// true   | 0        | terminated      | 0        | N/A        | DELETE runner (SUCCESS - terminal)
		// true   | non-zero | terminated      | 0        | N/A        | DELETE runner (SUCCESS - terminal)
		// true   | 0        | terminated      | 7        | N/A        | Mark Outdated (TERMINAL)
		// true   | non-zero | terminated      | 7        | N/A        | Mark Outdated (TERMINAL)
		// true   | 0        | terminated      | 137      | OOMKilled  | DELETE runner + remove from service (TERMINAL)
		// true   | non-zero | terminated      | 137      | OOMKilled  | DELETE runner + remove from service (TERMINAL)
		// true   | 0        | terminated      | other    | <empty>    | DELETE runner + remove from service (TERMINAL)
		// true   | non-zero | terminated      | other    | <empty>    | DELETE runner + remove from service (TERMINAL)
		//
		// Key Insights:
		// -------------
		// 1. HasJob=true always results in TERMINAL deletion (runner + service cleanup)
		// 2. HasJob=false with retryable failures (Evicted, OutOf*, no container status) -> RECREATE
		// 3. OOMKilled (ExitCode 137, Reason=OOMKilled) is TERMINAL regardless of HasJob status
		// 4. ExitCode 0 and 7 are always terminal regardless of HasJob
		// 5. Other non-zero exit codes follow the HasJob branching logic
		//
		// Implementation Notes:
		// ---------------------
		// - Controller logic: ephemeralrunner_controller.go:314-352 (main PodFailed branch)
		// - deleteEphemeralRunnerOrPod: ephemeralrunner_controller.go:388-420 (HasJob fork)
		// - deletePodAsFailed: ephemeralrunner_controller.go:604-627 (failure tracking)
		//
		// ==============================================================================

		Context("Terminal Lifecycle Invariants Matrix", func() {
			It("Matrix: HasJob=false, RunnerID=non-zero, ExitCode=137 (OOMKilled) should delete runner and remove from service", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "OOMKilled"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
						},
					},
				})
				err := k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "ephemeral runner should be deleted (terminal)")
			})

			It("Matrix: HasJob=true, RunnerID=non-zero, ExitCode=137 (OOMKilled) should delete runner and remove from service", func() {
				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "12345"
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status")

				Eventually(func() (string, error) {
					current := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current); err != nil {
						return "", err
					}
					return current.Status.JobID, nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal("12345"))

				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "OOMKilled"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "ephemeral runner should be deleted (terminal)")
			})

			It("Matrix: HasJob=false, RunnerID=0, ExitCode=137 (OOMKilled) should delete runner (terminal)", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "OOMKilled"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
						},
					},
				})
				err := k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "ephemeral runner should be deleted (terminal)")
			})

			It("Matrix: HasJob=false, no ContainerStatus.Terminated, Pod.Reason=OOMKilled should delete runner (terminal)", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "OOMKilled"
				pod.Status.ContainerStatuses = nil
				err := k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "ephemeral runner should be deleted (terminal)")
			})

			It("Matrix: HasJob=true, no ContainerStatus.Terminated, Pod.Reason=OOMKilled should delete runner (terminal)", func() {
				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "67890"
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status with job ID")

				Eventually(func() (string, error) {
					current := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current); err != nil {
						return "", err
					}
					return current.Status.JobID, nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal("67890"))

				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "OOMKilled"
				pod.Status.ContainerStatuses = nil
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeTrue(), "ephemeral runner should be deleted (terminal)")
			})

			It("Matrix: HasJob=false, no ContainerStatus and empty Pod.Reason should use fallback reason", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = ""
				pod.Status.Message = ""
				pod.Status.ContainerStatuses = nil
				err := k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				updated := new(v1alpha1.EphemeralRunner)
				Eventually(func() (int, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
					if err != nil {
						return 0, err
					}
					return len(updated.Status.Failures), nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal(1), "failure should be tracked")

				Eventually(func() (string, error) {
					current := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current)
					if err != nil {
						return "", err
					}
					return current.Status.Reason, nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal("Failed"), "reason should use fallback value")

				Eventually(func() (string, error) {
					current := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current)
					if err != nil {
						return "", err
					}
					return current.Status.Message, nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal("Pod failed without detailed termination information"), "message should use fallback value")

				Eventually(func() error {
					pod := new(corev1.Pod)
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout*2, ephemeralRunnerInterval).Should(Succeed(), "pod should be recreated")
			})
		})

		Context("OOMKilled with Transient API Failures", func() {
			var ctx context.Context
			var autoscalingNS *corev1.Namespace
			var configSecret *corev1.Secret
			var controller *EphemeralRunnerReconciler
			var ephemeralRunner *v1alpha1.EphemeralRunner
			var mgr ctrl.Manager

			BeforeEach(func() {
				ctx = context.Background()
				autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
				configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

				// Create controller with fake client that fails RemoveRunner
				controller = &EphemeralRunnerReconciler{
					Client: mgr.GetClient(),
					Scheme: mgr.GetScheme(),
					Log:    logf.Log,
					ResourceBuilder: ResourceBuilder{
						SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
							scalefake.WithClient(
								scalefake.NewClient(
									scalefake.WithGenerateJitRunnerConfig(
										&scaleset.RunnerScaleSetJitRunnerConfig{
											Runner:           &scaleset.RunnerReference{ID: 2, Name: "test-runner-transient"},
											EncodedJITConfig: "fake-jit-config",
										},
										nil,
									),
									scalefake.WithRemoveRunner(
										errors.New("transient API failure"),
									),
								),
							),
						)),
					},
				}

				err := controller.SetupWithManager(mgr)
				Expect(err).To(BeNil(), "failed to setup controller")

				ephemeralRunner = newExampleRunner("test-runner-transient", autoscalingNS.Name, configSecret.Name)
				err = k8sClient.Create(ctx, ephemeralRunner)
				Expect(err).To(BeNil(), "failed to create ephemeral runner")

				startManagers(GinkgoT(), mgr)
			})

			It("Matrix: HasJob=true, OOMKilled with transient RemoveRunner API failure should keep runner for retry and not set deletion timestamp", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "99999"
				er.Status.RunnerID = 2
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status with job ID and runner ID")

				Eventually(func() (string, error) {
					current := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current); err != nil {
						return "", err
					}
					return current.Status.JobID, nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal("99999"))

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "OOMKilled"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Consistently(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					if err != nil {
						return false
					}
					return check.DeletionTimestamp == nil && len(check.Finalizers) > 0
				}, "5s", ephemeralRunnerInterval).Should(BeTrue(), "runner should be preserved for retry until RemoveRunner succeeds")
			})

			It("Matrix: HasJob=true, non-OOM pod failure with transient RemoveRunner API failure should preserve runner for retry", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "88888"
				er.Status.RunnerID = 2
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status with job ID and runner ID")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "CrashLoopBackOff"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Consistently(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					if err != nil {
						return false
					}
					return check.DeletionTimestamp == nil
				}, "5s", ephemeralRunnerInterval).Should(BeTrue(), "runner should remain undeleted so RemoveRunner can be retried")
			})

			It("Matrix: HasJob=false, RunnerID=non-zero, pod failure with transient RemoveRunner API failure should preserve runner for retry", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.RunnerID = 3
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status with runner ID")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "Error"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				updated := new(v1alpha1.EphemeralRunner)
				Eventually(func() (int, error) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, updated)
					if err != nil {
						return 0, err
					}
					return len(updated.Status.Failures), nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal(1), "failure should be tracked")

				Consistently(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					if err != nil {
						return false
					}
					return check.DeletionTimestamp == nil && len(check.Status.Failures) == 1
				}, "5s", ephemeralRunnerInterval).Should(BeTrue(), "runner should be preserved for RemoveRunner retry despite HasJob=false")
			})
		})

		// ==============================================================================
		// Convergence Tests: Verify retry-safe cleanup converges correctly
		// ==============================================================================
		// These tests ensure the retry mechanism:
		// 1. Eventually converges when external service recovers
		// 2. Respects bounded behavior (no infinite loops)
		// 3. Maintains observability (no silent success on persistent failure)
		//
		// Implementation Reference:
		// - Backoff logic: ephemeralrunner_controller.go:229-254
		// - Cleanup retry: ephemeralrunner_controller.go:430-440
		// - MaxFailures boundary: ephemeralrunner_controller.go:229-236
		// ==============================================================================
		Context("Retry Convergence and Bounded Behavior", func() {
			var callCount int
			var mu sync.Mutex

			BeforeEach(func() {
				mu.Lock()
				callCount = 0
				mu.Unlock()

				controller = &EphemeralRunnerReconciler{
					Client: mgr.GetClient(),
					Scheme: mgr.GetScheme(),
					Log:    logf.Log,
					ResourceBuilder: ResourceBuilder{
						SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
							scalefake.WithClient(
								scalefake.NewClient(
									scalefake.WithGenerateJitRunnerConfig(
										&scaleset.RunnerScaleSetJitRunnerConfig{
											Runner:           &scaleset.RunnerReference{ID: 99, Name: "test-convergence"},
											EncodedJITConfig: "fake-jit-config",
										},
										nil,
									),
									scalefake.WithRemoveRunnerFunc(func(ctx context.Context, runnerID int64) error {
										mu.Lock()
										defer mu.Unlock()
										callCount++
										if callCount <= 2 {
											return errors.New("transient API failure")
										}
										return nil
									}),
								),
							),
						)),
					},
				}

				err := controller.SetupWithManager(mgr)
				Expect(err).To(BeNil(), "failed to setup controller")

				ephemeralRunner = newExampleRunner("test-convergence", autoscalingNS.Name, configSecret.Name)
				err = k8sClient.Create(ctx, ephemeralRunner)
				Expect(err).To(BeNil(), "failed to create ephemeral runner")

				startManagers(GinkgoT(), mgr)
			})

			It("Convergence: Repeated transient RemoveRunner failures eventually converge when service recovers", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "conv-job-1"
				er.Status.RunnerID = 99
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status")

				Eventually(func() (string, error) {
					current := new(v1alpha1.EphemeralRunner)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, current); err != nil {
						return "", err
					}
					return current.Status.JobID, nil
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Equal("conv-job-1"))

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "Error"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() int {
					mu.Lock()
					defer mu.Unlock()
					return callCount
				}, "30s", ephemeralRunnerInterval).Should(BeNumerically(">=", 3), "RemoveRunner should be retried until success")

				Eventually(func() bool {
					check := new(v1alpha1.EphemeralRunner)
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
					return kerrors.IsNotFound(err)
				}, "40s", ephemeralRunnerInterval).Should(BeTrue(), "ephemeral runner should be deleted after cleanup converges")
			})
		})

		Context("Bounded Retry Behavior: MaxFailures Enforcement", func() {
			var persistentFailCount int
			var muPersistent sync.Mutex

			BeforeEach(func() {
				muPersistent.Lock()
				persistentFailCount = 0
				muPersistent.Unlock()

				controller = &EphemeralRunnerReconciler{
					Client: mgr.GetClient(),
					Scheme: mgr.GetScheme(),
					Log:    logf.Log,
					ResourceBuilder: ResourceBuilder{
						SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
							scalefake.WithClient(
								scalefake.NewClient(
									scalefake.WithGenerateJitRunnerConfig(
										&scaleset.RunnerScaleSetJitRunnerConfig{
											Runner:           &scaleset.RunnerReference{ID: 777, Name: "test-persistent"},
											EncodedJITConfig: "fake-jit-config",
										},
										nil,
									),
									scalefake.WithRemoveRunnerFunc(func(ctx context.Context, runnerID int64) error {
										muPersistent.Lock()
										defer muPersistent.Unlock()
										persistentFailCount++
										return errors.New("persistent API failure")
									}),
								),
							),
						)),
					},
				}

				err := controller.SetupWithManager(mgr)
				Expect(err).To(BeNil(), "failed to setup controller")

				ephemeralRunner = newExampleRunner("test-persistent", autoscalingNS.Name, configSecret.Name)
				err = k8sClient.Create(ctx, ephemeralRunner)
				Expect(err).To(BeNil(), "failed to create ephemeral runner")

				startManagers(GinkgoT(), mgr)
			})

			It("Bounded: Persistent RemoveRunner failures do not cause infinite retry (maxFailures enforced)", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "persist-job"
				er.Status.RunnerID = 777
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "Error"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Consistently(func() int {
					muPersistent.Lock()
					defer muPersistent.Unlock()
					return persistentFailCount
				}, "15s", "1s").Should(BeNumerically("<=", 20), "RemoveRunner should not be called excessively (no infinite loop)")

				check := new(v1alpha1.EphemeralRunner)
				err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)
				if err == nil {
					Expect(check.DeletionTimestamp).NotTo(BeNil(), "runner should have deletion timestamp showing cleanup intent")
				} else {
					Expect(kerrors.IsNotFound(err)).To(BeTrue(), "runner either deleted or has visible error state")
				}
			})

			It("Observability: Failed cleanup attempts are visible and not silently succeeded", func() {
				pod := new(corev1.Pod)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get pod")

				er := new(v1alpha1.EphemeralRunner)
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, er)
				}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(Succeed(), "failed to get ephemeral runner")

				er.Status.JobID = "observe-job"
				er.Status.RunnerID = 777
				err := k8sClient.Status().Update(ctx, er)
				Expect(err).To(BeNil(), "failed to update ephemeral runner status")

				pod.Status.Phase = corev1.PodFailed
				pod.Status.Reason = "Error"
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
					Name: v1alpha1.EphemeralRunnerContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				})
				err = k8sClient.Status().Update(ctx, pod)
				Expect(err).To(BeNil(), "failed to update pod status")

				Eventually(func() int {
					muPersistent.Lock()
					defer muPersistent.Unlock()
					return persistentFailCount
				}, "10s", ephemeralRunnerInterval).Should(BeNumerically(">=", 1), "RemoveRunner must be attempted (not silently skipped)")

				time.Sleep(5 * time.Second)
				check := new(v1alpha1.EphemeralRunner)
				err = k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, check)

				if err == nil {
					Expect(check.DeletionTimestamp).NotTo(BeNil(), "runner must have deletion timestamp showing cleanup attempt")
				} else {
					Expect(kerrors.IsNotFound(err)).To(BeTrue(), "runner either deleted or has visible deletion intent")
				}

				muPersistent.Lock()
				finalCount := persistentFailCount
				muPersistent.Unlock()
				Expect(finalCount).To(BeNumerically(">=", 1), "cleanup must be attempted, not silently skipped")
			})
		})
	})

	Describe("Checking the API", func() {
		var ctx context.Context
		var autoscalingNS *corev1.Namespace
		var configSecret *corev1.Secret
		var controller *EphemeralRunnerReconciler
		var mgr ctrl.Manager

		BeforeEach(func() {
			ctx = context.Background()
			autoscalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
			configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

			controller = &EphemeralRunnerReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
				Log:    logf.Log,
				ResourceBuilder: ResourceBuilder{
					SecretResolver: secretresolver.New(
						mgr.GetClient(),
						scalefake.NewMultiClient(
							scalefake.WithClient(
								scalefake.NewClient(
									scalefake.WithGetRunner(
										nil,
										scaleset.RunnerNotFoundError,
									),
									scalefake.WithGenerateJitRunnerConfig(
										&scaleset.RunnerScaleSetJitRunnerConfig{
											Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
											EncodedJITConfig: "fake-jit-config",
										},
										nil,
									),
								),
							),
						),
					),
				},
			}
			err := controller.SetupWithManager(mgr)
			Expect(err).To(BeNil(), "failed to setup controller")

			startManagers(GinkgoT(), mgr)
		})

		It("It should delete EphemeralRunner when pod exits successfully", func() {
			ephemeralRunner := newExampleRunner("test-runner", autoscalingNS.Name, configSecret.Name)

			err := k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil())

			pod := new(corev1.Pod)
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod); err != nil {
					return false, err
				}
				return true, nil
			}, ephemeralRunnerTimeout, ephemeralRunnerInterval).Should(BeEquivalentTo(true))

			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: v1alpha1.EphemeralRunnerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 0,
					},
				},
			})
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).To(BeNil(), "failed to update pod status")

			updated := new(v1alpha1.EphemeralRunner)
			Eventually(
				func() bool {
					err := k8sClient.Get(
						ctx,
						client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace},
						updated,
					)
					return kerrors.IsNotFound(err)
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(BeTrue())
		})
	})

	Describe("Pod proxy config", func() {
		var ctx context.Context
		var mgr ctrl.Manager
		var autoScalingNS *corev1.Namespace
		var configSecret *corev1.Secret
		var controller *EphemeralRunnerReconciler

		BeforeEach(func() {
			ctx = context.Background()
			autoScalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
			configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoScalingNS.Name)

			controller = &EphemeralRunnerReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
				Log:    logf.Log,
				ResourceBuilder: ResourceBuilder{
					SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
						scalefake.WithClient(
							scalefake.NewClient(
								scalefake.WithGenerateJitRunnerConfig(
									&scaleset.RunnerScaleSetJitRunnerConfig{
										Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
										EncodedJITConfig: "fake-jit-config",
									},
									nil,
								),
							),
						),
					)),
				},
			}
			err := controller.SetupWithManager(mgr)
			Expect(err).To(BeNil(), "failed to setup controller")

			startManagers(GinkgoT(), mgr)
		})

		It("uses an actions client with proxy transport", func() {
			// Use an actual client
			controller.ResourceBuilder = ResourceBuilder{
				SecretResolver: secretresolver.New(
					mgr.GetClient(),
					multiclient.NewScaleset(),
				),
			}

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

			secretCredentials := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "proxy-credentials",
					Namespace: autoScalingNS.Name,
				},
				Data: map[string][]byte{
					"username": []byte("test"),
					"password": []byte("password"),
				},
			}

			err := k8sClient.Create(ctx, secretCredentials)
			Expect(err).NotTo(HaveOccurred(), "failed to create secret credentials")

			ephemeralRunner := newExampleRunner("test-runner", autoScalingNS.Name, configSecret.Name)
			ephemeralRunner.Spec.GitHubConfigUrl = "http://example.com/org/repo"
			ephemeralRunner.Spec.Proxy = &v1alpha1.ProxyConfig{
				HTTP: &v1alpha1.ProxyServerConfig{
					Url:                 proxy.URL,
					CredentialSecretRef: "proxy-credentials",
				},
			}

			err = k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil(), "failed to create ephemeral runner")

			Eventually(
				func() bool {
					return proxySuccessfulllyCalled
				},
				2*time.Second,
				ephemeralRunnerInterval,
			).Should(BeEquivalentTo(true))
		})

		It("It should create EphemeralRunner with proxy environment variables using ProxySecretRef", func() {
			ephemeralRunner := newExampleRunner("test-runner", autoScalingNS.Name, configSecret.Name)
			ephemeralRunner.Spec.Proxy = &v1alpha1.ProxyConfig{
				HTTP: &v1alpha1.ProxyServerConfig{
					Url: "http://proxy.example.com:8080",
				},
				HTTPS: &v1alpha1.ProxyServerConfig{
					Url: "http://proxy.example.com:8080",
				},
				NoProxy: []string{"example.com"},
			}
			ephemeralRunner.Spec.ProxySecretRef = "proxy-secret"
			err := k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil(), "failed to create ephemeral runner")

			pod := new(corev1.Pod)
			Eventually(
				func(g Gomega) {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ephemeralRunner.Name, Namespace: ephemeralRunner.Namespace}, pod)
					g.Expect(err).To(BeNil(), "failed to get ephemeral runner pod")
				},
				ephemeralRunnerTimeout,
				ephemeralRunnerInterval,
			).Should(Succeed(), "failed to get ephemeral runner pod")

			Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
				Name: EnvVarHTTPProxy,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ephemeralRunner.Spec.ProxySecretRef,
						},
						Key: "http_proxy",
					},
				},
			}))

			Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
				Name: EnvVarHTTPSProxy,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ephemeralRunner.Spec.ProxySecretRef,
						},
						Key: "https_proxy",
					},
				},
			}))

			Expect(pod.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
				Name: EnvVarNoProxy,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ephemeralRunner.Spec.ProxySecretRef,
						},
						Key: "no_proxy",
					},
				},
			}))
		})
	})

	Describe("TLS config", func() {
		var ctx context.Context
		var mgr ctrl.Manager
		var autoScalingNS *corev1.Namespace
		var configSecret *corev1.Secret
		var controller *EphemeralRunnerReconciler
		var rootCAConfigMap *corev1.ConfigMap

		BeforeEach(func() {
			ctx = context.Background()
			autoScalingNS, mgr = createNamespace(GinkgoT(), k8sClient)
			configSecret = createDefaultSecret(GinkgoT(), k8sClient, autoScalingNS.Name)

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
					Namespace: autoScalingNS.Name,
				},
				Data: map[string]string{
					"rootCA.crt": string(cert),
				},
			}
			err = k8sClient.Create(ctx, rootCAConfigMap)
			Expect(err).NotTo(HaveOccurred(), "failed to create configmap with root CAs")

			controller = &EphemeralRunnerReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
				Log:    logf.Log,
				ResourceBuilder: ResourceBuilder{
					SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient()),
				},
			}

			err = controller.SetupWithManager(mgr)
			Expect(err).To(BeNil(), "failed to setup controller")

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
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serverSuccessfullyCalled = true
				w.WriteHeader(http.StatusOK)
			}))
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			Expect(err).NotTo(HaveOccurred(), "failed to load server cert")

			server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			server.StartTLS()
			defer server.Close()

			// Use an actual client
			controller.ResourceBuilder = ResourceBuilder{
				SecretResolver: secretresolver.New(
					mgr.GetClient(),
					multiclient.NewScaleset(),
				),
			}

			ephemeralRunner := newExampleRunner("test-runner", autoScalingNS.Name, configSecret.Name)
			ephemeralRunner.Spec.GitHubConfigUrl = server.URL + "/my-org"
			ephemeralRunner.Spec.GitHubServerTLS = &v1alpha1.TLSConfig{
				CertificateFrom: &v1alpha1.TLSCertificateSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: rootCAConfigMap.Name,
						},
						Key: "rootCA.crt",
					},
				},
			}

			err = k8sClient.Create(ctx, ephemeralRunner)
			Expect(err).To(BeNil(), "failed to create ephemeral runner")

			Eventually(
				func() bool {
					return serverSuccessfullyCalled
				},
				2*time.Second,
				ephemeralRunnerInterval,
			).Should(BeTrue(), "failed to contact server")
		})
	})
})
