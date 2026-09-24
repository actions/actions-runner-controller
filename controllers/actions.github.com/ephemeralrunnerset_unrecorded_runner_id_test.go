package actionsgithubcom

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// The runner controller records the runner ID only after it has created the
// pod, and a job only once the listener reports it. These tests drive the real
// set and runner reconcilers over a fake API server into that window: the pod
// exists, the jitconfig secret holds registration 7, and the EphemeralRunner
// still says 0. The pod phase and every Actions service reply are modeled.

const unrecordedTestRunnerID = 7

var errUnrecordedTestJobStillRunning = fmt.Errorf("%w: %w", scaleset.ConflictError, scaleset.JobStillRunningError)

type unrecordedRunnerIDFixture struct {
	t                *testing.T
	c                client.Client
	set              *v1alpha1.EphemeralRunnerSet
	setController    *EphemeralRunnerSetReconciler
	runnerController *EphemeralRunnerReconciler
	queue            *RunnerUnregistrationQueue
	runnerKey        types.NamespacedName

	// reply is what the service answers for registration 7. Any other ID is
	// unknown to it and answers NotFound, which is what cleanup used to take
	// as a removal when it asked about ID 0.
	reply    error
	removals []int64
}

// newUnrecordedRunnerIDFixture leaves a runner created age ago in the window,
// with its pod running and registration 7 executing a job.
func newUnrecordedRunnerIDFixture(t *testing.T, age time.Duration) *unrecordedRunnerIDFixture {
	previous := unrecordedRunnerIDGracePeriod
	unrecordedRunnerIDGracePeriod = time.Minute
	t.Cleanup(func() { unrecordedRunnerIDGracePeriod = previous })

	f := &unrecordedRunnerIDFixture{t: t, reply: errUnrecordedTestJobStillRunning}
	ctx := t.Context()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))

	f.set = &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "unrecorded-id",
			Namespace:  "default",
			UID:        "unrecorded-id-set",
			Finalizers: []string{EphemeralRunnerSetFinalizerName},
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: 1,
			PatchID:  1,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    "https://github.com/owner/repo",
				GitHubConfigSecret: "github-config",
				RunnerScaleSetID:   1,
				PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  v1alpha1.EphemeralRunnerContainerName,
						Image: "ghcr.io/actions/actions-runner:latest",
					}},
				}},
			},
		},
	}
	configSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-config", Namespace: f.set.Namespace},
		Data:       map[string][]byte{"github_token": []byte("token")},
	}

	f.c = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(f.set, configSecret).
		WithStatusSubresource(&v1alpha1.EphemeralRunnerSet{}, &v1alpha1.EphemeralRunner{}, &corev1.Pod{}).
		WithIndex(&v1alpha1.EphemeralRunner{}, resourceOwnerKey, newGroupVersionOwnerKindIndexer("EphemeralRunnerSet")).
		WithInterceptorFuncs(interceptor.Funcs{
			// Stamped by the API server, which the fake does not do.
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*v1alpha1.EphemeralRunner); ok {
					obj.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-age)))
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	registration := &scaleset.RunnerReference{ID: unrecordedTestRunnerID, RunnerScaleSetID: 1}
	service := scalefake.NewClient(
		scalefake.WithGenerateJitRunnerConfig(&scaleset.RunnerScaleSetJitRunnerConfig{
			Runner:           registration,
			EncodedJITConfig: "jit",
		}, nil),
		scalefake.WithRemoveRunnerFunc(func(_ context.Context, id int64) error {
			f.removals = append(f.removals, id)
			if id != unrecordedTestRunnerID {
				return fmt.Errorf("%w: runner %d", scaleset.NotFoundError, id)
			}
			return f.reply
		}),
	)
	resolver := secretresolver.New(f.c, scalefake.NewMultiClient(scalefake.WithClient(service)))
	cache := NewResourceCache()
	resourceBuilder := ResourceBuilder{Scheme: scheme, ResourceCache: &cache, SecretResolver: resolver}

	f.setController = &EphemeralRunnerSetReconciler{
		Client:          f.c,
		APIReader:       f.c,
		Scheme:          scheme,
		Log:             logr.Discard(),
		ResourceBuilder: resourceBuilder,
	}
	// Workers are not started, so whatever is queued stays observable.
	f.queue = NewRunnerUnregistrationQueue(logr.Discard(), resolver, 1)
	f.runnerController = &EphemeralRunnerReconciler{
		Client:              f.c,
		Scheme:              scheme,
		Log:                 logr.Discard(),
		ResourceBuilder:     resourceBuilder,
		UnregistrationQueue: f.queue,
	}

	_, err := f.reconcileSet()
	require.NoError(t, err)
	var runners v1alpha1.EphemeralRunnerList
	require.NoError(t, f.c.List(ctx, &runners, client.InNamespace(f.set.Namespace)))
	require.Len(t, runners.Items, 1)
	f.runnerKey = client.ObjectKeyFromObject(&runners.Items[0])
	registration.Name = f.runnerKey.Name

	// Registers the runner and creates the pod, and returns before recording
	// the ID.
	_, err = f.reconcileRunner()
	require.NoError(t, err)

	secret := new(corev1.Secret)
	require.NoError(t, f.c.Get(ctx, f.runnerKey, secret))
	require.Equal(t, strconv.Itoa(unrecordedTestRunnerID), string(secret.Data["runnerId"]))

	pod := f.pod()
	require.NotNil(t, pod)
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  v1alpha1.EphemeralRunnerContainerName,
		Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}
	require.NoError(t, f.c.Status().Update(ctx, pod))

	runner := f.runner()
	require.NotNil(t, runner)
	require.Zero(t, runner.Status.RunnerID)
	require.False(t, runner.HasJob())
	require.Empty(t, f.removals)

	return f
}

