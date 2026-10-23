package actionsgithubcom

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestJITRateLimitReleasesReconcileWorker(t *testing.T) {
	f := newUnrecordedRunnerIDFixture(t, 0)
	require.NoError(t, f.c.Delete(t.Context(), f.pod()))
	secret := new(corev1.Secret)
	require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))
	require.NoError(t, f.c.Delete(t.Context(), secret))

	var attempts atomic.Int32
	service := newRetryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	f.runnerController.SecretResolver = &stubSecretResolver{client: service}

	start := time.Now()
	result, err := f.reconcileRunner()
	require.NoError(t, err)
	require.Equal(t, time.Minute, result.RequeueAfter)
	require.Less(t, time.Since(start), 2*time.Second)
	require.EqualValues(t, 1, attempts.Load())
	require.True(t, kerrors.IsNotFound(f.c.Get(t.Context(), f.runnerKey, new(corev1.Secret))))
	require.Nil(t, f.pod())
	require.Empty(t, f.runner().Status.Failures)
	require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)

	completed := f.runner().DeepCopy()
	completed.Name += "-completed"
	completed.ResourceVersion = ""
	completed.UID = ""
	completed.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
	require.NoError(t, f.c.Create(t.Context(), completed))
	require.NoError(t, f.c.Status().Update(t.Context(), completed))
	completedKey := client.ObjectKeyFromObject(completed)
	require.NoError(t, f.c.Create(t.Context(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: completed.Name, Namespace: completed.Namespace},
	}))
	result, err = f.runnerController.Reconcile(t.Context(), ctrl.Request{NamespacedName: completedKey})
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	require.True(t, kerrors.IsNotFound(f.c.Get(t.Context(), completedKey, new(corev1.Pod))))
	require.EqualValues(t, 1, attempts.Load(), "completed-runner cleanup must not call the throttled service")

	f.runnerController.SecretResolver = &stubSecretResolver{client: scalefake.NewClient(
		scalefake.WithGenerateJitRunnerConfig(&scaleset.RunnerScaleSetJitRunnerConfig{
			Runner: &scaleset.RunnerReference{ID: unrecordedTestRunnerID}, EncodedJITConfig: "jit",
		}, nil),
	)}
	result, err = f.reconcileRunner()
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	require.NotNil(t, f.pod())
	require.NoError(t, f.c.Get(t.Context(), f.runnerKey, new(corev1.Secret)))
}

func TestRunnerFinalizerRequeuesRateLimit(t *testing.T) {
	f := newUnrecordedRunnerIDFixture(t, 0)
	f.reply = &scaleset.RateLimitError{RetryAfter: time.Minute}
	require.NoError(t, f.c.Delete(t.Context(), f.runner()))

	result, err := f.reconcileRunner()
	require.NoError(t, err)
	require.Equal(t, time.Minute, result.RequeueAfter)
	f.requirePodKept()
	require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
	require.Empty(t, f.queue.queued())
}

func TestRunnerSetPreservesRateLimitAfterEarlierFailure(t *testing.T) {
	for _, failure := range []error{errors.New("service unavailable"), context.DeadlineExceeded} {
		for _, mode := range []string{"scale down", "pending cleanup", "running cleanup"} {
			t.Run(mode+"/"+failure.Error(), func(t *testing.T) {
				f := newUnrecordedRunnerIDFixture(t, 0)
				runner := f.runner()
				runner.Status.RunnerID = unrecordedTestRunnerID
				if mode == "pending cleanup" {
					runner.Status.Phase = v1alpha1.EphemeralRunnerPhasePending
				}
				require.NoError(t, f.c.Status().Update(t.Context(), runner))
				other := runner.DeepCopy()
				other.Name += "-other"
				other.ResourceVersion = ""
				other.UID = ""
				require.NoError(t, f.c.Create(t.Context(), other))
				require.NoError(t, f.c.Status().Update(t.Context(), other))
				calls := 0
				f.setController.SecretResolver = &stubSecretResolver{client: scalefake.NewClient(
					scalefake.WithRemoveRunnerFunc(func(context.Context, int64) error {
						calls++
						if calls == 1 {
							return failure
						}
						return &scaleset.RateLimitError{RetryAfter: time.Minute}
					}),
				)}
				if mode == "scale down" {
					set := new(v1alpha1.EphemeralRunnerSet)
					require.NoError(t, f.c.Get(t.Context(), client.ObjectKeyFromObject(f.set), set))
					set.Spec.Replicas = 0
					set.Spec.PatchID = 0
					require.NoError(t, f.c.Update(t.Context(), set))
				} else {
					f.startCleanup(true)
				}
				result, err := f.reconcileSet()
				require.NoError(t, err)
				require.Equal(t, time.Minute, result.RequeueAfter)
				require.Equal(t, 2, calls)
				f.requirePodKept()
				require.NoError(t, f.c.Get(t.Context(), client.ObjectKeyFromObject(other), other))
				require.True(t, other.DeletionTimestamp.IsZero())
			})
		}
	}
}

