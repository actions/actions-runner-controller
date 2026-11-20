package actionsgithubcom

import (
	"context"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCacheRestoreTimeout(t *testing.T) {
	assert.Equal(t, DefaultCacheRestoreTimeout, cacheRestoreTimeout(nil))
	assert.Equal(t, DefaultCacheRestoreTimeout, cacheRestoreTimeout(&v1alpha1.CacheVolume{}))
	assert.Equal(t, time.Minute, cacheRestoreTimeout(&v1alpha1.CacheVolume{RestoreTimeout: &metav1.Duration{Duration: time.Minute}}))
	assert.Zero(t, cacheRestoreTimeout(&v1alpha1.CacheVolume{RestoreTimeout: &metav1.Duration{}}), "zero waits forever")
	assert.Zero(t, cacheRestoreTimeout(&v1alpha1.CacheVolume{RestoreTimeout: &metav1.Duration{Duration: -time.Second}}))
}

func TestEnsureCacheClaimAfterAFailedRestore(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc-cache"}

	t.Run("starts empty when the runner gave up restoring", func(t *testing.T) {
		snap := cacheTestSnapshot("s", true, time.Now().Add(-time.Hour))
		c := cacheTestClient(t, true, &snap)
		runner := cacheTestRunner("")
		runner.Annotations = map[string]string{AnnotationKeyCacheRestoreSkipped: "restore timed out"}

		name, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, runner, logr.Discard())
		require.NoError(t, err)
		assert.Equal(t, key.Name, name)

		var claim corev1.PersistentVolumeClaim
		require.NoError(t, c.Get(ctx, key, &claim))
		assert.Nil(t, claim.Spec.DataSource, "a snapshot that cannot be restored must not be tried again")
	})

	t.Run("waits while the previous claim is still being deleted", func(t *testing.T) {
		terminating := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: key.Name, Namespace: key.Namespace,
			DeletionTimestamp: ptr.To(metav1.Now()), Finalizers: []string{"kubernetes.io/pvc-protection"},
		}}
		c := cacheTestClient(t, true, terminating)
		name, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.ErrorIs(t, err, errCacheClaimTerminating)
		assert.Empty(t, name, "a pod must never be attached to a claim that is going away")
	})
}

