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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWorkflowRefRef(t *testing.T) {
	assert.Equal(t, "refs/heads/main", workflowRefRef("org/repo/.github/workflows/ci.yaml@refs/heads/main"))
	assert.Equal(t, "refs/pull/7/merge", workflowRefRef("org/repo/.github/workflows/ci.yaml@refs/pull/7/merge"))
	assert.Equal(t, "refs/tags/v1", workflowRefRef("org/repo/.github/workflows/ci.yaml@refs/tags/v1"))
	assert.Empty(t, workflowRefRef("no-ref"))
	assert.Empty(t, workflowRefRef(""))
}

func TestCacheCommitAllowed(t *testing.T) {
	job := func(repo, ref, event string) *v1alpha1.EphemeralRunnerStatus {
		st := &v1alpha1.EphemeralRunnerStatus{JobRepositoryName: repo, JobEventName: event}
		if ref != "" {
			st.JobWorkflowRef = repo + "/.github/workflows/ci.yaml@" + ref
		}
		return st
	}

	tests := []struct {
		name   string
		policy *v1alpha1.CacheCommitPolicy
		status *v1alpha1.EphemeralRunnerStatus
		want   bool
	}{
		{"no policy allows everything, even without job info", nil, &v1alpha1.EphemeralRunnerStatus{}, true},
		{"empty policy allows everything", &v1alpha1.CacheCommitPolicy{}, &v1alpha1.EphemeralRunnerStatus{}, true},

		{"repository glob matches", &v1alpha1.CacheCommitPolicy{Repositories: []string{"org/*"}}, job("org/repo", "refs/heads/main", "push"), true},
		{"repository exact matches", &v1alpha1.CacheCommitPolicy{Repositories: []string{"org/repo"}}, job("org/repo", "refs/heads/main", "push"), true},
		{"other repository is refused", &v1alpha1.CacheCommitPolicy{Repositories: []string{"org/*"}}, job("evil/repo", "refs/heads/main", "push"), false},
		{"any of several repositories", &v1alpha1.CacheCommitPolicy{Repositories: []string{"a/x", "org/*"}}, job("org/repo", "", ""), true},

		{"ref exact matches", &v1alpha1.CacheCommitPolicy{Refs: []string{"refs/heads/main"}}, job("org/repo", "refs/heads/main", "push"), true},
		{"ref glob stays within a path segment", &v1alpha1.CacheCommitPolicy{Refs: []string{"refs/heads/*"}}, job("org/repo", "refs/heads/release/1", "push"), false},
		{"ref glob with segments", &v1alpha1.CacheCommitPolicy{Refs: []string{"refs/heads/release/*"}}, job("org/repo", "refs/heads/release/1", "push"), true},
		{"pull request ref is refused", &v1alpha1.CacheCommitPolicy{Refs: []string{"refs/heads/main"}}, job("org/repo", "refs/pull/7/merge", "pull_request"), false},

		{"event matches", &v1alpha1.CacheCommitPolicy{Events: []string{"push", "schedule"}}, job("org/repo", "refs/heads/main", "schedule"), true},
		{"pull_request_target is refused even from the base branch", &v1alpha1.CacheCommitPolicy{Events: []string{"push"}}, job("org/repo", "refs/heads/main", "pull_request_target"), false},
		{"events are compared exactly", &v1alpha1.CacheCommitPolicy{Events: []string{"push"}}, job("org/repo", "refs/heads/main", "pus"), false},

		{"every list has to match", &v1alpha1.CacheCommitPolicy{Repositories: []string{"org/*"}, Refs: []string{"refs/heads/main"}, Events: []string{"push"}}, job("org/repo", "refs/heads/main", "push"), true},
		{"one failing list refuses", &v1alpha1.CacheCommitPolicy{Repositories: []string{"org/*"}, Refs: []string{"refs/heads/main"}, Events: []string{"push"}}, job("org/repo", "refs/heads/main", "pull_request"), false},

		// Fail closed: a restriction cannot be checked against something unknown.
		{"repository unknown", &v1alpha1.CacheCommitPolicy{Repositories: []string{"*/*"}}, &v1alpha1.EphemeralRunnerStatus{}, false},
		{"ref unknown", &v1alpha1.CacheCommitPolicy{Refs: []string{"*"}}, &v1alpha1.EphemeralRunnerStatus{JobWorkflowRef: "no-ref"}, false},
		{"event unknown, as when the listener did not record it", &v1alpha1.CacheCommitPolicy{Events: []string{"push"}}, job("org/repo", "refs/heads/main", ""), false},
		{"invalid pattern never matches", &v1alpha1.CacheCommitPolicy{Repositories: []string{"org/[a-"}}, job("org/repo", "", ""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := cacheCommitAllowed(tt.policy, tt.status)
			assert.Equal(t, tt.want, got, reason)
			if !tt.want {
				assert.NotEmpty(t, reason)
			}
		})
	}
}

