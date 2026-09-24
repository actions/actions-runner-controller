package actionsgithubcom

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileValidatesJITIdentityBeforePublication(t *testing.T) {
	for _, podState := range []string{"absent", "live", "live but missing from cache"} {
		t.Run(podState, func(t *testing.T) {
			for _, value := range []string{"", "not-an-id", "0", "-1", "99999999999999999999999999"} {
				t.Run("id="+value, func(t *testing.T) {
					f := newUnrecordedRunnerIDFixture(t, 0)
					if podState == "absent" {
						require.NoError(t, f.c.Delete(t.Context(), f.pod()))
					}
					if podState == "live but missing from cache" {
						f.runnerController.Client = interceptor.NewClient(f.c, interceptor.Funcs{
							Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
								if _, ok := obj.(*corev1.Pod); ok {
									return kerrors.NewNotFound(corev1.Resource("pods"), key.Name)
								}
								return c.Get(ctx, key, obj, opts...)
							},
						})
					}
					secret := new(corev1.Secret)
					require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))
					secret.Data["runnerId"] = []byte(value)
					require.NoError(t, f.c.Update(t.Context(), secret))

					result, err := f.reconcileRunner()
					if podState == "absent" {
						require.NoError(t, err)
						require.Equal(t, 500*time.Millisecond, result.RequeueAfter)
						require.Nil(t, f.pod(), "invalid identity must not reach a new pod")
						require.True(t, kerrors.IsNotFound(f.c.Get(t.Context(), f.runnerKey, new(corev1.Secret))))
					} else {
						require.ErrorContains(t, err, "invalid runner ID")
						f.requirePodKept()
						preserved := new(corev1.Secret)
						require.NoError(t, f.c.Get(t.Context(), f.runnerKey, preserved))
						require.Equal(t, value, string(preserved.Data["runnerId"]))
					}
					require.Zero(t, f.runner().Status.RunnerID, "invalid identity must not become sticky in status")
					require.Empty(t, f.removals)
					require.Empty(t, f.queue.queued())

					secret.Data["runnerId"] = []byte("7")
					if podState == "absent" {
						secret.ResourceVersion = ""
						require.NoError(t, f.c.Create(t.Context(), secret))
					} else {
						require.NoError(t, f.c.Update(t.Context(), secret))
					}
					f.runnerController.Client = f.c
					if podState == "absent" {
						_, err = f.reconcileRunner()
						require.NoError(t, err)
						require.NotNil(t, f.pod())
						require.Zero(t, f.runner().Status.RunnerID)
					}
					_, err = f.reconcileRunner()
					require.NoError(t, err)
					require.Equal(t, unrecordedTestRunnerID, f.runner().Status.RunnerID)
				})
			}
		})
	}
}

func TestRunnerFinalizerReusesActionsClientRecoveredByName(t *testing.T) {
	f := newUnrecordedRunnerIDFixture(t, 0)
	secret := new(corev1.Secret)
	require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))
	require.NoError(t, f.c.Delete(t.Context(), secret))
	require.NoError(t, f.c.Delete(t.Context(), f.runner()))

	for _, reply := range []error{errUnrecordedTestJobStillRunning, nil} {
		service := scalefake.NewClient(
			scalefake.WithGetRunnerByName(&scaleset.RunnerReference{
				ID: unrecordedTestRunnerID, RunnerScaleSetID: 1, Name: f.runnerKey.Name,
			}, nil),
			scalefake.WithRemoveRunnerFunc(func(_ context.Context, id int64) error {
				require.Equal(t, int64(unrecordedTestRunnerID), id)
				f.removals = append(f.removals, id)
				return reply
			}),
		)
		resolver := NewMockSecretResolver(t)
		resolver.EXPECT().GetActionsService(mock.Anything, mock.Anything).Return(service, nil).Once()
		f.runnerController.SecretResolver = resolver

		result, err := f.reconcileRunner()
		require.NoError(t, err)
		resolver.AssertExpectations(t)
		require.Empty(t, f.queue.queued())
		if reply != nil {
			require.Equal(t, busyRunnerRequeueInterval, result.RequeueAfter)
			f.requirePodKept()
			require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
		} else {
			require.Zero(t, result.RequeueAfter)
			require.Nil(t, f.pod())
			require.Nil(t, f.runner())
		}
	}
	require.Equal(t, []int64{unrecordedTestRunnerID, unrecordedTestRunnerID}, f.removals)
}

