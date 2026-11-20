package actionsgithubcom

import (
	"context"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const cacheTestNamespace = "runners"

var cacheTestEpoch = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func cacheTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, snapshotv1.AddToScheme(scheme))
	return scheme
}

// cacheTestClient returns a fake client. The fake client serves no REST mapping
// on its own, so the snapshot API is made available (or not) explicitly.
func cacheTestClient(t *testing.T, snapshotAPI bool, objs ...client.Object) client.Client {
	t.Helper()
	mapper := meta.NewDefaultRESTMapper(nil)
	if snapshotAPI {
		mapper.Add(snapshotv1.SchemeGroupVersion.WithKind("VolumeSnapshot"), meta.RESTScopeNamespace)
	}
	return fake.NewClientBuilder().
		WithScheme(cacheTestScheme(t)).
		WithRESTMapper(mapper).
		WithObjects(objs...).
		Build()
}

func cacheTestVolume() *v1alpha1.CacheVolume {
	return &v1alpha1.CacheVolume{
		StorageClassName:  "fast",
		Size:              resource.MustParse("50Gi"),
		SnapshotClassName: ptr.To("snap"),
	}
}

func cacheTestScaleSet() *v1alpha1.AutoscalingRunnerSet {
	return &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: cacheTestNamespace, UID: "ars-uid"},
		Spec:       v1alpha1.AutoscalingRunnerSetSpec{CacheVolume: cacheTestVolume()},
	}
}

func cacheTestRunner(phase v1alpha1.EphemeralRunnerPhase) *v1alpha1.EphemeralRunner {
	return &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "set-runner-abc",
			Namespace: cacheTestNamespace,
			UID:       "er-uid",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: v1alpha1.GroupVersion.String(),
				Kind:       "EphemeralRunnerSet",
				Name:       "set",
				UID:        "ers-uid",
				Controller: ptr.To(true),
			}},
		},
		Spec: v1alpha1.EphemeralRunnerSpec{CacheVolume: cacheTestVolume()},
		Status: v1alpha1.EphemeralRunnerStatus{
			Phase:             phase,
			JobRepositoryName: "org/repo",
			JobWorkflowRef:    "org/repo/.github/workflows/ci.yaml@refs/heads/main",
			WorkflowRunID:     42,
		},
	}
}

func cacheTestSnapshot(name string, ready bool, cut time.Time) snapshotv1.VolumeSnapshot {
	s := snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         cacheTestNamespace,
			UID:               types.UID("uid-" + name),
			CreationTimestamp: metav1.NewTime(cut),
			Labels:            map[string]string{LabelKeyCacheLineage: cacheLineage(cacheTestNamespace, "set")},
		},
		Status: &snapshotv1.VolumeSnapshotStatus{ReadyToUse: ptr.To(ready)},
	}
	if ready {
		s.Status.CreationTime = ptr.To(metav1.NewTime(cut))
	}
	return s
}

func TestCacheLineage(t *testing.T) {
	a := cacheLineage("ns", "set")
	assert.Equal(t, a, cacheLineage("ns", "set"), "stable")
	assert.NotEqual(t, a, cacheLineage("ns", "other"))
	assert.NotEqual(t, a, cacheLineage("other", "set"))
	assert.Len(t, a, 20, "fits the label value limit regardless of name length")
	assert.Len(t, cacheLineage("ns", string(make([]byte, 300))), 20)
}