func TestCommitCacheVolumeCommitPolicy(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc-cache"}
	claim := func() *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
		}
	}
	runner := func(event, ref string) *v1alpha1.EphemeralRunner {
		r := cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded)
		r.Spec.CacheVolume.CommitPolicy = &v1alpha1.CacheCommitPolicy{
			Repositories: []string{"org/*"}, Refs: []string{"refs/heads/main"}, Events: []string{"push"},
		}
		r.Status.JobEventName = event
		r.Status.JobWorkflowRef = "org/repo/.github/workflows/ci.yaml@" + ref
		return r
	}
	exists := func(c client.Client) bool {
		return c.Get(ctx, key, new(snapshotv1.VolumeSnapshot)) == nil
	}

	t.Run("a job that matches the policy commits and the event is recorded", func(t *testing.T) {
		c := cacheTestClient(t, true, claim(), cacheTestScaleSet())
		_, err := newCacheTestReconciler(c).commitCacheVolume(ctx, runner("push", "refs/heads/main"), logr.Discard())
		require.NoError(t, err)

		var s snapshotv1.VolumeSnapshot
		require.NoError(t, c.Get(ctx, key, &s))
		assert.Equal(t, "push", s.Annotations[AnnotationKeyCacheJobEvent])
	})

	t.Run("pull request jobs and unknown jobs are discarded", func(t *testing.T) {
		for name, r := range map[string]*v1alpha1.EphemeralRunner{
			"pull_request":        runner("pull_request", "refs/pull/7/merge"),
			"pull_request_target": runner("pull_request_target", "refs/heads/main"),
			"event not recorded":  runner("", "refs/heads/main"),
			"wrong ref":           runner("push", "refs/heads/feature"),
			"no job info": func() *v1alpha1.EphemeralRunner {
				r := runner("", "")
				r.Status = v1alpha1.EphemeralRunnerStatus{Phase: v1alpha1.EphemeralRunnerPhaseSucceeded}
				return r
			}(),
		} {
			c := cacheTestClient(t, true, claim(), cacheTestScaleSet())
			res, err := newCacheTestReconciler(c).commitCacheVolume(ctx, r, logr.Discard())
			require.NoError(t, err, name)
			assert.True(t, res.IsZero(), name+": the runner is released without waiting")
			assert.False(t, exists(c), name)
		}
	})
}

func TestCacheRetentionLimits(t *testing.T) {
	maxAge, idle := cacheRetentionLimits(nil)
	assert.Equal(t, DefaultCacheMaxAge, maxAge)
	assert.Equal(t, DefaultCacheIdleTimeout, idle)

	maxAge, idle = cacheRetentionLimits(&v1alpha1.CacheVolume{})
	assert.Equal(t, DefaultCacheMaxAge, maxAge)
	assert.Equal(t, DefaultCacheIdleTimeout, idle)

	maxAge, idle = cacheRetentionLimits(&v1alpha1.CacheVolume{Retention: &v1alpha1.CacheRetention{
		MaxAge: &metav1.Duration{Duration: time.Hour},
	}})
	assert.Equal(t, time.Hour, maxAge)
	assert.Equal(t, DefaultCacheIdleTimeout, idle, "unset limits keep their default")

	maxAge, idle = cacheRetentionLimits(&v1alpha1.CacheVolume{Retention: &v1alpha1.CacheRetention{
		MaxAge:      &metav1.Duration{},
		IdleTimeout: &metav1.Duration{Duration: -time.Hour},
	}})
	assert.Zero(t, maxAge, "zero disables the limit")
	assert.Zero(t, idle, "a negative duration disables the limit too")
}

