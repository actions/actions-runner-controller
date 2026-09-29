package actionsgithubcom

import (
	"context"
	"encoding/json"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("AutoscalingListener empty collection convergence", func() {
	for _, tc := range listenerEmptyCollectionTestCases() {
		It(tc.name, func() {
			ctx, cancel := context.WithTimeout(context.Background(), autoscalingRunnerSetTestTimeout)
			defer cancel()
			ns, mgr := createNamespace(GinkgoT(), k8sClient)
			secret := createDefaultSecret(GinkgoT(), k8sClient, ns.Name)
			const name = "empty-listener"
			scaleSet := &scaleset.RunnerScaleSet{ID: 1, Name: name, RunnerGroupID: 1, RunnerGroupName: "Default"}
			builder := ResourceBuilder{
				Scheme:        mgr.GetScheme(),
				ResourceCache: newTestResourceCache(),
				SecretResolver: secretresolver.New(k8sClient, scalefake.NewMultiClient(scalefake.WithClient(
					scalefake.NewClient(
						scalefake.WithCreateRunnerScaleSet(scaleSet, nil),
						scalefake.WithGetRunnerScaleSetByID(scaleSet, nil),
					),
				))),
			}
			runnerController := &AutoscalingRunnerSetReconciler{
				Client:                             mgr.GetClient(),
				Scheme:                             mgr.GetScheme(),
				Log:                                logf.Log,
				ControllerNamespace:                ns.Name,
				DefaultRunnerScaleSetListenerImage: "ghcr.io/actions/arc:latest",
				ResourceBuilder:                    builder,
			}
			listenerController := &AutoscalingListenerReconciler{
				Client:              k8sClient,
				Scheme:              mgr.GetScheme(),
				Log:                 logf.Log,
				ListenerMetricsAddr: "0",
				ResourceBuilder:     builder,
			}
			// Run the indexed cache, but drive reconciles explicitly so UID
			// stability is checked after known reconciles, not a timed quiet period.
			startManagers(GinkgoT(), mgr)

			var spec map[string]any
			Expect(json.Unmarshal([]byte(tc.runnerSetSpec), &spec)).To(Succeed())
			spec["githubConfigUrl"] = "https://github.com/owner/repo"
			spec["githubConfigSecret"] = secret.Name
			spec["maxRunners"] = int64(5)
			spec["template"] = map[string]any{
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "runner", "image": "ghcr.io/actions/runner:latest"}},
				},
			}
			raw := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": v1alpha1.GroupVersion.String(),
				"kind":       "AutoscalingRunnerSet",
				"metadata": map[string]any{
					"name": name, "namespace": ns.Name,
					"labels": map[string]any{LabelKeyKubernetesVersion: build.Version},
				},
				"spec": spec,
			}}
			// A typed Create would erase the empty input before it reached the API.
			Expect(k8sClient.Create(ctx, raw)).To(Succeed())
			runnerSet := new(v1alpha1.AutoscalingRunnerSet)
			runnerKey := client.ObjectKeyFromObject(raw)
			Expect(k8sClient.Get(ctx, runnerKey, runnerSet)).To(Succeed())
			listenerKey := client.ObjectKey{Namespace: ns.Name, Name: scaleSetListenerName(runnerSet)}

			waitForCache := func(key client.ObjectKey, obj client.Object) {
				err := k8sClient.Get(ctx, key, obj)
				if kerrors.IsNotFound(err) {
					Eventually(func() bool {
						return kerrors.IsNotFound(mgr.GetClient().Get(ctx, key, obj))
					}, autoscalingRunnerSetTestTimeout, 10*time.Millisecond).Should(BeTrue())
					return
				}
				Expect(err).NotTo(HaveOccurred())
				version := obj.GetResourceVersion()
				Eventually(func(g Gomega) {
					g.Expect(mgr.GetClient().Get(ctx, key, obj)).To(Succeed())
					g.Expect(obj.GetResourceVersion()).To(Equal(version))
				}, autoscalingRunnerSetTestTimeout, 10*time.Millisecond).Should(Succeed())
			}
			reconcileRunnerSet := func() {
				waitForCache(runnerKey, new(v1alpha1.AutoscalingRunnerSet))
				waitForCache(runnerKey, new(v1alpha1.EphemeralRunnerSet))
				waitForCache(listenerKey, new(v1alpha1.AutoscalingListener))
				_, err := runnerController.Reconcile(ctx, ctrl.Request{NamespacedName: runnerKey})
				Expect(err).NotTo(HaveOccurred())
			}
			reconcileListener := func() {
				_, err := listenerController.Reconcile(ctx, ctrl.Request{NamespacedName: listenerKey})
				Expect(err).NotTo(HaveOccurred())
			}
			listener := new(v1alpha1.AutoscalingListener)
			for range 10 {
				reconcileRunnerSet()
				err := k8sClient.Get(ctx, listenerKey, listener)
				if err == nil {
					break
				}
				Expect(kerrors.IsNotFound(err)).To(BeTrue())
			}
			Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())

			assertRoundTrip := func() {
				Expect(k8sClient.Get(ctx, runnerKey, runnerSet)).To(Succeed())
				ephemeralRunnerSet := new(v1alpha1.EphemeralRunnerSet)
				Expect(k8sClient.Get(ctx, runnerKey, ephemeralRunnerSet)).To(Succeed())
				uncachedBuilder := ResourceBuilder{ResourceCache: newTestResourceCache()}
				desired, err := uncachedBuilder.newAutoscalingListener(
					runnerSet, ephemeralRunnerSet, ns.Name, runnerController.DefaultRunnerScaleSetListenerImage, nil,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
				persisted := tc.collections(listener.Spec)
				for i, collection := range tc.collections(desired.Spec) {
					Expect(collection).NotTo(BeNil(), "the API must preserve explicit empty ARS input")
					Expect(collection).To(BeEmpty())
					Expect(persisted[i]).To(BeNil(), "the derived listener must lose empty collections on write")
				}
			}
			assertRoundTrip()

			createListenerPod := func() *corev1.Pod {
				pod := new(corev1.Pod)
				for range 10 {
					reconcileListener()
					err := k8sClient.Get(ctx, listenerKey, pod)
					if err == nil {
						return pod
					}
					Expect(kerrors.IsNotFound(err)).To(BeTrue())
				}
				Fail("listener controller did not create its pod")
				return nil
			}
			assertStable := func(uid, podUID types.UID) {
				for range 5 {
					reconcileRunnerSet()
					Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
					Expect(listener.UID).To(Equal(uid), "unchanged configuration must retain the listener UID")
					Expect(listener.DeletionTimestamp.IsZero()).To(BeTrue(), "empty collections must not trigger replacement")
					reconcileListener()
					pod := new(corev1.Pod)
					Expect(k8sClient.Get(ctx, listenerKey, pod)).To(Succeed())
					Expect(pod.UID).To(Equal(podUID))
				}
				Expect(k8sClient.Get(ctx, runnerKey, runnerSet)).To(Succeed())
				Expect(runnerSet.Status.Phase).To(Equal(v1alpha1.AutoscalingRunnerSetPhaseRunning))
				Expect(runnerSet.Status.ObservedGeneration).To(Equal(runnerSet.Generation))
			}
			pod := createListenerPod()
			uid := listener.UID
			assertStable(uid, pod.UID)

			By("replacing the listener for a genuine spec change while retaining empty ARS input")
			Expect(k8sClient.Patch(ctx, raw, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"maxRunners":6}}`)))).To(Succeed())
			for range 5 {
				reconcileRunnerSet()
				Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
				if !listener.DeletionTimestamp.IsZero() {
					break
				}
			}
			Expect(listener.UID).To(Equal(uid))
			Expect(listener.DeletionTimestamp.IsZero()).To(BeFalse(), "nonempty config drift must still trigger replacement")

			// envtest has no garbage collector; exercise ARC's child cleanup
			// rather than manually removing the listener finalizer or its children.
			for range 10 {
				reconcileListener()
				err := k8sClient.Get(ctx, listenerKey, new(v1alpha1.AutoscalingListener))
				if kerrors.IsNotFound(err) {
					break
				}
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(kerrors.IsNotFound(k8sClient.Get(ctx, listenerKey, new(v1alpha1.AutoscalingListener)))).To(BeTrue())
			for _, child := range []client.Object{new(corev1.Pod), new(corev1.ServiceAccount), new(rbacv1.Role), new(rbacv1.RoleBinding)} {
				Expect(kerrors.IsNotFound(k8sClient.Get(ctx, listenerKey, child))).To(BeTrue())
			}
			configKey := client.ObjectKey{Namespace: ns.Name, Name: scaleSetListenerConfigName(listener)}
			Expect(kerrors.IsNotFound(k8sClient.Get(ctx, configKey, new(corev1.Secret)))).To(BeTrue())

			reconcileRunnerSet()
			Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
			Expect(listener.UID).NotTo(Equal(uid), "real drift must produce a replacement listener")
			Expect(listener.Spec.MaxRunners).To(Equal(6))
			assertRoundTrip()
			pod = createListenerPod()
			assertStable(listener.UID, pod.UID)
		})
	}
})