func TestReconcilePreservesInvalidJITSecretOnPodReadError(t *testing.T) {
	for _, configured := range []bool{true, false} {
		name := "reader not configured"
		if configured {
			name = "pod read failed"
		}
		t.Run(name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			secret := new(corev1.Secret)
			require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))
			secret.Data["runnerId"] = []byte("-1")
			require.NoError(t, f.c.Update(t.Context(), secret))
			readErr := kerrors.NewServiceUnavailable("pod state is unavailable")
			if configured {
				f.runnerController.APIReader = interceptor.NewClient(f.c, interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return readErr
					},
				})
			} else {
				f.runnerController.APIReader = nil
			}

			_, err := f.reconcileRunner()
			require.Error(t, err)
			if configured {
				require.ErrorIs(t, err, readErr)
			}
			require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))
			require.Equal(t, "-1", string(secret.Data["runnerId"]))
			require.Zero(t, f.runner().Status.RunnerID)
			f.requirePodKept()
		})
	}
}

func TestRunnerFinalizerDoesNotResolveUnusedActionsClient(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runnerID  int
		succeeded bool
	}{
		{name: "self deregistered", succeeded: true},
		{name: "terminated pod with recorded ID", runnerID: unrecordedTestRunnerID},
		{name: "terminated pod with ID in secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			runner := f.runner()
			runner.Status.RunnerID = tc.runnerID
			if tc.succeeded {
				runner.Status.Phase = v1alpha1.EphemeralRunnerPhaseSucceeded
			}
			require.NoError(t, f.c.Status().Update(t.Context(), runner))
			pod := f.pod()
			pod.Status.ContainerStatuses[0].Ready = false
			pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{},
			}
			require.NoError(t, f.c.Status().Update(t.Context(), pod))
			f.runnerController.SecretResolver = NewMockSecretResolver(t)
			require.NoError(t, f.c.Delete(t.Context(), runner))

			_, err := f.reconcileRunner()
			require.NoError(t, err)
			require.Nil(t, f.runner())
			require.Nil(t, f.pod())
			if tc.succeeded {
				require.Empty(t, f.queue.queued())
			} else {
				require.Len(t, f.queue.queued(), 1)
				require.Equal(t, unrecordedTestRunnerID, f.queue.queued()[0].runnerID)
			}
		})
	}
}

func TestRunnerFinalizerDoesNotTrustStalePodCache(t *testing.T) {
	for _, cachedState := range []string{"missing", "terminated"} {
		t.Run(cachedState, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			stalePod := f.pod()
			stalePod.Status.ContainerStatuses[0].State = corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
			}
			f.runnerController.Client = interceptor.NewClient(f.c, interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if pod, ok := obj.(*corev1.Pod); ok {
						if cachedState == "missing" {
							return kerrors.NewNotFound(corev1.Resource("pods"), key.Name)
						}
						stalePod.DeepCopyInto(pod)
						return nil
					}
					return c.Get(ctx, key, obj, opts...)
				},
			})
			require.NoError(t, f.c.Delete(t.Context(), f.runner()))

			result, err := f.reconcileRunner()
			require.NoError(t, err)
			require.Equal(t, busyRunnerRequeueInterval, result.RequeueAfter)
			require.Equal(t, []int64{unrecordedTestRunnerID}, f.removals)
			require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
			require.Empty(t, f.queue.queued())
			f.requirePodKept()
		})
	}
}

func TestRunnerFinalizerPreservesPodWhenAuthoritativeReadFails(t *testing.T) {
	for _, configured := range []bool{true, false} {
		name := "reader not configured"
		if configured {
			name = "API read failed"
		}
		t.Run(name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			readErr := kerrors.NewServiceUnavailable("pod state is unavailable")
			if configured {
				f.runnerController.APIReader = interceptor.NewClient(f.c, interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return readErr
					},
				})
			} else {
				f.runnerController.APIReader = nil
			}
			require.NoError(t, f.c.Delete(t.Context(), f.runner()))

			_, err := f.reconcileRunner()
			require.Error(t, err)
			if configured {
				require.ErrorIs(t, err, readErr)
			}
			require.Empty(t, f.removals)
			require.Empty(t, f.queue.queued())
			require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
			f.requirePodKept()
		})
	}
}