func TestNewestReadySnapshot(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		assert.Nil(t, newestReadySnapshot(nil))
		assert.Nil(t, newestReadySnapshot([]snapshotv1.VolumeSnapshot{cacheTestSnapshot("a", false, cacheTestEpoch)}))
	})

	t.Run("newest cut time wins and pending snapshots are ignored", func(t *testing.T) {
		got := newestReadySnapshot([]snapshotv1.VolumeSnapshot{
			cacheTestSnapshot("old", true, cacheTestEpoch),
			cacheTestSnapshot("new", true, cacheTestEpoch.Add(time.Minute)),
			cacheTestSnapshot("pending", false, cacheTestEpoch.Add(time.Hour)),
		})
		require.NotNil(t, got)
		assert.Equal(t, "new", got.Name)
	})

	t.Run("snapshots being deleted are ignored", func(t *testing.T) {
		deleting := cacheTestSnapshot("deleting", true, cacheTestEpoch.Add(time.Hour))
		deleting.DeletionTimestamp = ptr.To(metav1.NewTime(cacheTestEpoch))
		got := newestReadySnapshot([]snapshotv1.VolumeSnapshot{cacheTestSnapshot("ok", true, cacheTestEpoch), deleting})
		require.NotNil(t, got)
		assert.Equal(t, "ok", got.Name)
	})

	t.Run("ties are broken by name", func(t *testing.T) {
		got := newestReadySnapshot([]snapshotv1.VolumeSnapshot{
			cacheTestSnapshot("a", true, cacheTestEpoch),
			cacheTestSnapshot("b", true, cacheTestEpoch),
		})
		require.NotNil(t, got)
		assert.Equal(t, "b", got.Name)
	})
}

func names(snapshots []*snapshotv1.VolumeSnapshot) []string {
	out := make([]string, 0, len(snapshots))
	for _, s := range snapshots {
		out = append(out, s.Name)
	}
	return out
}

func TestPlanCacheGC(t *testing.T) {
	now := cacheTestEpoch.Add(time.Hour)

	t.Run("only snapshot is kept", func(t *testing.T) {
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{cacheTestSnapshot("a", true, cacheTestEpoch)}, now)
		assert.Empty(t, plan.delete)
		assert.Empty(t, plan.markSuperseded)
	})

	t.Run("replaced snapshots are marked first and deleted after the grace period", func(t *testing.T) {
		snapshots := []snapshotv1.VolumeSnapshot{
			cacheTestSnapshot("old", true, cacheTestEpoch),
			cacheTestSnapshot("new", true, cacheTestEpoch.Add(time.Minute)),
		}
		plan := planCacheGC(snapshots, now)
		assert.Equal(t, []string{"old"}, names(plan.markSuperseded))
		assert.Empty(t, plan.delete)

		snapshots[0].Annotations = map[string]string{AnnotationKeyCacheSupersededAt: now.Add(-cacheSupersededGracePeriod / 2).Format(time.RFC3339)}
		plan = planCacheGC(snapshots, now)
		assert.Empty(t, plan.markSuperseded)
		assert.Empty(t, plan.delete, "within the grace period")

		snapshots[0].Annotations[AnnotationKeyCacheSupersededAt] = now.Add(-cacheSupersededGracePeriod).Format(time.RFC3339)
		plan = planCacheGC(snapshots, now)
		assert.Equal(t, []string{"old"}, names(plan.delete))
	})

	t.Run("a corrupt marker does not keep a snapshot forever", func(t *testing.T) {
		old := cacheTestSnapshot("old", true, cacheTestEpoch)
		old.Annotations = map[string]string{AnnotationKeyCacheSupersededAt: "garbage"}
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{old, cacheTestSnapshot("new", true, cacheTestEpoch.Add(time.Minute))}, now)
		assert.Equal(t, []string{"old"}, names(plan.delete))
	})

	t.Run("snapshots still being taken are never deleted", func(t *testing.T) {
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{
			cacheTestSnapshot("current", true, cacheTestEpoch),
			cacheTestSnapshot("pending", false, cacheTestEpoch.Add(time.Minute)),
		}, now)
		assert.Empty(t, plan.delete)
		assert.Empty(t, plan.markSuperseded)
	})

	t.Run("the only ready snapshot is kept even if newer ones fail", func(t *testing.T) {
		failed := cacheTestSnapshot("failed", false, cacheTestEpoch.Add(time.Minute))
		failed.Status.Error = &snapshotv1.VolumeSnapshotError{Message: ptr.To("boom")}
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{cacheTestSnapshot("good", true, cacheTestEpoch), failed}, now)
		assert.Equal(t, []string{"failed"}, names(plan.delete), "old failure is cleaned up")
		assert.Empty(t, plan.markSuperseded, "the good snapshot is still the newest ready one")

		plan = planCacheGC([]snapshotv1.VolumeSnapshot{cacheTestSnapshot("good", true, cacheTestEpoch), failed}, failed.CreationTimestamp.Add(time.Second))
		assert.Empty(t, plan.delete, "recent failure is kept for a while")
	})

	t.Run("snapshots being deleted are left alone", func(t *testing.T) {
		old := cacheTestSnapshot("old", true, cacheTestEpoch)
		old.DeletionTimestamp = ptr.To(metav1.NewTime(now))
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{old, cacheTestSnapshot("new", true, cacheTestEpoch.Add(time.Minute))}, now)
		assert.Empty(t, plan.delete)
		assert.Empty(t, plan.markSuperseded)
	})
}

