package actionsgithubcom

import (
	"context"
	"fmt"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type metadataPatchRecorder struct {
	client.Client
	patches []string
}

func (c *metadataPatchRecorder) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches = append(c.patches, fmt.Sprintf("%T/%s", obj, obj.GetName()))
	return c.Client.Patch(ctx, obj, patch, opts...)
}

var _ = Describe("Resource metadata empty collection convergence", func() {
	fields := []string{
		"autoscalingListener",
		"listenerServiceAccountMetadata",
		"listenerRoleMetadata",
		"listenerRoleBindingMetadata",
		"listenerConfigSecretMetadata",
		"ephemeralRunnerSetMetadata",
		"ephemeralRunnerMetadata",
		"ephemeralRunnerConfigSecretMetadata",
	}
	for _, field := range append(fields, "all") {
		for _, keys := range [][]string{{"labels"}, {"annotations"}, {"labels", "annotations"}} {
			It(fmt.Sprintf("%s with empty %v", field, keys), func() {
				ctx, cancel := context.WithTimeout(context.Background(), autoscalingRunnerSetTestTimeout)
				defer cancel()
				ns, mgr := createNamespace(GinkgoT(), k8sClient)
				secret := createDefaultSecret(GinkgoT(), k8sClient, ns.Name)
				const name = "empty-metadata"
				scaleSet := &scaleset.RunnerScaleSet{ID: 1, Name: name, RunnerGroupID: 1, RunnerGroupName: "Default"}
				cache := newTestResourceCache()
				builder := ResourceBuilder{
					Scheme: mgr.GetScheme(), ResourceCache: cache,
					SecretResolver: secretresolver.New(k8sClient, scalefake.NewMultiClient(scalefake.WithClient(
						scalefake.NewClient(
							scalefake.WithCreateRunnerScaleSet(scaleSet, nil),
							scalefake.WithGetRunnerScaleSetByID(scaleSet, nil),
							scalefake.WithGenerateJitRunnerConfig(&scaleset.RunnerScaleSetJitRunnerConfig{
								Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner", RunnerScaleSetID: 1},
								EncodedJITConfig: "fake-jit-config",
							}, nil),
						),
					))),
				}
				cachedClient := &metadataPatchRecorder{Client: mgr.GetClient()}
				directClient := &metadataPatchRecorder{Client: k8sClient}
				arsController := &AutoscalingRunnerSetReconciler{
					Client: cachedClient, Scheme: mgr.GetScheme(), Log: logf.Log,
					ControllerNamespace: ns.Name, DefaultRunnerScaleSetListenerImage: "listener:latest",
					ResourceBuilder: builder,
				}
				listenerController := &AutoscalingListenerReconciler{
					Client: directClient, Scheme: mgr.GetScheme(), Log: logf.Log,
					ListenerMetricsAddr: "0", ResourceBuilder: builder,
				}
				ersController := &EphemeralRunnerSetReconciler{
					Client: cachedClient, APIReader: k8sClient, Scheme: mgr.GetScheme(), Log: logf.Log,
					ResourceBuilder: builder,
				}
				runnerController := &EphemeralRunnerReconciler{
					Client: directClient, APIReader: k8sClient, Scheme: mgr.GetScheme(), Log: logf.Log,
					ResourceBuilder: builder,
				}
				startManagers(GinkgoT(), mgr)

				spec := map[string]any{
					"githubConfigUrl": "https://github.com/owner/repo", "githubConfigSecret": secret.Name,
					"template": map[string]any{"spec": map[string]any{
						"containers": []any{map[string]any{"name": "runner", "image": "runner:latest"}},
					}},
				}
				selected := []string{field}
				if field == "all" {
					selected = fields
				}
				for _, resource := range selected {
					metadata := map[string]any{}
					for _, key := range keys {
						metadata[key] = map[string]any{}
					}
					spec[resource] = metadata
				}
				if field == "all" {
					Expect(unstructured.SetNestedMap(spec, spec["autoscalingListener"].(map[string]any), "template", "metadata")).To(Succeed())
					Expect(unstructured.SetNestedMap(spec, spec["autoscalingListener"].(map[string]any), "listenerTemplate", "metadata")).To(Succeed())
					Expect(unstructured.SetNestedSlice(spec, []any{map[string]any{"name": "listener"}}, "listenerTemplate", "spec", "containers")).To(Succeed())
					spec["proxy"] = map[string]any{
						"http": map[string]any{"url": "http://proxy.example.com:8080", "credentialSecretRef": "proxy-auth"},
					}
					Expect(k8sClient.Create(ctx, &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: "proxy-auth", Namespace: ns.Name},
						Data:       map[string][]byte{"username": []byte("user"), "password": []byte("password")},
					})).To(Succeed())
				}
				raw := &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": v1alpha1.GroupVersion.String(), "kind": "AutoscalingRunnerSet",
					"metadata": map[string]any{
						"name": name, "namespace": ns.Name,
						"labels": map[string]any{LabelKeyKubernetesVersion: build.Version},
					},
					"spec": spec,
				}}
				// Typed Create erases these maps before admission, hiding the regression.
				Expect(k8sClient.Create(ctx, raw)).To(Succeed())
				arsKey := client.ObjectKeyFromObject(raw)
				ars := new(v1alpha1.AutoscalingRunnerSet)
				Expect(k8sClient.Get(ctx, arsKey, ars)).To(Succeed())
				listenerKey := client.ObjectKey{Namespace: ns.Name, Name: scaleSetListenerName(ars)}
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
				reconcileARS := func() {
					waitForCache(arsKey, new(v1alpha1.AutoscalingRunnerSet))
					waitForCache(arsKey, new(v1alpha1.EphemeralRunnerSet))
					waitForCache(listenerKey, new(v1alpha1.AutoscalingListener))
					_, err := arsController.Reconcile(ctx, ctrl.Request{NamespacedName: arsKey})
					Expect(err).NotTo(HaveOccurred())
				}
				reconcileListener := func() {
					_, err := listenerController.Reconcile(ctx, ctrl.Request{NamespacedName: listenerKey})
					Expect(err).NotTo(HaveOccurred())
				}
				for range 10 {
					reconcileARS()
					reconcileListener()
				}
				Expect(k8sClient.Get(ctx, arsKey, ars)).To(Succeed())
				Expect(ars.Status.Phase).To(Equal(v1alpha1.AutoscalingRunnerSetPhaseRunning))
				Expect(ars.Status.ObservedGeneration).To(Equal(ars.Generation))

				ers := new(v1alpha1.EphemeralRunnerSet)
				Expect(k8sClient.Get(ctx, arsKey, ers)).To(Succeed())
				Expect(k8sClient.Patch(ctx, ers, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"replicas":1}}`)))).To(Succeed())
				reconcileERS := func() {
					waitForCache(arsKey, new(v1alpha1.EphemeralRunnerSet))
					_, err := ersController.Reconcile(ctx, ctrl.Request{NamespacedName: arsKey})
					Expect(err).NotTo(HaveOccurred())
				}
				for range 2 {
					reconcileERS()
				}
				runners := new(v1alpha1.EphemeralRunnerList)
				Expect(k8sClient.List(ctx, runners, client.InNamespace(ns.Name))).To(Succeed())
				Expect(runners.Items).To(HaveLen(1))
				runnerKey := client.ObjectKeyFromObject(&runners.Items[0])
				reconcileRunner := func() {
					_, err := runnerController.Reconcile(ctx, ctrl.Request{NamespacedName: runnerKey})
					Expect(err).NotTo(HaveOccurred())
					waitForCache(runnerKey, new(v1alpha1.EphemeralRunner))
				}
				reconcileRunner()

				objects := []client.Object{
					ars, ers,
					&v1alpha1.AutoscalingListener{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name, Namespace: ns.Name}},
					&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name, Namespace: ns.Name}},
					&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name, Namespace: ns.Name}},
					&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name, Namespace: ns.Name}},
					&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name + "-config", Namespace: ns.Name}},
					&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name, Namespace: ns.Name}},
					&runners.Items[0],
					&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: runnerKey.Name, Namespace: ns.Name}},
					&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: runnerKey.Name, Namespace: ns.Name}},
				}
				if field == "all" {
					objects = append(objects,
						&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: listenerKey.Name + "-proxy", Namespace: ns.Name}},
						&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: proxyEphemeralRunnerSetSecretName(ers), Namespace: ns.Name}},
					)
				}
				reconcileAll := func() {
					reconcileARS()
					reconcileListener()
					reconcileERS()
					reconcileRunner()
				}
				for range 3 {
					reconcileAll()
				}
				for _, obj := range objects {
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
					Expect(obj.GetDeletionTimestamp().IsZero()).To(BeTrue())
					// Creation normalizes cached desired pointers. Rebuild them as a
					// restart or dependency update would, rather than testing only hits.
					cache.Delete(obj)
				}
				cachedClient.patches, directClient.patches = nil, nil
				for range 5 {
					reconcileAll()
					for _, obj := range objects {
						fresh := obj.DeepCopyObject().(client.Object)
						Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), fresh)).To(Succeed())
						Expect(fresh.GetUID()).To(Equal(obj.GetUID()), "%T must not be replaced", obj)
						Expect(fresh.GetResourceVersion()).To(Equal(obj.GetResourceVersion()), "%T must not be rewritten", obj)
						Expect(fresh.GetDeletionTimestamp().IsZero()).To(BeTrue())
					}
				}
				Expect(cachedClient.patches).To(BeEmpty(), "even no-op patches must not starve later reconciliation")
				Expect(directClient.patches).To(BeEmpty())
				Expect(k8sClient.Get(ctx, arsKey, raw)).To(Succeed())
				for _, resource := range selected {
					for _, key := range keys {
						value, found, err := unstructured.NestedStringMap(raw.Object, "spec", resource, key)
						Expect(err).NotTo(HaveOccurred())
						Expect(found).To(BeTrue(), "controller patches must retain the explicit empty input")
						Expect(value).To(BeEmpty())
					}
				}

				By("propagating a genuine spec change while keeping all empty metadata inputs")
				listener := new(v1alpha1.AutoscalingListener)
				Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
				oldUID := listener.UID
				Expect(k8sClient.Patch(ctx, raw, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"maxRunners":2}}`)))).To(Succeed())
				for range 5 {
					reconcileARS()
					Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
					if !listener.DeletionTimestamp.IsZero() {
						break
					}
				}
				Expect(listener.DeletionTimestamp.IsZero()).To(BeFalse(), "empty runner metadata must not block listener updates")
				for range 10 {
					reconcileListener()
					err := k8sClient.Get(ctx, listenerKey, listener)
					if kerrors.IsNotFound(err) {
						break
					}
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(kerrors.IsNotFound(k8sClient.Get(ctx, listenerKey, listener))).To(BeTrue())
				for range 10 {
					reconcileARS()
					reconcileListener()
				}
				Expect(k8sClient.Get(ctx, listenerKey, listener)).To(Succeed())
				Expect(listener.UID).NotTo(Equal(oldUID))
				Expect(listener.Spec.MaxRunners).To(Equal(2))
				Expect(listener.DeletionTimestamp.IsZero()).To(BeTrue())
				Expect(k8sClient.Get(ctx, arsKey, ars)).To(Succeed())
				Expect(ars.Status.Phase).To(Equal(v1alpha1.AutoscalingRunnerSetPhaseRunning))
			})
		}
	}
})
