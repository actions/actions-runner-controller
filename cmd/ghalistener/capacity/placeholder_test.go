package capacity

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

var discardLogger = slog.New(slog.DiscardHandler)

// newFakeClientset creates a fake.Clientset with a reactor that makes
// DeleteCollection actually delete matching pods from the object tracker.
// The default fake client records the action but does not remove objects.
func newFakeClientset() *fake.Clientset {
	cs := fake.NewSimpleClientset()
	podsGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	podsGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}

	// The default fake client's DeleteCollection records the action but
	// does not actually remove objects from the tracker. This reactor
	// uses the ObjectTracker directly (bypassing Invokes) to avoid the
	// mutex deadlock that would occur if we called cs.CoreV1().Pods()
	// from within a reactor.
	cs.PrependReactor("delete-collection", "pods",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			dca := action.(k8stesting.DeleteCollectionActionImpl)
			ns := dca.GetNamespace()
			selectorStr := dca.GetListOptions().LabelSelector

			obj, err := cs.Tracker().List(podsGVR, podsGVK, ns)
			if err != nil {
				return true, nil, err
			}
			podList := obj.(*corev1.PodList)

			sel, err := labels.Parse(selectorStr)
			if err != nil {
				return true, nil, err
			}

			for i := range podList.Items {
				p := &podList.Items[i]
				if sel.Matches(labels.Set(p.Labels)) {
					_ = cs.Tracker().Delete(podsGVR, ns, p.Name)
				}
			}
			return true, nil, nil
		},
	)
	return cs
}

func newTestPM(t *testing.T, cfg Config) (*PlaceholderManager, *fake.Clientset) {
	t.Helper()
	if cfg.Namespace == "" {
		cfg.Namespace = "test-ns"
	}
	if cfg.ScaleSetName == "" {
		cfg.ScaleSetName = "test-scale-set"
	}
	cs := newFakeClientset()
	pm := NewPlaceholderManager(cs, cfg.Namespace, "listener-abc", cfg, discardLogger)
	return pm, cs
}

func TestCreatePair(t *testing.T) {
	cfg := Config{
		RunnerCPU:      "750m",
		RunnerMemory:   "512Mi",
		WorkflowCPU:    "4",
		WorkflowMemory: "8Gi",
	}
	pm, cs := newTestPM(t, cfg)
	ctx := context.Background()

	err := pm.CreatePair(ctx, "slot-1")
	require.NoError(t, err)

	pods, err := cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, pods.Items, 2)

	var runner, workflow *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		switch p.Labels[labelPlaceholderRole] {
		case rolePlaceholderRunner:
			runner = p
		case rolePlaceholderWorkflow:
			workflow = p
		}
	}
	require.NotNil(t, runner, "runner pod must exist")
	require.NotNil(t, workflow, "workflow pod must exist")

	// Names.
	assert.True(t, strings.HasPrefix(runner.Name, "ph-r-"), "runner name prefix")
	assert.True(t, strings.HasPrefix(workflow.Name, "ph-w-"), "workflow name prefix")

	// Labels.
	assert.Equal(t, managedByValue, runner.Labels[labelManagedBy])
	assert.Equal(t, "slot-1", runner.Labels[labelPlaceholderID])
	assert.Equal(t, "listener-abc", runner.Labels[labelListenerPod])
	assert.Equal(t, "test-scale-set", runner.Labels[labelScaleSet])

	// Priority classes.
	assert.Equal(t, "placeholder-runner", runner.Spec.PriorityClassName)
	assert.Equal(t, "placeholder-workflow", workflow.Spec.PriorityClassName)

	// Runner uses hard nodeSelector; workflow uses soft node affinity
	// (matching modules/arc-runners/templates/runner.yaml.tpl).
	assert.Nil(t, runner.Spec.Affinity, "runner uses nodeSelector, not affinity")
	assert.Nil(t, workflow.Spec.NodeSelector,
		"workflow uses affinity, not hard nodeSelector")
	require.NotNil(t, workflow.Spec.Affinity, "workflow must have affinity")
	require.NotNil(t, workflow.Spec.Affinity.NodeAffinity, "workflow nodeAffinity")

	// Resources.
	runnerReq := runner.Spec.Containers[0].Resources.Requests
	assert.Equal(t, "750m", runnerReq.Cpu().String())
	assert.Equal(t, "512Mi", runnerReq.Memory().String())

	wfReq := workflow.Spec.Containers[0].Resources.Requests
	assert.Equal(t, "4", wfReq.Cpu().String())
	assert.Equal(t, "8Gi", wfReq.Memory().String())
}