func TestEnforceCacheRestoreTimeout(t *testing.T) {
	ctx := context.Background()
	claimKey := types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc-cache"}
	podKey := types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc"}

	pendingPod := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: podKey.Name, Namespace: podKey.Namespace},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		}
	}
	// restoring is a claim that was created from a snapshot, that is not bound.
	restoring := func(mutate ...func(*corev1.PersistentVolumeClaim)) *corev1.PersistentVolumeClaim {
		claim := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: claimKey.Name, Namespace: claimKey.Namespace,
				Annotations: map[string]string{"volume.kubernetes.io/storage-provisioner": "csi.example.com"},
			},
			Spec: corev1.PersistentVolumeClaimSpec{DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: ptr.To("snapshot.storage.k8s.io"), Kind: "VolumeSnapshot", Name: "s",
			}},
			Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
		}
		for _, m := range mutate {
			m(claim)
		}
		return claim
	}
	runner := func(annotations map[string]string, mutate ...func(*v1alpha1.EphemeralRunner)) *v1alpha1.EphemeralRunner {
		r := cacheTestRunner("")
		r.Annotations = annotations
		for _, m := range mutate {
			m(r)
		}
		return r
	}
	setup := func(t *testing.T, r *v1alpha1.EphemeralRunner, objs ...client.Object) (client.Client, *EphemeralRunnerReconciler, *events.FakeRecorder) {
		t.Helper()
		c := cacheTestClient(t, true, append(objs, r)...)
		rec := events.NewFakeRecorder(10)
		rc := newCacheTestReconciler(c)
		rc.Recorder = rec
		return c, rc, rec
	}
	started := func(ago time.Duration) map[string]string {
		return map[string]string{AnnotationKeyCacheRestoreWaitStartedAt: time.Now().Add(-ago).UTC().Format(time.RFC3339)}
	}

	t.Run("does nothing for runners that are not waiting for a restored volume", func(t *testing.T) {
		for name, tc := range map[string]struct {
			runner *v1alpha1.EphemeralRunner
			pod    *corev1.Pod
			claim  *corev1.PersistentVolumeClaim
		}{
			"no cache volume": {runner(started(time.Hour), func(r *v1alpha1.EphemeralRunner) { r.Spec.CacheVolume = nil }), pendingPod(), restoring()},
			"timeout disabled": {runner(started(time.Hour), func(r *v1alpha1.EphemeralRunner) {
				r.Spec.CacheVolume.RestoreTimeout = &metav1.Duration{}
			}), pendingPod(), restoring()},
			"pod is running": {runner(started(time.Hour)), func() *corev1.Pod { p := pendingPod(); p.Status.Phase = corev1.PodRunning; return p }(), restoring()},
			"claim is bound": {runner(started(time.Hour)), pendingPod(), restoring(func(c *corev1.PersistentVolumeClaim) { c.Status.Phase = corev1.ClaimBound })},
			"claim is empty, there is nothing to restore": {runner(started(time.Hour)), pendingPod(), restoring(func(c *corev1.PersistentVolumeClaim) { c.Spec.DataSource = nil })},
			"already fell back":                           {runner(map[string]string{AnnotationKeyCacheRestoreSkipped: "x"}), pendingPod(), restoring()},
		} {
			c, rc, _ := setup(t, tc.runner, tc.pod, tc.claim)
			again, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, tc.runner, tc.pod, logr.Discard())
			require.NoError(t, err, name)
			assert.False(t, fellBack, name)
			assert.Zero(t, again, name)
			require.NoError(t, c.Get(ctx, claimKey, new(corev1.PersistentVolumeClaim)), name+": the claim is left alone")
		}
	})

	t.Run("does not count the time before provisioning started", func(t *testing.T) {
		noProvisioner := restoring(func(c *corev1.PersistentVolumeClaim) { c.Annotations = nil })
		r := runner(nil)
		c, rc, _ := setup(t, r, pendingPod(), noProvisioner)
		again, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, r, pendingPod(), logr.Discard())
		require.NoError(t, err)
		assert.False(t, fellBack, "a pod waiting for capacity is not a failed restore")
		assert.Equal(t, cacheRestorePollInterval, again)

		var got v1alpha1.EphemeralRunner
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(r), &got))
		assert.Empty(t, got.Annotations[AnnotationKeyCacheRestoreWaitStartedAt], "the clock has not started")
	})

	t.Run("starts the clock when provisioning is first seen", func(t *testing.T) {
		r := runner(nil)
		c, rc, _ := setup(t, r, pendingPod(), restoring())
		again, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, r, pendingPod(), logr.Discard())
		require.NoError(t, err)
		assert.False(t, fellBack)
		assert.Equal(t, cacheRestorePollInterval, again)

		var got v1alpha1.EphemeralRunner
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(r), &got))
		at, ok := annotationTimeOf(got.Annotations[AnnotationKeyCacheRestoreWaitStartedAt])
		require.True(t, ok)
		assert.WithinDuration(t, time.Now(), at, time.Minute)
	})

	t.Run("keeps waiting within the timeout", func(t *testing.T) {
		r := runner(started(time.Minute))
		c, rc, _ := setup(t, r, pendingPod(), restoring())
		again, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, r, pendingPod(), logr.Discard())
		require.NoError(t, err)
		assert.False(t, fellBack)
		assert.Equal(t, cacheRestorePollInterval, again)
		require.NoError(t, c.Get(ctx, claimKey, new(corev1.PersistentVolumeClaim)))
		require.NoError(t, c.Get(ctx, podKey, new(corev1.Pod)))
	})

	t.Run("wakes up in time when the timeout is close", func(t *testing.T) {
		r := runner(started(DefaultCacheRestoreTimeout - 2*time.Second))
		_, rc, _ := setup(t, r, pendingPod(), restoring())
		again, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, r, pendingPod(), logr.Discard())
		require.NoError(t, err)
		assert.False(t, fellBack)
		assert.Greater(t, again, time.Duration(0))
		assert.LessOrEqual(t, again, 2*time.Second)
	})

	t.Run("falls back to an empty volume after the timeout", func(t *testing.T) {
		r := runner(started(DefaultCacheRestoreTimeout + time.Second))
		c, rc, rec := setup(t, r, pendingPod(), restoring())
		again, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, r, pendingPod(), logr.Discard())
		require.NoError(t, err)
		assert.True(t, fellBack)
		assert.Zero(t, again)

		var got v1alpha1.EphemeralRunner
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(r), &got))
		assert.Contains(t, got.Annotations[AnnotationKeyCacheRestoreSkipped], "timed out", "the replacement claim will skip the restore")

		assert.True(t, kerrors.IsNotFound(c.Get(ctx, podKey, new(corev1.Pod))), "the stuck pod is deleted")
		assert.True(t, kerrors.IsNotFound(c.Get(ctx, claimKey, new(corev1.PersistentVolumeClaim))), "the claim that cannot be restored is deleted")

		select {
		case e := <-rec.Events:
			assert.Contains(t, e, "CacheRestoreTimedOut")
		default:
			t.Fatal("expected a warning event")
		}

		// The runner then gets an empty claim, even though a good looking snapshot exists.
		snap := cacheTestSnapshot("s", true, time.Now().Add(-time.Hour))
		require.NoError(t, c.Create(ctx, &snap))
		_, err = rc.ensureCacheClaim(ctx, &got, logr.Discard())
		require.NoError(t, err)
		var fresh corev1.PersistentVolumeClaim
		require.NoError(t, c.Get(ctx, claimKey, &fresh))
		assert.Nil(t, fresh.Spec.DataSource)
	})

	t.Run("a configured timeout is honoured", func(t *testing.T) {
		short := func(r *v1alpha1.EphemeralRunner) {
			r.Spec.CacheVolume.RestoreTimeout = &metav1.Duration{Duration: 10 * time.Second}
		}
		r := runner(started(30*time.Second), short)
		_, rc, _ := setup(t, r, pendingPod(), restoring())
		_, fellBack, err := rc.enforceCacheRestoreTimeout(ctx, r, pendingPod(), logr.Discard())
		require.NoError(t, err)
		assert.True(t, fellBack)
	})
}

