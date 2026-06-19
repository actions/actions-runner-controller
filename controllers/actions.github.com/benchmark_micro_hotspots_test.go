package actionsgithubcom

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func BenchmarkActionsGithub_Hash_EphemeralRunnerSetIntegrity(b *testing.B) {
	testCases := []struct {
		name     string
		replicas int
		envCount int
	}{
		{name: "small", replicas: 3, envCount: 5},
		{name: "medium", replicas: 20, envCount: 30},
		{name: "large", replicas: 100, envCount: 120},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ers := NewMinimalEphemeralRunnerSet("default", "bench-ers")
			ers.Spec.Replicas = tc.replicas
			ers.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env = make([]corev1.EnvVar, 0, tc.envCount)
			for i := 0; i < tc.envCount; i++ {
				ers.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env = append(
					ers.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Env,
					corev1.EnvVar{Name: fmt.Sprintf("KEY_%d", i), Value: fmt.Sprintf("VALUE_%d", i)},
				)
			}

			b.ResetTimer()
			for b.Loop() {
				_ = ephemeralRunnerSetIntegrityHash(ers)
			}
		})
	}
}

func BenchmarkActionsGithub_ProxyConfig_ToSecretData(b *testing.B) {
	testCases := []struct {
		name          string
		withCreds     bool
		noProxyCount  int
		secretPayload int
	}{
		{name: "no_creds_small", withCreds: false, noProxyCount: 5, secretPayload: 16},
		{name: "creds_medium", withCreds: true, noProxyCount: 25, secretPayload: 128},
		{name: "creds_large", withCreds: true, noProxyCount: 100, secretPayload: 1024},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			proxy := &actionsv1alpha1.ProxyConfig{
				NoProxy: make([]string, 0, tc.noProxyCount),
			}
			for i := 0; i < tc.noProxyCount; i++ {
				proxy.NoProxy = append(proxy.NoProxy, fmt.Sprintf("service-%d.default.svc", i))
			}

			proxy.HTTP = &actionsv1alpha1.ProxyServerConfig{URL: "http://proxy.example.com:8080"}
			proxy.HTTPS = &actionsv1alpha1.ProxyServerConfig{URL: "https://secure-proxy.example.com:8443"}
			if tc.withCreds {
				proxy.HTTP.CredentialSecretRef = "proxy-creds"
				proxy.HTTPS.CredentialSecretRef = "proxy-creds"
			}

			secret := &corev1.Secret{
				Data: map[string][]byte{
					"username": []byte("user-" + string(make([]byte, tc.secretPayload))),
					"password": []byte("pass-" + string(make([]byte, tc.secretPayload))),
				},
			}

			fetcher := func(name string) (*corev1.Secret, error) {
				if name != "proxy-creds" {
					return nil, fmt.Errorf("unexpected secret: %s", name)
				}
				return secret, nil
			}

			b.ResetTimer()
			for b.Loop() {
				_, _ = proxy.ToSecretData(fetcher)
			}
		})
	}
}

func BenchmarkActionsGithub_ClassifyEphemeralRunnersByState(b *testing.B) {
	testCases := []struct {
		name         string
		runnerCount  int
		includeSlice bool
	}{
		{name: "counts_only_100", runnerCount: 100, includeSlice: false},
		{name: "counts_only_1000", runnerCount: 1000, includeSlice: false},
		{name: "with_slices_100", runnerCount: 100, includeSlice: true},
		{name: "with_slices_1000", runnerCount: 1000, includeSlice: true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			runnerList := newBenchmarkEphemeralRunnerList(tc.runnerCount)

			b.ResetTimer()
			for b.Loop() {
				_ = newEphemeralRunnersByStates(runnerList, tc.includeSlice, true)
			}
		})
	}
}

func BenchmarkActionsGithub_EphemeralRunnerStepper(b *testing.B) {
	testCases := []struct {
		name        string
		pendingSize int
		runningSize int
	}{
		{name: "small", pendingSize: 20, runningSize: 20},
		{name: "medium", pendingSize: 100, runningSize: 200},
		{name: "large", pendingSize: 500, runningSize: 500},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			pending := make([]*actionsv1alpha1.EphemeralRunner, 0, tc.pendingSize)
			running := make([]*actionsv1alpha1.EphemeralRunner, 0, tc.runningSize)

			now := time.Now()
			for i := 0; i < tc.pendingSize; i++ {
				r := NewMinimalEphemeralRunner("default", fmt.Sprintf("pending-%d", i))
				r.CreationTimestamp = metav1.NewTime(now.Add(time.Duration(i) * time.Second))
				pending = append(pending, r)
			}
			for i := 0; i < tc.runningSize; i++ {
				r := NewMinimalEphemeralRunner("default", fmt.Sprintf("running-%d", i))
				r.CreationTimestamp = metav1.NewTime(now.Add(time.Duration(i) * time.Second))
				r.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseRunning
				running = append(running, r)
			}

			b.ResetTimer()
			for b.Loop() {
				pendingCopy := append([]*actionsv1alpha1.EphemeralRunner(nil), pending...)
				runningCopy := append([]*actionsv1alpha1.EphemeralRunner(nil), running...)
				stepper := newEphemeralRunnerStepper(pendingCopy, runningCopy)
				for stepper.next() {
					_ = stepper.object()
				}
			}
		})
	}
}