func TestCacheExpired(t *testing.T) {
	now := cacheTestEpoch.Add(100 * time.Hour)
	snap := func(chainStarted, lastUsed time.Duration) *snapshotv1.VolumeSnapshot {
		snapshot := cacheTestSnapshot("s", true, cacheTestEpoch.Add(90*time.Hour)) // cut 10h ago
		s := &snapshot
		s.Annotations = map[string]string{}
		if chainStarted != 0 {
			s.Annotations[AnnotationKeyCacheChainStartedAt] = now.Add(-chainStarted).Format(time.RFC3339)
		}
		if lastUsed != 0 {
			s.Annotations[AnnotationKeyCacheLastUsedAt] = now.Add(-lastUsed).Format(time.RFC3339)
		}
		return s
	}

	expired, _ := cacheExpired(snap(0, 0), 24*time.Hour, 24*time.Hour, now)
	assert.False(t, expired, "cut 10h ago, no annotations")

	expired, reason := cacheExpired(snap(30*time.Hour, 0), 24*time.Hour, 0, now)
	assert.True(t, expired, "the history started too long ago")
	assert.Contains(t, reason, "history")

	expired, _ = cacheExpired(snap(30*time.Hour, 0), 0, 0, now)
	assert.False(t, expired, "limits of zero are disabled")

	expired, reason = cacheExpired(snap(0, 0), 0, 5*time.Hour, now)
	assert.True(t, expired, "cut 10h ago and never restored")
	assert.Contains(t, reason, "not used")

	expired, _ = cacheExpired(snap(0, 2*time.Hour), 0, 5*time.Hour, now)
	assert.False(t, expired, "a recent restore keeps the snapshot alive")

	expired, _ = cacheExpired(snap(0, 50*time.Hour), 0, 5*time.Hour, now)
	assert.True(t, expired, "a restore older than the cut does not count: the snapshot was cut 10h ago")
}

func TestPlanCacheGCRetention(t *testing.T) {
	now := cacheTestEpoch.Add(100 * time.Hour)
	fresh := cacheTestSnapshot("fresh", true, cacheTestEpoch.Add(99*time.Hour))

	t.Run("an expired newest snapshot is deleted", func(t *testing.T) {
		old := cacheTestSnapshot("idle", true, cacheTestEpoch)
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{old}, now, 0, 24*time.Hour)
		assert.Equal(t, []string{"idle"}, names(plan.delete))

		plan = planCacheGC([]snapshotv1.VolumeSnapshot{old}, now, 24*time.Hour, 0)
		assert.Equal(t, []string{"idle"}, names(plan.delete), "the chain started when the snapshot was cut")
	})

	t.Run("a snapshot within the retention is kept", func(t *testing.T) {
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{fresh}, now, 24*time.Hour, 24*time.Hour)
		assert.Empty(t, plan.delete)
		assert.Empty(t, plan.markSuperseded)
	})

	t.Run("a long chain expires even if it is used all the time", func(t *testing.T) {
		busy := cacheTestSnapshot("busy", true, cacheTestEpoch.Add(99*time.Hour))
		busy.Annotations = map[string]string{
			AnnotationKeyCacheChainStartedAt: cacheTestEpoch.Format(time.RFC3339),
			AnnotationKeyCacheLastUsedAt:     now.Add(-time.Minute).Format(time.RFC3339),
		}
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{busy}, now, 24*time.Hour, 24*time.Hour)
		assert.Equal(t, []string{"busy"}, names(plan.delete), "this is what bounds the growth of the cache")
	})

	t.Run("an old restore keeps an old snapshot alive", func(t *testing.T) {
		used := cacheTestSnapshot("used", true, cacheTestEpoch)
		used.Annotations = map[string]string{AnnotationKeyCacheLastUsedAt: now.Add(-time.Hour).Format(time.RFC3339)}
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{used}, now, 0, 24*time.Hour)
		assert.Empty(t, plan.delete)
	})

	t.Run("older snapshots are still replaced normally next to an expired newest one", func(t *testing.T) {
		older := cacheTestSnapshot("older", true, cacheTestEpoch.Add(-time.Hour))
		newest := cacheTestSnapshot("newest", true, cacheTestEpoch)
		plan := planCacheGC([]snapshotv1.VolumeSnapshot{older, newest}, now, 0, 24*time.Hour)
		assert.Equal(t, []string{"newest"}, names(plan.delete))
		assert.Equal(t, []string{"older"}, names(plan.markSuperseded))
	})
}