func TestCacheMetricsAndEvents(t *testing.T) {
	ctx := context.Background()
	t.Run("restore results are counted when metrics are published", func(t *testing.T) {
		ns := "metrics-restore"
		before := func(result string) float64 {
			return testutil.ToFloat64(counterFor(t, "cache_restores_total", result, ns, "set"))
		}
		c := cacheTestClient(t, true)
		rc := newCacheTestReconciler(c)
		rc.PublishMetrics = true
		runner := cacheTestRunner("")
		runner.Namespace = ns

		cold, hit := before(metrics.CacheRestoreCold), before(metrics.CacheRestoreHit)
		_, err := rc.ensureCacheClaim(ctx, runner, logr.Discard())
		require.NoError(t, err)
		assert.Equal(t, cold+1, before(metrics.CacheRestoreCold))
		assert.Equal(t, hit, before(metrics.CacheRestoreHit))

		// not published when metrics are off
		off := newCacheTestReconciler(cacheTestClient(t, true))
		runner2 := cacheTestRunner("")
		runner2.Namespace = ns
		_, err = off.ensureCacheClaim(ctx, runner2, logr.Discard())
		require.NoError(t, err)
		assert.Equal(t, cold+1, before(metrics.CacheRestoreCold))
	})

	t.Run("a refused commit is counted and reported as an event", func(t *testing.T) {
		c := cacheTestClient(t, true)
		rc := newCacheTestReconciler(c)
		rc.PublishMetrics = true
		rec := events.NewFakeRecorder(5)
		rc.Recorder = rec

		runner := cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded)
		runner.Namespace = "metrics-commit"
		runner.Spec.CacheVolume.CommitPolicy = &v1alpha1.CacheCommitPolicy{Events: []string{"push"}}
		runner.Status.JobEventName = "pull_request"

		got := func() float64 {
			return testutil.ToFloat64(counterFor(t, "cache_commits_total", metrics.CacheCommitDenied, "metrics-commit", "set"))
		}
		before := got()
		_, err := rc.commitCacheVolume(ctx, runner, logr.Discard())
		require.NoError(t, err)
		assert.Equal(t, before+1, got())
		select {
		case e := <-rec.Events:
			assert.Contains(t, e, "CacheNotSaved")
		default:
			t.Fatal("expected an event")
		}
	})

	t.Run("a missing snapshot API is reported", func(t *testing.T) {
		c := cacheTestClient(t, false)
		rc := newCacheTestReconciler(c)
		rec := events.NewFakeRecorder(5)
		rc.Recorder = rec
		name, err := rc.ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)
		assert.Empty(t, name)
		select {
		case e := <-rec.Events:
			assert.Contains(t, e, "CacheUnavailable")
		default:
			t.Fatal("expected an event")
		}
	})

	t.Run("garbage collection reports why snapshots are deleted", func(t *testing.T) {
		now := cacheTestEpoch.Add(100 * time.Hour)
		failed := cacheTestSnapshot("failed", false, cacheTestEpoch)
		failed.Status.Error = &snapshotv1.VolumeSnapshotError{Message: ptr.To("boom")}
		old := cacheTestSnapshot("old", true, cacheTestEpoch.Add(time.Hour))
		old.Annotations = map[string]string{AnnotationKeyCacheSupersededAt: cacheTestEpoch.Format(time.RFC3339)}
		newest := cacheTestSnapshot("newest", true, cacheTestEpoch.Add(2*time.Hour))

		plan := planCacheGC([]snapshotv1.VolumeSnapshot{failed, old, newest}, now, 0, 0)
		assert.Equal(t, map[string]string{"failed": metrics.CacheSnapshotFailed, "old": metrics.CacheSnapshotSuperseded}, plan.reasons)

		plan = planCacheGC([]snapshotv1.VolumeSnapshot{newest}, now, 0, time.Hour)
		assert.Equal(t, map[string]string{"newest": metrics.CacheSnapshotExpired}, plan.reasons)
	})
}

// counterFor returns one series of a cache counter. The collectors are shared by the whole test
// process, so tests compare a series with its value from before the code under test ran.
func counterFor(t *testing.T, name, result, namespace, scaleSet string) prometheus.Counter {
	t.Helper()
	switch name {
	case "cache_restores_total":
		return metrics.CacheRestoreCounter(result, namespace, scaleSet)
	case "cache_commits_total":
		return metrics.CacheCommitCounter(result, namespace, scaleSet)
	}
	t.Fatalf("unknown counter %q", name)
	return nil
}