func TestRunnerFinalizerPreservesPodWithInvalidJITIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value []byte
	}{
		{name: "missing"},
		{name: "empty", value: []byte("")},
		{name: "malformed", value: []byte("not-an-id")},
		{name: "zero", value: []byte("0")},
		{name: "negative", value: []byte("-1")},
		{name: "overflow", value: []byte("99999999999999999999999999")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 2*time.Minute)
			secret := new(corev1.Secret)
			require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))
			if tc.value == nil {
				delete(secret.Data, "runnerId")
			} else {
				secret.Data["runnerId"] = tc.value
			}
			require.NoError(t, f.c.Update(t.Context(), secret))
			f.startCleanup(true)
			_, err := f.reconcileSet()
			require.NoError(t, err)

			_, err = f.reconcileRunner()
			require.Error(t, err)
			require.Empty(t, f.removals)
			require.Empty(t, f.queue.queued())
			f.requirePodKept()
			runner := f.runner()
			require.NotNil(t, runner)
			require.Contains(t, runner.Finalizers, ephemeralRunnerFinalizerName)
			require.Contains(t, runner.Finalizers, ephemeralRunnerActionsFinalizerName)
			require.NoError(t, f.c.Get(t.Context(), f.runnerKey, secret))

			// Repairing the metadata restores the normal busy-runner guard.
			secret.Data["runnerId"] = []byte("7")
			require.NoError(t, f.c.Update(t.Context(), secret))
			result, err := f.reconcileRunner()
			require.NoError(t, err)
			require.Equal(t, busyRunnerRequeueInterval, result.RequeueAfter)
			require.Equal(t, []int64{unrecordedTestRunnerID}, f.removals)
			f.requirePodKept()
		})
	}
}

func TestCleanupRejectsNegativeStatusRunnerID(t *testing.T) {
	for _, cleanup := range unrecordedRunnerIDCleanups {
		t.Run(cleanup.name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			runner := f.runner()
			runner.Status.RunnerID = -1
			require.NoError(t, f.c.Status().Update(t.Context(), runner))
			f.startCleanup(cleanup.deleteSet)

			_, err := f.reconcileSet()
			require.Error(t, err)
			require.Empty(t, f.removals)
			runner = f.runner()
			require.NotNil(t, runner)
			require.True(t, runner.DeletionTimestamp.IsZero())
			require.Contains(t, runner.Finalizers, ephemeralRunnerActionsFinalizerName)
			f.requirePodKept()
			if !cleanup.deleteSet {
				require.Zero(t, f.appliedActionableRevision())
			}

			require.NoError(t, f.c.Delete(t.Context(), runner))
			_, err = f.reconcileRunner()
			require.Error(t, err)
			require.Empty(t, f.removals)
			require.Empty(t, f.queue.queued())
			require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
			f.requirePodKept()
		})
	}
}

func TestSetCleanupDoesNotResolveUnusedActionsClient(t *testing.T) {
	for _, cleanup := range unrecordedRunnerIDCleanups {
		t.Run(cleanup.name, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				age        time.Duration
				registered bool
				deleting   bool
				waiting    bool
			}{
				{name: "waiting for ID", waiting: true},
				{name: "ID grace period expired", age: 2 * time.Minute, deleting: true},
				{name: "registered runner with a job", registered: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := newUnrecordedRunnerIDFixture(t, tc.age)
					if tc.registered {
						runner := f.runner()
						runner.Status.RunnerID = unrecordedTestRunnerID
						runner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
						runner.Status.JobID = "job-1"
						require.NoError(t, f.c.Status().Update(t.Context(), runner))
					}
					f.setController.SecretResolver = &stubSecretResolver{err: errors.New("Actions configuration is unavailable")}
					f.startCleanup(cleanup.deleteSet)

					result, err := f.reconcileSet()
					require.NoError(t, err)
					require.Empty(t, f.removals)
					runner := f.runner()
					require.NotNil(t, runner)
					require.Equal(t, tc.deleting, !runner.DeletionTimestamp.IsZero())
					require.Contains(t, runner.Finalizers, ephemeralRunnerActionsFinalizerName)
					require.Equal(t, tc.waiting, result.RequeueAfter > 0)
					f.requirePodKept()
					if !cleanup.deleteSet {
						if tc.waiting {
							require.Zero(t, f.appliedActionableRevision())
						} else {
							require.Equal(t, int64(1), f.appliedActionableRevision())
						}
					}
				})
			}
		})
	}
}