func TestInjectCacheVolume(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		spec := corev1.PodSpec{Volumes: []corev1.Volume{{Name: "work"}}}
		injectCacheVolume(&spec, "cache", "claim-1")
		require.Len(t, spec.Volumes, 2)
		assert.Equal(t, "work", spec.Volumes[0].Name)
		require.NotNil(t, spec.Volumes[1].PersistentVolumeClaim)
		assert.Equal(t, "claim-1", spec.Volumes[1].PersistentVolumeClaim.ClaimName)
	})

	t.Run("falls back to emptyDir without a claim", func(t *testing.T) {
		spec := corev1.PodSpec{}
		injectCacheVolume(&spec, "cache", "")
		require.Len(t, spec.Volumes, 1)
		assert.NotNil(t, spec.Volumes[0].EmptyDir)
	})

	t.Run("replaces a volume of the same name without touching the shared slice", func(t *testing.T) {
		shared := make([]corev1.Volume, 1, 4)
		shared[0] = corev1.Volume{Name: "cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
		spec := corev1.PodSpec{Volumes: shared}

		injectCacheVolume(&spec, "cache", "claim-1")
		require.Len(t, spec.Volumes, 1)
		assert.NotNil(t, spec.Volumes[0].PersistentVolumeClaim)
		assert.NotNil(t, shared[0].EmptyDir, "the runner spec the pod spec was copied from is unchanged")

		spec2 := corev1.PodSpec{Volumes: shared}
		injectCacheVolume(&spec2, "other", "claim-2")
		assert.Len(t, shared[:2], 2)
		assert.Empty(t, shared[:2][1].Name, "append did not write into the shared backing array")
	})
}

func TestCacheVolumeSpecDefaults(t *testing.T) {
	var nilVolume *v1alpha1.CacheVolume
	assert.Equal(t, v1alpha1.DefaultCacheVolumeName, nilVolume.GetVolumeName())
	assert.Equal(t, v1alpha1.DefaultCacheVolumeName, (&v1alpha1.CacheVolume{}).GetVolumeName())
	assert.Equal(t, "x", (&v1alpha1.CacheVolume{VolumeName: "x"}).GetVolumeName())
}

func newCacheTestReconciler(c client.Client) *EphemeralRunnerReconciler {
	return &EphemeralRunnerReconciler{Client: c, Log: logr.Discard()}
}