func (f *unrecordedRunnerIDFixture) reconcileSet() (ctrl.Result, error) {
	return f.setController.Reconcile(f.t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(f.set)})
}

func (f *unrecordedRunnerIDFixture) reconcileRunner() (ctrl.Result, error) {
	return f.runnerController.Reconcile(f.t.Context(), ctrl.Request{NamespacedName: f.runnerKey})
}

func (f *unrecordedRunnerIDFixture) runner() *v1alpha1.EphemeralRunner {
	runner := new(v1alpha1.EphemeralRunner)
	if err := f.c.Get(f.t.Context(), f.runnerKey, runner); err != nil {
		require.True(f.t, kerrors.IsNotFound(err), err)
		return nil
	}
	return runner
}

func (f *unrecordedRunnerIDFixture) pod() *corev1.Pod {
	pod := new(corev1.Pod)
	if err := f.c.Get(f.t.Context(), f.runnerKey, pod); err != nil {
		require.True(f.t, kerrors.IsNotFound(err), err)
		return nil
	}
	return pod
}

func (f *unrecordedRunnerIDFixture) appliedActionableRevision() int64 {
	set := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(f.t, f.c.Get(f.t.Context(), client.ObjectKeyFromObject(f.set), set))
	return set.Status.AppliedActionableRevision
}

func (f *unrecordedRunnerIDFixture) requirePodKept() {
	pod := f.pod()
	require.NotNil(f.t, pod, "the pod of a runner executing a job was deleted")
	require.True(f.t, pod.DeletionTimestamp.IsZero(), "the pod of a runner executing a job is being deleted")
}

// startCleanup brings the set into cleanup by deleting it, or by updating the
// runner spec.
func (f *unrecordedRunnerIDFixture) startCleanup(deleteSet bool) {
	ctx := f.t.Context()
	set := new(v1alpha1.EphemeralRunnerSet)
	require.NoError(f.t, f.c.Get(ctx, client.ObjectKeyFromObject(f.set), set))
	if deleteSet {
		require.NoError(f.t, f.c.Delete(ctx, set))
		return
	}
	set.Spec.EphemeralRunnerSpec.Spec.Containers[0].Image = "ghcr.io/actions/actions-runner:new"
	set.Spec.ActionableRevision++
	require.NoError(f.t, f.c.Update(ctx, set))
}