// When the workflow pod creation fails, the runner pod that was already
// created must be best-effort cleaned up so we don't leave a half-pair
// occupying capacity.
func TestCreatePair_WorkflowFailureCleansUpRunner(t *testing.T) {
	pm, cs := newTestPM(t, Config{})
	ctx := context.Background()

	// Inject a failure for the workflow pod create only (name has "ph-w-" prefix).
	cs.PrependReactor("create", "pods",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			ca := action.(k8stesting.CreateAction)
			pod, ok := ca.GetObject().(*corev1.Pod)
			if !ok {
				return false, nil, nil
			}
			if strings.HasPrefix(pod.Name, "ph-w-") {
				return true, nil, errors.New("simulated workflow create failure")
			}
			return false, nil, nil
		},
	)

	err := pm.CreatePair(ctx, "fail-slot")
	require.Error(t, err, "CreatePair must surface workflow creation error")
	assert.Contains(t, err.Error(), "creating workflow placeholder")

	// Runner pod must have been best-effort deleted; no pods should remain.
	pods, listErr := cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Empty(t, pods.Items,
		"runner pod must be cleaned up after workflow create failure")
}

func TestDeletePair(t *testing.T) {
	pm, cs := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "slot-del"))

	pods, _ := cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	require.Len(t, pods.Items, 2)

	require.NoError(t, pm.DeletePair(ctx, "slot-del"))

	pods, _ = cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	assert.Empty(t, pods.Items)
}

func TestListPairs_GroupsBySlotID(t *testing.T) {
	pm, _ := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "slot-a"))
	require.NoError(t, pm.CreatePair(ctx, "slot-b"))

	pairs, err := pm.ListPairs(ctx)
	require.NoError(t, err)
	assert.Len(t, pairs, 2)
	assert.Contains(t, pairs, "slot-a")
	assert.Contains(t, pairs, "slot-b")

	for _, pair := range pairs {
		assert.NotNil(t, pair.RunnerPod, "pair must have runner pod")
		assert.NotNil(t, pair.WorkflowPod, "pair must have workflow pod")
	}
}

func TestCleanupTimedOut(t *testing.T) {
	cfg := Config{PlaceholderTimeout: 1 * time.Second}
	pm, cs := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "slot-old"))
	require.NoError(t, pm.CreatePair(ctx, "slot-new"))

	// Make "old" pair's pods Pending with old creation timestamps.
	setPodsCreationAndPhase(t, cs, ctx, "test-ns", "slot-old",
		time.Now().Add(-10*time.Minute), corev1.PodPending)
	// Make "new" pair's pods Pending but recent.
	setPodsCreationAndPhase(t, cs, ctx, "test-ns", "slot-new",
		time.Now(), corev1.PodPending)

	deleted, failed, err := pm.CleanupTimedOut(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.Equal(t, 0, failed)

	pairs, _ := pm.ListPairs(ctx)
	assert.Len(t, pairs, 1)
	assert.Contains(t, pairs, "slot-new")
}

func TestCleanupOrphans(t *testing.T) {
	cfg := Config{Namespace: "test-ns", ScaleSetName: "sset"}
	cs := newFakeClientset()
	ctx := context.Background()

	// Create pods from a different listener (same scale set) — these are orphans.
	otherPM := NewPlaceholderManager(cs, "test-ns", "old-listener", cfg, discardLogger)
	require.NoError(t, otherPM.CreatePair(ctx, "orphan-slot"))

	// Create pods from the current listener.
	pm := NewPlaceholderManager(cs, "test-ns", "current-listener", cfg, discardLogger)
	require.NoError(t, pm.CreatePair(ctx, "my-slot"))

	pods, _ := cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	assert.Len(t, pods.Items, 4, "4 pods before cleanup")

	deleted, err := pm.CleanupOrphans(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "two orphan pods deleted")

	pods, _ = cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	assert.Len(t, pods.Items, 2, "only current listener pods remain")
	for _, p := range pods.Items {
		assert.Equal(t, "current-listener", p.Labels[labelListenerPod])
	}
}