func TestEnsureCacheClaim(t *testing.T) {
	ctx := context.Background()
	lineage := cacheLineage(cacheTestNamespace, "set")

	t.Run("empty claim on cold start", func(t *testing.T) {
		c := cacheTestClient(t, true)
		name, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)
		assert.Equal(t, "set-runner-abc-cache", name)

		var claim corev1.PersistentVolumeClaim
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: cacheTestNamespace, Name: name}, &claim))
		assert.Nil(t, claim.Spec.DataSource)
		assert.Equal(t, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, claim.Spec.AccessModes)
		assert.Equal(t, "fast", *claim.Spec.StorageClassName)
		assert.Equal(t, resource.MustParse("50Gi"), claim.Spec.Resources.Requests[corev1.ResourceStorage])
		assert.Equal(t, lineage, claim.Labels[LabelKeyCacheLineage])
		require.Len(t, claim.OwnerReferences, 1)
		assert.Equal(t, "er-uid", string(claim.OwnerReferences[0].UID), "the claim outlives the pod and is owned by the runner")
	})

	t.Run("restored from the newest ready snapshot", func(t *testing.T) {
		older := cacheTestSnapshot("older", true, cacheTestEpoch)
		newer := cacheTestSnapshot("newer", true, cacheTestEpoch.Add(time.Minute))
		pending := cacheTestSnapshot("pending", false, cacheTestEpoch.Add(time.Hour))
		foreign := cacheTestSnapshot("foreign", true, cacheTestEpoch.Add(2*time.Hour))
		foreign.Labels[LabelKeyCacheLineage] = cacheLineage(cacheTestNamespace, "another-set")
		c := cacheTestClient(t, true, &older, &newer, &pending, &foreign)

		name, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)

		var claim corev1.PersistentVolumeClaim
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: cacheTestNamespace, Name: name}, &claim))
		require.NotNil(t, claim.Spec.DataSource)
		assert.Equal(t, "newer", claim.Spec.DataSource.Name)
		assert.Equal(t, "VolumeSnapshot", claim.Spec.DataSource.Kind)
		assert.Equal(t, "snapshot.storage.k8s.io", *claim.Spec.DataSource.APIGroup)
		assert.Equal(t, "newer", claim.Annotations[AnnotationKeyCacheBaseSnapshot])
	})

	t.Run("existing claim is reused, even if a newer snapshot exists", func(t *testing.T) {
		c := cacheTestClient(t, true)
		r := newCacheTestReconciler(c)
		_, err := r.ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)

		newer := cacheTestSnapshot("newer", true, cacheTestEpoch)
		require.NoError(t, c.Create(ctx, &newer))
		name, err := r.ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)

		var claim corev1.PersistentVolumeClaim
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: cacheTestNamespace, Name: name}, &claim))
		assert.Nil(t, claim.Spec.DataSource)
	})

	t.Run("falls back to no claim when the snapshot API is missing", func(t *testing.T) {
		c := cacheTestClient(t, false)
		name, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)
		assert.Empty(t, name)

		var claims corev1.PersistentVolumeClaimList
		require.NoError(t, c.List(ctx, &claims))
		assert.Empty(t, claims.Items)
	})

	t.Run("falls back to no claim when the scale set is unknown", func(t *testing.T) {
		runner := cacheTestRunner("")
		runner.OwnerReferences = nil
		name, err := newCacheTestReconciler(cacheTestClient(t, true)).ensureCacheClaim(ctx, runner, logr.Discard())
		require.NoError(t, err)
		assert.Empty(t, name)
	})
}