func TestEnsureCacheClaimRetention(t *testing.T) {
	ctx := context.Background()
	getClaim := func(t *testing.T, c client.Client) corev1.PersistentVolumeClaim {
		t.Helper()
		var claim corev1.PersistentVolumeClaim
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc-cache"}, &claim))
		return claim
	}

	t.Run("the cache history start is inherited and the use is recorded", func(t *testing.T) {
		started := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
		snap := cacheTestSnapshot("s", true, time.Now().Add(-time.Hour))
		snap.Annotations = map[string]string{AnnotationKeyCacheChainStartedAt: started.Format(time.RFC3339)}
		c := cacheTestClient(t, true, &snap)

		_, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)

		claim := getClaim(t, c)
		assert.Equal(t, "s", claim.Spec.DataSource.Name)
		assert.Equal(t, started.Format(time.RFC3339), claim.Annotations[AnnotationKeyCacheChainStartedAt])

		var got snapshotv1.VolumeSnapshot
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: cacheTestNamespace, Name: "s"}, &got))
		used, err := time.Parse(time.RFC3339, got.Annotations[AnnotationKeyCacheLastUsedAt])
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), used, time.Minute)
	})

	t.Run("a cold start begins a new history", func(t *testing.T) {
		c := cacheTestClient(t, true)
		_, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)

		started, err := time.Parse(time.RFC3339, getClaim(t, c).Annotations[AnnotationKeyCacheChainStartedAt])
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), started, time.Minute)
	})

	t.Run("an expired snapshot is not restored from", func(t *testing.T) {
		old := cacheTestSnapshot("old", true, time.Now().Add(-30*24*time.Hour))
		c := cacheTestClient(t, true, &old)

		_, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, cacheTestRunner(""), logr.Discard())
		require.NoError(t, err)

		claim := getClaim(t, c)
		assert.Nil(t, claim.Spec.DataSource, "the history is older than the default maximum age")
		started, err := time.Parse(time.RFC3339, claim.Annotations[AnnotationKeyCacheChainStartedAt])
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), started, time.Minute, "a new history starts")
	})

	t.Run("limits can be configured and disabled", func(t *testing.T) {
		old := cacheTestSnapshot("old", true, time.Now().Add(-30*24*time.Hour))

		disabled := cacheTestRunner("")
		disabled.Spec.CacheVolume.Retention = &v1alpha1.CacheRetention{MaxAge: &metav1.Duration{}, IdleTimeout: &metav1.Duration{}}
		c := cacheTestClient(t, true, &old)
		_, err := newCacheTestReconciler(c).ensureCacheClaim(ctx, disabled, logr.Discard())
		require.NoError(t, err)
		assert.NotNil(t, getClaim(t, c).Spec.DataSource, "no limits, so it is restored")

		strict := cacheTestRunner("")
		strict.Spec.CacheVolume.Retention = &v1alpha1.CacheRetention{MaxAge: &metav1.Duration{Duration: time.Minute}}
		fresh := cacheTestSnapshot("fresh", true, time.Now().Add(-time.Hour))
		c = cacheTestClient(t, true, &fresh)
		_, err = newCacheTestReconciler(c).ensureCacheClaim(ctx, strict, logr.Discard())
		require.NoError(t, err)
		assert.Nil(t, getClaim(t, c).Spec.DataSource, "older than the configured maximum age")
	})
}

func TestCommitCacheVolumeKeepsCacheHistoryStart(t *testing.T) {
	ctx := context.Background()
	started := "2026-11-01T00:00:00Z"
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "set-runner-abc-cache", Namespace: cacheTestNamespace,
			Annotations: map[string]string{AnnotationKeyCacheChainStartedAt: started},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	c := cacheTestClient(t, true, claim, cacheTestScaleSet())
	_, err := newCacheTestReconciler(c).commitCacheVolume(ctx, cacheTestRunner(v1alpha1.EphemeralRunnerPhaseSucceeded), logr.Discard())
	require.NoError(t, err)

	var s snapshotv1.VolumeSnapshot
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: cacheTestNamespace, Name: "set-runner-abc-cache"}, &s))
	assert.Equal(t, started, s.Annotations[AnnotationKeyCacheChainStartedAt], "otherwise a busy cache would never reach its maximum age")
}
