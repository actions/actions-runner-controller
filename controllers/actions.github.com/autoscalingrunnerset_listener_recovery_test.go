package actionsgithubcom

import (
	"context"
	"strconv"
	"sync/atomic"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	ghalistenerconfig "github.com/actions/actions-runner-controller/cmd/ghalistener/config"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Both reconcilers run here, so the hand-off is exercised as it happens in a
// cluster: the listener controller deletes the listener, its finalizer holds
// it while the child resources go, and only then does the AutoscalingRunnerSet
// controller find it missing and re-register the scale set.
var _ = Describe("AutoscalingListener whose runner scale set is gone", func() {
	const originalRunnerScaleSetID = 77
	const replacementRunnerScaleSetID = 78

	It("is rebuilt against a re-registered runner scale set", func() {
		ctx := context.Background()
		autoscalingNS, mgr := createNamespace(GinkgoT(), k8sClient)
		configSecret := createDefaultSecret(GinkgoT(), k8sClient, autoscalingNS.Name)

		var scaleSetDeleted atomic.Bool
		builder := ResourceBuilder{
			ResourceCache: newTestResourceCache(),
			SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
				scalefake.WithClient(
					scalefake.NewClient(
						scalefake.WithGetRunnerGroupByName(&scaleset.RunnerGroup{ID: 1, Name: "testgroup"}, nil),
						scalefake.WithGetRunnerScaleSetByIDFunc(func(_ context.Context, runnerScaleSetID int) (*scaleset.RunnerScaleSet, error) {
							if runnerScaleSetID == originalRunnerScaleSetID && scaleSetDeleted.Load() {
								return nil, scaleset.NotFoundError
							}
							return &scaleset.RunnerScaleSet{ID: runnerScaleSetID, Name: "test-asrs", RunnerGroupID: 1, RunnerGroupName: "testgroup"}, nil
						}),
						scalefake.WithGetRunnerScaleSet(nil, nil),
						scalefake.WithCreateRunnerScaleSet(&scaleset.RunnerScaleSet{ID: replacementRunnerScaleSetID, Name: "test-asrs", RunnerGroupID: 1, RunnerGroupName: "testgroup"}, nil),
						scalefake.WithDeleteRunnerScaleSet(nil),
					),
				),
			)),
		}

		runnerSetController := &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoscalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ResourceBuilder:                    builder,
		}
		Expect(runnerSetController.SetupWithManager(mgr)).To(Succeed(), "failed to setup AutoscalingRunnerSet controller")

		listenerController := &AutoscalingListenerReconciler{
			Client:          mgr.GetClient(),
			Scheme:          mgr.GetScheme(),
			Log:             logf.Log,
			ResourceBuilder: builder,
		}
		Expect(listenerController.SetupWithManager(mgr)).To(Succeed(), "failed to setup AutoscalingListener controller")

		startManagers(GinkgoT(), mgr)

		minRunners := 1
		maxRunners := 10
		autoscalingRunnerSet := &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-asrs",
				Namespace: autoscalingNS.Name,
				Labels:    map[string]string{LabelKeyKubernetesVersion: build.Version},
				Annotations: map[string]string{
					runnerScaleSetIDAnnotationKey:         strconv.Itoa(originalRunnerScaleSetID),
					AnnotationKeyGitHubRunnerScaleSetName: "test-asrs",
					AnnotationKeyGitHubRunnerGroupName:    "testgroup",
				},
			},
			Spec: v1alpha1.AutoscalingRunnerSetSpec{
				GitHubConfigUrl:    "https://github.com/owner/repo",
				GitHubConfigSecret: configSecret.Name,
				MaxRunners:         &maxRunners,
				MinRunners:         &minRunners,
				RunnerGroup:        "testgroup",
				RunnerScaleSetName: "test-asrs",
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, autoscalingRunnerSet)).To(Succeed(), "failed to create AutoscalingRunnerSet")

		listenerKey := types.NamespacedName{Namespace: autoscalingNS.Name, Name: scaleSetListenerName(autoscalingRunnerSet)}

		listener := new(v1alpha1.AutoscalingListener)
		pod := new(corev1.Pod)
		Eventually(
			func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
				g.Expect(listener.Spec.RunnerScaleSetID).To(Equal(originalRunnerScaleSetID))
				g.Expect(k8sClient.Get(ctx, listenerKey, pod)).To(Succeed())
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(Succeed(), "the listener and its pod should be running against the original scale set")
		originalListenerUID := listener.UID

		// The scale set is deleted on the Actions service side, and the next
		// listener pod fails to create its session because of it.
		scaleSetDeleted.Store(true)
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{
			{
				Name: autoscalingListenerContainerName,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{ExitCode: ghalistenerconfig.ExitCodeRunnerScaleSetNotFound},
				},
			},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed(), "failed to report the listener container as terminated")

		Eventually(
			func(g Gomega) {
				current := new(v1alpha1.AutoscalingRunnerSet)
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(autoscalingRunnerSet), current)).To(Succeed())
				g.Expect(current.Annotations[runnerScaleSetIDAnnotationKey]).To(Equal(strconv.Itoa(replacementRunnerScaleSetID)),
					"the scale set should be registered again")

				runnerSet := new(v1alpha1.EphemeralRunnerSet)
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(autoscalingRunnerSet), runnerSet)).To(Succeed())
				g.Expect(runnerSet.Spec.EphemeralRunnerSpec.RunnerScaleSetID).To(Equal(replacementRunnerScaleSetID),
					"the EphemeralRunnerSet should follow the re-registration")

				rebuilt := new(v1alpha1.AutoscalingListener)
				g.Expect(k8sClient.Get(ctx, listenerKey, rebuilt)).To(Succeed())
				g.Expect(rebuilt.UID).NotTo(Equal(originalListenerUID), "the listener should be a new object")
				g.Expect(rebuilt.Spec.RunnerScaleSetID).To(Equal(replacementRunnerScaleSetID),
					"the rebuilt listener should use the new scale set ID")

				g.Expect(k8sClient.Get(ctx, listenerKey, new(corev1.Pod))).To(Succeed(), "the rebuilt listener should have a pod")
			},
			autoscalingRunnerSetTestTimeout,
			autoscalingRunnerSetTestInterval,
		).Should(Succeed())
	})
})