func TestCommitCacheVolume(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc-cache"}

	boundClaim := func() *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: key.Name, Namespace: key.Namespace,
				Annotations: map[string]string{AnnotationKeyCacheBaseSnapshot: "base"},
			},
			Spec:   corev1.PersistentVolumeClaimSpec{VolumeName: "pv-1"},
			Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
		}
	}
	runnerPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "set-runner-abc", Namespace: cacheTestNamespace}}
	attachment := &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "csi-1"},
		Spec: storagev1.VolumeAttachmentSpec{
			Attacher: "csi.example.com", NodeName: "node-1",
			Source: storagev1.VolumeAttachmentSource{PersistentVolumeName: ptr.To("pv-1")},
		},
	}

	snapshotExists := func(t *testing.T, c client.Client) bool {
		t.Helper()
		err := c.Get(ctx, key, new(snapshotv1.VolumeSnapshot))
		if err != nil {
			require.True(t, client.IgnoreNotFound(err) == nil, "unexpected error: %v", err)
			return false
		}
		return true
	}

	t.Run("snapshots the claim of a runner that succeeded", func(t *testing.T) {
		c := cacheTestClient(t, true, boundClaim(), cacheTestScaleSet())
		res, err := newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
		require.NoError(t, err)
		assert.True(t, res.IsZero())

		var snapshot snapshotv1.VolumeSnapshot
		require.NoError(t, c.Get(ctx, key, &snapshot))
		assert.Equal(t, key.Name, *snapshot.Spec.Source.PersistentVolumeClaimName)
		assert.Equal(t, "snap", *snapshot.Spec.VolumeSnapshotClassName)
		assert.Equal(t, cacheLineage(cacheTestNamespace, "set"), snapshot.Labels[LabelKeyCacheLineage])
		assert.Equal(t, "set-runner-abc", snapshot.Annotations[AnnotationKeyCacheSourceRunner])
		assert.Equal(t, "base", snapshot.Annotations[AnnotationKeyCacheBaseSnapshot])
		assert.Equal(t, "org/repo", snapshot.Annotations[AnnotationKeyCacheJobRepository])
		assert.Equal(t, "42", snapshot.Annotations[AnnotationKeyCacheWorkflowRunID])
		require.Len(t, snapshot.OwnerReferences, 1)
		assert.Equal(t, "AutoscalingRunnerSet", snapshot.OwnerReferences[0].Kind)
		assert.Equal(t, "ars-uid", string(snapshot.OwnerReferences[0].UID), "uninstalling the scale set removes its cache")
	})

	t.Run("is idempotent", func(t *testing.T) {
		c := cacheTestClient(t, true, boundClaim(), cacheTestScaleSet())
		r := newCacheTestReconciler(c)
		for range 2 {
			res, err := r.commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
			require.NoError(t, err)
			assert.True(t, res.IsZero())
		}
		var list snapshotv1.VolumeSnapshotList
		require.NoError(t, c.List(ctx, &list))
		assert.Len(t, list.Items, 1)
	})

	t.Run("does not commit runners that did not succeed or have no cache", func(t *testing.T) {
		for _, phase := range []v1alpha1.EphemeralRunnerPhase{"", v1alpha1.EphemeralRunnerPhaseRunning, v1alpha1.EphemeralRunnerPhaseFailed, v1alpha1.EphemeralRunnerPhaseOutdated} {
			c := cacheTestClient(t, true, boundClaim(), cacheTestScaleSet())
			_, err := newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(phase), logr.Discard())
			require.NoError(t, err)
			assert.False(t, snapshotExists(t, c), "phase %q", phase)
		}

		runner := cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded)
		runner.Spec.CacheVolume = nil
		c := cacheTestClient(t, true, boundClaim(), cacheTestScaleSet())
		_, err := newCacheTestReconciler(c).commitCacheVolume(ctx, runner, logr.Discard())
		require.NoError(t, err)
		assert.False(t, snapshotExists(t, c))
	})

	t.Run("does nothing without a bound claim", func(t *testing.T) {
		pending := boundClaim()
		pending.Status.Phase = corev1.ClaimPending
		for name, c := range map[string]client.Client{
			"missing": cacheTestClient(t, true, cacheTestScaleSet()),
			"pending": cacheTestClient(t, true, pending, cacheTestScaleSet()),
		} {
			res, err := newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
			require.NoError(t, err, name)
			assert.True(t, res.IsZero(), name)
			assert.False(t, snapshotExists(t, c), name)
		}
	})

	t.Run("does nothing when the snapshot API is missing or the scale set is gone", func(t *testing.T) {
		c := cacheTestClient(t, false, boundClaim(), cacheTestScaleSet())
		_, err := newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
		require.NoError(t, err)

		c = cacheTestClient(t, true, boundClaim())
		_, err = newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
		require.NoError(t, err)
		assert.False(t, snapshotExists(t, c))
	})

	t.Run("waits while the pod exists or the volume is attached", func(t *testing.T) {
		for name, extra := range map[string]client.Object{"pod": runnerPod, "attachment": attachment} {
			c := cacheTestClient(t, true, boundClaim(), cacheTestScaleSet(), extra)
			res, err := newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
			require.NoError(t, err, name)
			assert.Equal(t, ctrl.Result{RequeueAfter: cacheDetachPollInterval}, res, name)
			assert.False(t, snapshotExists(t, c), name)
		}
	})

	t.Run("snapshots anyway once the detach wait timed out", func(t *testing.T) {
		runner := cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded)
		runner.DeletionTimestamp = ptr.To(metav1.NewTime(time.Now().Add(-2 * cacheDetachWaitTimeout)))
		c := cacheTestClient(t, true, boundClaim(), cacheTestScaleSet(), runnerPod, attachment)
		res, err := newCacheTestReconciler(c).commitCacheVolume(ctx, runner, logr.Discard())
		require.NoError(t, err)
		assert.True(t, res.IsZero())
		assert.True(t, snapshotExists(t, c))
	})
}

