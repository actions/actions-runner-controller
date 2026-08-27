package actionsgithubcom

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
)

// listVariantERS lists the EphemeralRunnerSets owned by an AutoscalingRunnerSet,
// keyed by the runner-variant label ("" for the default variant).
func listVariantERS(ctx context.Context, ns string) (map[string]v1alpha1.EphemeralRunnerSet, error) {
	list := new(v1alpha1.EphemeralRunnerSetList)
	if err := k8sClient.List(ctx, list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	out := make(map[string]v1alpha1.EphemeralRunnerSet, len(list.Items))
	for _, ers := range list.Items {
		out[ers.Labels[LabelKeyGitHubRunnerVariant]] = ers
	}
	return out, nil
}

var _ = Describe("Test AutoScalingRunnerSet multi-variant controller", Ordered, func() {
	var ctx context.Context
	var mgr ctrl.Manager
	var controller *AutoscalingRunnerSetReconciler
	var autoscalingNS *corev1.Namespace
	var ars *v1alpha1.AutoscalingRunnerSet
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

		// Assign a distinct scale-set id per requested name so the id map the
		// reconciler stamps has one entry per variant. The default set is created
		// under ars.Name; named variants under "<ars>-<variant>".
		var idLock sync.Mutex
		idByName := map[string]int{}
		nextID := 100

		controller = &AutoscalingRunnerSetReconciler{
			Client:                             mgr.GetClient(),
			Scheme:                             mgr.GetScheme(),
			Log:                                logf.Log,
			ControllerNamespace:                autoscalingNS.Name,
			DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc",
			ResourceBuilder: ResourceBuilder{
				SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient(
					scalefake.WithClient(
						scalefake.NewClient(
							scalefake.WithGetRunnerGroupByNameFunc(func(ctx context.Context, groupName string) (*scaleset.RunnerGroup, error) {
								return &scaleset.RunnerGroup{ID: 1, Name: groupName}, nil
							}),
							scalefake.WithGetRunnerScaleSet(nil, nil),
							scalefake.WithCreateRunnerScaleSetFunc(func(ctx context.Context, rs *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
								idLock.Lock()
								defer idLock.Unlock()
								id, ok := idByName[rs.Name]
								if !ok {
									nextID++
									id = nextID
									idByName[rs.Name] = id
								}
								return &scaleset.RunnerScaleSet{ID: id, Name: rs.Name, RunnerGroupID: rs.RunnerGroupID, RunnerGroupName: "testgroup"}, nil
							}),
							scalefake.WithUpdateRunnerScaleSetFunc(func(ctx context.Context, scaleSetID int, rs *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
								return &scaleset.RunnerScaleSet{ID: scaleSetID, Name: rs.Name, RunnerGroupID: rs.RunnerGroupID, RunnerGroupName: "testgroup"}, nil
							}),
							scalefake.WithDeleteRunnerScaleSet(nil),
						),
					),
				)),
			},
		}
		Expect(controller.SetupWithManager(mgr)).NotTo(HaveOccurred(), "failed to setup controller")

		min := 0
		max := 5
		bigMax := 20
		ars = &v1alpha1.AutoscalingRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-mv",
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
							{Name: "runner", Image: "ghcr.io/actions/runner"},
						},
					},
				},
				RunnerVariants: []v1alpha1.RunnerVariant{
					{Name: "small"},
					{
						Name:       "big",
						MaxRunners: &bigMax,
						Template: &corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{Name: "runner", Image: "ghcr.io/actions/runner-gpu"},
								},
							},
						},
					},
				},
			},
		}

		Expect(k8sClient.Create(ctx, ars)).NotTo(HaveOccurred(), "failed to create AutoScalingRunnerSet")

		startManagers(GinkgoT(), mgr)
	})

	Context("When creating a multi-variant AutoScalingRunnerSet", func() {
		It("creates one EphemeralRunnerSet per variant, a single listener, and a single RBAC bundle", func() {
			// Two EphemeralRunnerSets, one per named variant, each carrying the
			// runner-variant label and its own resolved scale-set id.
			Eventually(
				func() (map[string]v1alpha1.EphemeralRunnerSet, error) {
					return listVariantERS(ctx, ars.Namespace)
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(HaveLen(2), "one EphemeralRunnerSet per variant")

			byVariant, err := listVariantERS(ctx, ars.Namespace)
			Expect(err).NotTo(HaveOccurred())

			small, ok := byVariant["small"]
			Expect(ok).To(BeTrue(), "small variant ERS should exist")
			Expect(small.Name).To(Equal("test-mv-small"))
			Expect(small.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image).To(Equal("ghcr.io/actions/runner"))

			big, ok := byVariant["big"]
			Expect(ok).To(BeTrue(), "big variant ERS should exist")
			Expect(big.Name).To(Equal("test-mv-big"))
			Expect(big.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image).To(Equal("ghcr.io/actions/runner-gpu"))

			// Each variant registered a distinct scale-set id.
			Expect(small.Spec.EphemeralRunnerSpec.RunnerScaleSetID).NotTo(Equal(big.Spec.EphemeralRunnerSpec.RunnerScaleSetID))

			// The ARS carries a variant id map with one entry per variant.
			created := new(v1alpha1.AutoscalingRunnerSet)
			Eventually(
				func() (int, error) {
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ars), created); err != nil {
						return 0, err
					}
					ids, err := decodeRunnerScaleSetIDs(created.Annotations[AnnotationKeyGitHubRunnerScaleSetIDs])
					if err != nil {
						return 0, err
					}
					return len(ids), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Equal(2), "id map should have one entry per named variant")

			ids, err := decodeRunnerScaleSetIDs(created.Annotations[AnnotationKeyGitHubRunnerScaleSetIDs])
			Expect(err).NotTo(HaveOccurred())
			Expect(ids).To(HaveKey("small"))
			Expect(ids).To(HaveKey("big"))

			// Exactly one AutoscalingListener, carrying the scale-set tuple list.
			listener := new(v1alpha1.AutoscalingListener)
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(ars), Namespace: ars.Namespace}, listener)
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "a single listener should be created")

			Eventually(
				func() (int, error) {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(ars), Namespace: ars.Namespace}, listener); err != nil {
						return 0, err
					}
					tuples, err := decodeListenerScaleSets(listener.Annotations[AnnotationKeyGitHubListenerScaleSets])
					if err != nil {
						return 0, err
					}
					return len(tuples), nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Equal(2), "listener annotation should list both scale sets")

			// One listener namespace holds exactly one AutoscalingListener.
			allListeners := new(v1alpha1.AutoscalingListenerList)
			Expect(k8sClient.List(ctx, allListeners, client.InNamespace(controller.ControllerNamespace))).To(Succeed())
			Expect(allListeners.Items).To(HaveLen(1), "one listener pod bundle for the whole set")
		})
	})

	Context("When a variant is removed", func() {
		It("deletes the orphaned EphemeralRunnerSet", func() {
			Eventually(
				func() (int, error) {
					m, err := listVariantERS(ctx, ars.Namespace)
					return len(m), err
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Equal(2), "both variant ERS created first")

			// Drop the "big" variant.
			updated := new(v1alpha1.AutoscalingRunnerSet)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ars), updated)).To(Succeed())
			updated.Spec.RunnerVariants = []v1alpha1.RunnerVariant{{Name: "small"}}
			Expect(k8sClient.Update(ctx, updated)).To(Succeed())

			// The AutoscalingRunnerSet reconciler's job is to request deletion of the
			// orphaned variant's EphemeralRunnerSet. The object is only removed once
			// the EphemeralRunnerSet controller clears its finalizer, which is out of
			// scope for this reconciler test and not wired here, so assert the
			// deletion was requested (non-zero DeletionTimestamp) and the survivor is
			// untouched.
			Eventually(
				func() error {
					m, err := listVariantERS(ctx, ars.Namespace)
					if err != nil {
						return err
					}
					big, ok := m["big"]
					if !ok {
						return nil // fully gone is also acceptable
					}
					if big.DeletionTimestamp.IsZero() {
						return fmt.Errorf("big variant ERS deletion not requested")
					}
					if survivor, ok := m["small"]; !ok || !survivor.DeletionTimestamp.IsZero() {
						return fmt.Errorf("small variant ERS should be present and not deleted")
					}
					return nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "removed variant's ERS should be marked for deletion, survivor kept")
		})
	})

	Context("When deleting a multi-variant AutoScalingRunnerSet", func() {
		It("cleans up every EphemeralRunnerSet and the listener", func() {
			Eventually(
				func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: scaleSetListenerName(ars), Namespace: ars.Namespace}, new(v1alpha1.AutoscalingListener))
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "listener should be created before delete")

			Expect(k8sClient.Delete(ctx, ars)).To(Succeed(), "failed to delete AutoScalingRunnerSet")

			Eventually(
				func() error {
					m, err := listVariantERS(ctx, ars.Namespace)
					if err != nil {
						return err
					}
					if len(m) != 0 {
						return fmt.Errorf("EphemeralRunnerSet not deleted, count=%d", len(m))
					}
					return nil
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "all EphemeralRunnerSets should be deleted")

			Eventually(
				func() error {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ars), new(v1alpha1.AutoscalingRunnerSet))
					if err != nil && errors.IsNotFound(err) {
						return nil
					}
					return fmt.Errorf("AutoScalingRunnerSet is not deleted")
				},
				autoscalingRunnerSetTestTimeout,
				autoscalingRunnerSetTestInterval,
			).Should(Succeed(), "AutoScalingRunnerSet should be deleted")
		})
	})
})