// CleanupOrphans must only delete pods belonging to the SAME scale set —
// listeners from a different scale set must be left alone.
// Regression test for the cross-scale-set deletion bug.
func TestCleanupOrphans_ScopedToScaleSet(t *testing.T) {
	cs := newFakeClientset()
	ctx := context.Background()

	// Other scale set, other listener — must NOT be touched.
	otherSetCfg := Config{Namespace: "test-ns", ScaleSetName: "other-sset"}
	otherSetPM := NewPlaceholderManager(cs, "test-ns", "old-listener-other-sset", otherSetCfg, discardLogger)
	require.NoError(t, otherSetPM.CreatePair(ctx, "other-slot"))

	// Same scale set, different listener — orphan, MUST be deleted.
	myCfg := Config{Namespace: "test-ns", ScaleSetName: "my-sset"}
	stalePM := NewPlaceholderManager(cs, "test-ns", "old-listener-my-sset", myCfg, discardLogger)
	require.NoError(t, stalePM.CreatePair(ctx, "stale-slot"))

	// Same scale set, current listener — MUST be left alone.
	pm := NewPlaceholderManager(cs, "test-ns", "current-listener", myCfg, discardLogger)
	require.NoError(t, pm.CreatePair(ctx, "current-slot"))

	pods, _ := cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	assert.Len(t, pods.Items, 6, "6 pods before cleanup")

	deleted, err := pm.CleanupOrphans(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "two stale orphan pods deleted")

	pods, _ = cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	// 2 from other-sset (preserved) + 2 from current listener (preserved).
	assert.Len(t, pods.Items, 4,
		"orphan pods from same scale set deleted; other scale set untouched")

	otherSetCount, currentCount, staleCount := 0, 0, 0
	for _, p := range pods.Items {
		switch p.Labels[labelScaleSet] {
		case "other-sset":
			otherSetCount++
		case "my-sset":
			if p.Labels[labelListenerPod] == "current-listener" {
				currentCount++
			} else {
				staleCount++
			}
		}
	}
	assert.Equal(t, 2, otherSetCount, "other scale set pods preserved")
	assert.Equal(t, 2, currentCount, "current listener pods preserved")
	assert.Equal(t, 0, staleCount, "stale orphans of same scale set deleted")
}

func TestCleanupAll(t *testing.T) {
	pm, cs := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "s1"))
	require.NoError(t, pm.CreatePair(ctx, "s2"))

	pods, _ := cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	assert.Len(t, pods.Items, 4)

	require.NoError(t, pm.CleanupAll(ctx))

	pods, _ = cs.CoreV1().Pods("test-ns").List(ctx, metav1.ListOptions{})
	assert.Empty(t, pods.Items)
}

func TestBothRunning(t *testing.T) {
	pair := &PlaceholderPair{
		RunnerPod:   &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		WorkflowPod: &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	}
	assert.True(t, pair.BothRunning())

	pair.WorkflowPod.Status.Phase = corev1.PodPending
	assert.False(t, pair.BothRunning())

	pair.WorkflowPod = nil
	assert.False(t, pair.BothRunning())
}

func TestAnyPendingTooLong(t *testing.T) {
	timeout := 5 * time.Minute
	oldTime := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	recentTime := metav1.NewTime(time.Now())

	pair := &PlaceholderPair{
		RunnerPod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: oldTime},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		WorkflowPod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: recentTime},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	}
	assert.True(t, pair.AnyPendingTooLong(timeout), "old pending runner triggers")

	pair.RunnerPod.Status.Phase = corev1.PodRunning
	assert.False(t, pair.AnyPendingTooLong(timeout), "running pods do not trigger")
}

// ---- test helpers ----

func setPodsPhase(t *testing.T, cs *fake.Clientset, ctx context.Context,
	ns, slotID string, phase corev1.PodPhase) {
	t.Helper()
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelPlaceholderID + "=" + slotID,
	})
	require.NoError(t, err)
	for i := range pods.Items {
		p := pods.Items[i]
		p.Status.Phase = phase
		_, err := cs.CoreV1().Pods(ns).UpdateStatus(ctx, &p, metav1.UpdateOptions{})
		require.NoError(t, err)
	}
}

func setPodsCreationAndPhase(t *testing.T, cs *fake.Clientset, ctx context.Context,
	ns, slotID string, created time.Time, phase corev1.PodPhase) {
	t.Helper()
	podsGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelPlaceholderID + "=" + slotID,
	})
	require.NoError(t, err)
	for i := range pods.Items {
		p := pods.Items[i]
		p.CreationTimestamp = metav1.NewTime(created)
		p.Status.Phase = phase
		require.NoError(t, cs.Tracker().Update(podsGVR, &p, ns))
	}
}