func TestCacheSnapshotReconciler(t *testing.T) {
	ctx := context.Background()
	now := cacheTestEpoch.Add(time.Hour)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: cacheTestNamespace, Name: "set"}}

	remaining := func(t *testing.T, c client.Client) []string {
		t.Helper()
		var list snapshotv1.VolumeSnapshotList
		require.NoError(t, c.List(ctx, &list))
		out := make([]string, 0, len(list.Items))
		for _, s := range list.Items {
			out = append(out, s.Name)
		}
		return out
	}

	t.Run("keeps only the newest snapshot after the grace period", func(t *testing.T) {
		old, newest := cacheTestSnapshot("old", true, cacheTestEpoch), cacheTestSnapshot("new", true, cacheTestEpoch.Add(time.Minute))
		other := cacheTestSnapshot("other-set", true, cacheTestEpoch.Add(-time.Hour))
		other.Labels[LabelKeyCacheLineage] = cacheLineage(cacheTestNamespace, "another-set")
		c := cacheTestClient(t, true, cacheTestScaleSet(), &old, &newest, &other)

		clock := now
		r := &CacheSnapshotReconciler{Client: c, Log: logr.Discard(), Now: func() time.Time { return clock }}

		res, err := r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, cacheGCInterval, res.RequeueAfter)
		assert.ElementsMatch(t, []string{"old", "new", "other-set"}, remaining(t, c), "first sighting only marks")

		clock = now.Add(cacheSupersededGracePeriod / 2)
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"old", "new", "other-set"}, remaining(t, c))

		clock = now.Add(cacheSupersededGracePeriod)
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"new", "other-set"}, remaining(t, c), "snapshots of other scale sets are untouched")
	})

	t.Run("does nothing without a cache volume, without the API, or without the scale set", func(t *testing.T) {
		old, newest := cacheTestSnapshot("old", true, cacheTestEpoch), cacheTestSnapshot("new", true, cacheTestEpoch.Add(time.Minute))
		old.Annotations = map[string]string{AnnotationKeyCacheSupersededAt: cacheTestEpoch.Format(time.RFC3339)}

		noCache := cacheTestScaleSet()
		noCache.Spec.CacheVolume = nil
		c := cacheTestClient(t, true, noCache, &old, &newest)
		r := &CacheSnapshotReconciler{Client: c, Log: logr.Discard(), Now: func() time.Time { return now }}
		res, err := r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.True(t, res.IsZero())
		assert.Len(t, remaining(t, c), 2)

		c = cacheTestClient(t, false, cacheTestScaleSet())
		r = &CacheSnapshotReconciler{Client: c, Log: logr.Discard()}
		res, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, time.Minute, res.RequeueAfter)

		c = cacheTestClient(t, true)
		r = &CacheSnapshotReconciler{Client: c, Log: logr.Discard()}
		res, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		assert.True(t, res.IsZero())
	})
}