func TestSetCleanupContinuesLocalDeletionWhenActionsClientFails(t *testing.T) {
	f := newUnrecordedRunnerIDFixture(t, 2*time.Minute)
	var registeredRunners []*v1alpha1.EphemeralRunner
	for _, name := range []string{"registered-a", "registered-b"} {
		runner := f.runner().DeepCopy()
		runner.Name = name
		runner.ResourceVersion = ""
		runner.UID = ""
		runner.Status.RunnerID = 8 + len(registeredRunners)
		require.NoError(t, f.c.Create(t.Context(), runner))
		registeredRunners = append(registeredRunners, runner)
	}
	configErr := errors.New("Actions configuration is unavailable")
	resolver := NewMockSecretResolver(t)
	resolver.EXPECT().GetActionsService(mock.Anything, mock.Anything).Return(nil, configErr).Once()
	f.setController.SecretResolver = resolver
	f.startCleanup(false)

	_, err := f.reconcileSet()
	require.ErrorIs(t, err, configErr)
	require.Empty(t, f.removals)
	require.False(t, f.runner().DeletionTimestamp.IsZero(), "a client error must not block local zero-ID deletion")
	require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
	f.requirePodKept()
	require.Zero(t, f.appliedActionableRevision(), "registered runners have not been cleaned up")
	for _, runner := range registeredRunners {
		require.NoError(t, f.c.Get(t.Context(), client.ObjectKeyFromObject(runner), runner))
		require.True(t, runner.DeletionTimestamp.IsZero())
	}
}

func TestRunnerFinalizerHandlesServiceRemovalResponses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reply   error
		wantErr bool
		busy    bool
	}{
		{name: "removed"},
		{name: "not found", reply: scaleset.NotFoundError},
		{name: "runner not found", reply: scaleset.RunnerNotFoundError},
		{name: "busy", reply: errUnrecordedTestJobStillRunning, busy: true},
		{name: "API error", reply: scaleset.BadRequestError, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			f.reply = tc.reply
			require.NoError(t, f.c.Delete(t.Context(), f.runner()))

			result, err := f.reconcileRunner()
			if tc.wantErr {
				require.ErrorIs(t, err, tc.reply)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, []int64{unrecordedTestRunnerID}, f.removals)
			require.Empty(t, f.queue.queued())
			if tc.busy {
				require.Equal(t, busyRunnerRequeueInterval, result.RequeueAfter)
			} else {
				require.Zero(t, result.RequeueAfter)
			}
			if tc.wantErr || tc.busy {
				f.requirePodKept()
				require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)
			} else {
				require.Nil(t, f.pod())
				require.Nil(t, f.runner())
			}
		})
	}
}

func TestSetCleanupDoesNotDelayTerminalZeroIDRunners(t *testing.T) {
	for _, phase := range []v1alpha1.EphemeralRunnerPhase{
		v1alpha1.EphemeralRunnerPhaseSucceeded,
		v1alpha1.EphemeralRunnerPhaseFailed,
		v1alpha1.EphemeralRunnerPhaseOutdated,
	} {
		t.Run(string(phase), func(t *testing.T) {
			f := newUnrecordedRunnerIDFixture(t, 0)
			runner := f.runner()
			runner.Status.Phase = phase
			require.NoError(t, f.c.Status().Update(t.Context(), runner))
			pod := f.pod()
			exitCode := int32(1)
			switch phase {
			case v1alpha1.EphemeralRunnerPhaseSucceeded:
				exitCode = 0
			case v1alpha1.EphemeralRunnerPhaseOutdated:
				exitCode = 7
			}
			pod.Status.ContainerStatuses[0].Ready = false
			pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode},
			}
			require.NoError(t, f.c.Status().Update(t.Context(), pod))
			f.startCleanup(true)

			result, err := f.reconcileSet()
			require.NoError(t, err)
			require.Zero(t, result.RequeueAfter)
			require.False(t, f.runner().DeletionTimestamp.IsZero())
			require.Contains(t, f.runner().Finalizers, ephemeralRunnerActionsFinalizerName)

			_, err = f.reconcileRunner()
			require.NoError(t, err)
			require.Nil(t, f.runner())
			require.Nil(t, f.pod())
			require.Empty(t, f.removals)
			if phase == v1alpha1.EphemeralRunnerPhaseSucceeded {
				require.Empty(t, f.queue.queued())
			} else {
				require.Len(t, f.queue.queued(), 1)
				require.Equal(t, unrecordedTestRunnerID, f.queue.queued()[0].runnerID)
			}
		})
	}
}