var unrecordedRunnerIDCleanups = []struct {
	name      string
	deleteSet bool
}{
	{name: "set deletion", deleteSet: true},
	{name: "spec update", deleteSet: false},
}

func TestSetCleanupWaitsForRunnerToRecordItsID(t *testing.T) {
	for _, cleanup := range unrecordedRunnerIDCleanups {
		t.Run(cleanup.name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			f.startCleanup(cleanup.deleteSet)

			result, err := f.reconcileSet()
			require.NoError(t, err)

			require.Empty(t, f.removals, "a runner without a recorded ID must not be asked about")
			runner := f.runner()
			require.NotNil(t, runner)
			require.True(t, runner.DeletionTimestamp.IsZero(), "the runner was deleted before its ID was recorded")
			f.requirePodKept()
			if !cleanup.deleteSet {
				require.Zero(t, f.appliedActionableRevision(), "the new spec was marked applied over a runner still on the old one")
			}
			require.Positive(t, result.RequeueAfter)
			require.LessOrEqual(t, result.RequeueAfter, unrecordedRunnerIDGracePeriod)

			// Recording the ID updates the runner, which reconciles the set again.
			_, err = f.reconcileRunner()
			require.NoError(t, err)
			require.Equal(t, unrecordedTestRunnerID, f.runner().Status.RunnerID)

			result, err = f.reconcileSet()
			require.NoError(t, err)
			require.Zero(t, result.RequeueAfter)

			require.Equal(t, []int64{unrecordedTestRunnerID}, f.removals)
			runner = f.runner()
			require.NotNil(t, runner)
			require.True(t, runner.DeletionTimestamp.IsZero(), "a runner executing a job was deleted")
			f.requirePodKept()
			if !cleanup.deleteSet {
				require.Equal(t, int64(1), f.appliedActionableRevision())
			}
		})
	}
}

func TestSetCleanupDeletesRunnerThatNeverRecordsItsID(t *testing.T) {
	for _, cleanup := range unrecordedRunnerIDCleanups {
		t.Run(cleanup.name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 2*time.Minute)
			f.startCleanup(cleanup.deleteSet)

			result, err := f.reconcileSet()
			require.NoError(t, err)
			require.Zero(t, result.RequeueAfter)

			require.Empty(t, f.removals, "a runner without a recorded ID must not be asked about")
			runner := f.runner()
			require.NotNil(t, runner)
			require.False(t, runner.DeletionTimestamp.IsZero(), "a runner that never recorded its ID must not hold up cleanup")
			require.Contains(t, runner.Finalizers, ephemeralRunnerActionsFinalizerName)
			if !cleanup.deleteSet {
				require.Equal(t, int64(1), f.appliedActionableRevision())
			}

			// Finalizing asks about the registration in the jitconfig secret
			// before the live pod goes.
			result, err = f.reconcileRunner()
			require.NoError(t, err)
			require.Equal(t, busyRunnerRequeueInterval, result.RequeueAfter)
			require.Equal(t, []int64{unrecordedTestRunnerID}, f.removals)
			require.NotNil(t, f.runner())
			f.requirePodKept()
			require.Empty(t, f.queue.queued())

			f.reply = fmt.Errorf("%w: service unavailable", scaleset.BadRequestError)
			_, err = f.reconcileRunner()
			require.ErrorIs(t, err, scaleset.BadRequestError)
			require.NotNil(t, f.runner())
			f.requirePodKept()

			// The job finished and the service let go of the runner.
			f.reply = nil
			_, err = f.reconcileRunner()
			require.NoError(t, err)
			require.Equal(t, []int64{unrecordedTestRunnerID, unrecordedTestRunnerID, unrecordedTestRunnerID}, f.removals)
			require.Nil(t, f.pod())
			require.Nil(t, f.runner())
			require.Empty(t, f.queue.queued(), "the registration was already removed")
		})
	}
}