func BenchmarkActionsGithub_ClientPatch_EphemeralRunnerSetAnnotation(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)

	ers := NewMinimalEphemeralRunnerSet("default", "patch-bench-ers")
	controllerutil.AddFinalizer(ers, EphemeralRunnerSetFinalizerName)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ers).
		WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}).
		Build()

	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "patch-bench-ers"}

	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		current := &actionsv1alpha1.EphemeralRunnerSet{}
		_ = fakeClient.Get(ctx, key, current)
		updated := current.DeepCopy()
		if updated.Annotations == nil {
			updated.Annotations = map[string]string{}
		}
		updated.Annotations[AnnotationKeyIntegrityHash] = "bench-hash-" + strconv.Itoa(i%2)
		_ = fakeClient.Patch(ctx, updated, client.MergeFrom(current))
	}
}

func BenchmarkActionsGithub_Reconcile_EphemeralRunnerSetProxySecret_NoOp(b *testing.B) {
	scheme := runtime.NewScheme()
	_ = actionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	ers := NewMinimalEphemeralRunnerSet("default", "proxy-noop-ers")
	ers.Labels = map[string]string{
		LabelKeyGitHubScaleSetName:      "scale-set",
		LabelKeyGitHubScaleSetNamespace: "default",
	}
	ers.Spec.EphemeralRunnerSpec.Proxy = &actionsv1alpha1.ProxyConfig{
		HTTP:  &actionsv1alpha1.ProxyServerConfig{URL: "http://proxy.example.com:8080"},
		HTTPS: &actionsv1alpha1.ProxyServerConfig{URL: "https://proxy.example.com:8443"},
		NoProxy: []string{
			"kubernetes.default.svc",
			"metadata.google.internal",
		},
	}

	proxyData, err := ers.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(func(string) (*corev1.Secret, error) {
		return nil, nil
	})
	if err != nil {
		b.Fatalf("failed to build proxy secret data: %v", err)
	}

	reconciler := &EphemeralRunnerSetReconciler{Scheme: scheme, Log: logr.Discard()}
	proxySecret, err := reconciler.newEphemeralRunnerSetProxySecret(ers, proxyData)
	if err != nil {
		b.Fatalf("failed to build proxy secret: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ers, proxySecret).
		WithStatusSubresource(&actionsv1alpha1.EphemeralRunnerSet{}).
		Build()

	reconciler.Client = fakeClient

	ctx := context.Background()
	log := logr.Discard()

	b.ResetTimer()
	for b.Loop() {
		_, _, err := reconciler.reconcileEphemeralRunnerSetProxySecret(ctx, ers, log)
		if err != nil {
			b.Fatalf("unexpected reconcile error: %v", err)
		}
	}
}

func newBenchmarkEphemeralRunnerList(count int) *actionsv1alpha1.EphemeralRunnerList {
	list := &actionsv1alpha1.EphemeralRunnerList{Items: make([]actionsv1alpha1.EphemeralRunner, 0, count)}
	now := time.Now()
	for i := 0; i < count; i++ {
		runner := *NewMinimalEphemeralRunner("default", fmt.Sprintf("runner-%d", i))
		runner.CreationTimestamp = metav1.NewTime(now.Add(time.Duration(i) * time.Second))
		if runner.Annotations == nil {
			runner.Annotations = map[string]string{}
		}
		runner.Annotations[AnnotationKeyPatchID] = strconv.Itoa(i % 8)

		switch i % 5 {
		case 0:
			runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseRunning
		case 1:
			runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseSucceeded
		case 2:
			runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseFailed
		case 3:
			runner.Status.Phase = actionsv1alpha1.EphemeralRunnerPhaseOutdated
		default:
			runner.Status.Phase = ""
		}

		if i%13 == 0 {
			ts := metav1.NewTime(now)
			runner.DeletionTimestamp = &ts
		}

		list.Items = append(list.Items, runner)
	}

	return list
}