func TestRunnerSetStopsBatchOnRateLimit(t *testing.T) {
	for _, mode := range []string{"scale down", "pending cleanup", "running cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			runner := f.runner()
			runner.Status.RunnerID = unrecordedTestRunnerID
			if mode == "pending cleanup" {
				runner.Status.Phase = v1alpha1.EphemeralRunnerPhasePending
			}
			require.NoError(t, f.c.Status().Update(t.Context(), runner))
			other := runner.DeepCopy()
			other.Name += "-other"
			other.ResourceVersion = ""
			other.UID = ""
			require.NoError(t, f.c.Create(t.Context(), other))
			require.NoError(t, f.c.Status().Update(t.Context(), other))
			f.reply = &scaleset.RateLimitError{RetryAfter: time.Minute}
			if mode == "scale down" {
				set := new(v1alpha1.EphemeralRunnerSet)
				require.NoError(t, f.c.Get(t.Context(), client.ObjectKeyFromObject(f.set), set))
				set.Spec.Replicas = 0
				set.Spec.PatchID = 0
				require.NoError(t, f.c.Update(t.Context(), set))
			} else {
				f.startCleanup(true)
			}

			result, err := f.reconcileSet()
			require.NoError(t, err)
			require.Equal(t, time.Minute, result.RequeueAfter)
			require.Len(t, f.removals, 1, "a throttled batch must stop issuing service calls")
			f.requirePodKept()
			require.NoError(t, f.c.Get(t.Context(), client.ObjectKeyFromObject(other), other))
			require.True(t, other.DeletionTimestamp.IsZero())
		})
	}
}

func TestAutoscalingRunnerSetRequeuesRateLimit(t *testing.T) {
	for _, operation := range []string{"lookup group", "lookup set", "create", "update group", "update name", "delete", "verify existing"} {
		t.Run(operation, func(t *testing.T) {
			limited := &scaleset.RateLimitError{RetryAfter: time.Minute}
			set := &v1alpha1.AutoscalingRunnerSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "set", Namespace: "default",
					Finalizers: []string{autoscalingRunnerSetFinalizerName},
					Labels:     map[string]string{LabelKeyKubernetesVersion: build.Version},
				},
				Spec: v1alpha1.AutoscalingRunnerSetSpec{
					RunnerScaleSetName: "set", GitHubConfigUrl: "https://github.com/owner/repo",
				},
			}
			var options []scalefake.ClientOption
			switch operation {
			case "lookup group":
				set.Spec.RunnerGroup = "group"
				options = append(options, scalefake.WithGetRunnerGroupByName(nil, limited))
			case "lookup set":
				options = append(options, scalefake.WithGetRunnerScaleSet(nil, limited))
			case "create":
				options = append(options, scalefake.WithCreateRunnerScaleSet(nil, limited))
			default:
				set.Annotations = map[string]string{
					runnerScaleSetIDAnnotationKey: "1", AnnotationKeyGitHubRunnerScaleSetName: "set",
					AnnotationKeyGitHubRunnerGroupName: "Default",
				}
				switch operation {
				case "update group":
					set.Spec.RunnerGroup = "other"
					options = append(options, scalefake.WithGetRunnerGroupByName(&scaleset.RunnerGroup{ID: 2}, nil),
						scalefake.WithUpdateRunnerScaleSet(nil, limited))
				case "update name":
					set.Spec.RunnerScaleSetName = "other"
					options = append(options, scalefake.WithUpdateRunnerScaleSet(nil, limited))
				case "delete":
					now := metav1.Now()
					set.DeletionTimestamp = &now
					options = append(options, scalefake.WithDeleteRunnerScaleSet(limited))
				case "verify existing":
					options = append(options, scalefake.WithGetRunnerScaleSetByID(nil, limited))
				}
			}
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, v1alpha1.AddToScheme(scheme))
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
				WithStatusSubresource(&v1alpha1.AutoscalingRunnerSet{}).
				WithIndex(&v1alpha1.AutoscalingListener{}, autoscalingRunnerSetOwnerKey, autoscalingListenerRunnerSetIndexer).
				Build()
			reconciler := &AutoscalingRunnerSetReconciler{
				Client: c, Scheme: scheme, Log: logr.Discard(),
				ResourceBuilder: ResourceBuilder{
					Scheme: scheme, ResourceCache: newTestResourceCache(),
					SecretResolver: &stubSecretResolver{client: scalefake.NewClient(options...)},
				},
			}
			if operation == "verify existing" {
				desired, err := reconciler.newEphemeralRunnerSet(set)
				require.NoError(t, err)
				require.NoError(t, c.Create(t.Context(), desired))
			}
			result, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
			require.NoError(t, err)
			require.Equal(t, time.Minute, result.RequeueAfter)
		})
	}
}
