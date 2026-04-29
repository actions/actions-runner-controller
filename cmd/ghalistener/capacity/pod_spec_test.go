package capacity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestPodNameTruncation(t *testing.T) {
	longName := strings.Repeat("a", 100)
	result := truncatePodName(longName)
	assert.Len(t, result, maxPodNameLen)

	shortName := "ph-r-test-slot1"
	assert.Equal(t, shortName, truncatePodName(shortName))
}

// Runner placeholder lands on the cluster-wide runner pool (e.g. c7i-runner)
// which has NO osdc.io/runner-class label or taint. The placeholder must NEVER
// include runner-class in its nodeSelector or tolerations — doing so leaves
// the pod Pending forever. nodeSelector: workload-type, optional node-fleet.
// Tolerations: instance-type Exists, git-cache, optional node-fleet, optional GPU.
// NO workload-type toleration (it's a node label, not a taint).
func TestRunnerPlaceholder_NoGPU_NoFleet_NoRunnerClass(t *testing.T) {
	pm, _ := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "s1"))
	pairs, _ := pm.ListPairs(ctx)
	runner := pairs["s1"].RunnerPod

	assert.Equal(t, "github-runner", runner.Spec.NodeSelector["workload-type"])
	_, hasFleet := runner.Spec.NodeSelector["node-fleet"]
	assert.False(t, hasFleet, "no node-fleet without NodeFleet config")
	_, hasRC := runner.Spec.NodeSelector["osdc.io/runner-class"]
	assert.False(t, hasRC, "runner placeholder must never include runner-class in nodeSelector")

	// Without NodeFleet/GPU: only instance-type + git-cache-not-ready.
	assert.Len(t, runner.Spec.Tolerations, 2)
	tolerationKeys := tolerationKeySet(runner.Spec.Tolerations)
	assert.Contains(t, tolerationKeys, "instance-type")
	assert.Contains(t, tolerationKeys, "git-cache-not-ready")
	assert.NotContains(t, tolerationKeys, "osdc.io/runner-class",
		"runner placeholder must never include runner-class toleration")
}

// Workflow placeholder mirrors the job-pod.yaml ConfigMap template:
// NO hard nodeSelector — uses preferredDuringScheduling node affinity
// with weight 50 for node-fleet + workload-type. Tolerations:
// instance-type Exists, optional node-fleet, optional GPU. NO
// git-cache-not-ready (workflow waits for cache, doesn't tolerate the taint).
func TestWorkflowPlaceholder_NoGPU_NoFleet_NoRunnerClass(t *testing.T) {
	pm, _ := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "s1"))
	pairs, _ := pm.ListPairs(ctx)
	wf := pairs["s1"].WorkflowPod

	assert.Nil(t, wf.Spec.NodeSelector, "workflow has no hard nodeSelector")

	require.NotNil(t, wf.Spec.Affinity)
	require.NotNil(t, wf.Spec.Affinity.NodeAffinity)
	preferred := wf.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	require.NotEmpty(t, preferred)
	assert.Equal(t, int32(50), preferred[len(preferred)-1].Weight)

	// Without NodeFleet/GPU: only instance-type toleration. No git-cache.
	assert.Len(t, wf.Spec.Tolerations, 1)
	assert.Equal(t, "instance-type", wf.Spec.Tolerations[0].Key)
	for _, tol := range wf.Spec.Tolerations {
		assert.NotEqual(t, "git-cache-not-ready", tol.Key,
			"workflow must not tolerate git-cache-not-ready")
	}
}

func TestRunnerPlaceholder_WithGPU_WithFleet_WithRunnerClass(t *testing.T) {
	cfg := Config{
		WorkflowGPU:     2,
		NodeFleet:       "gpu-fleet",
		RunnerNodeFleet: "c7i-runner",
		RunnerClass:     "gpu-large",
	}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "gpu-slot"))
	pairs, _ := pm.ListPairs(ctx)
	runner := pairs["gpu-slot"].RunnerPod

	// Runner has hard nodeSelector for the RUNNER fleet (not the workflow
	// fleet). Runner-class MUST NOT be in the nodeSelector — the runner pool
	// has no runner-class label.
	assert.Equal(t, "c7i-runner", runner.Spec.NodeSelector["node-fleet"],
		"runner placeholder must pin to RunnerNodeFleet, not the workflow NodeFleet")
	_, hasRC := runner.Spec.NodeSelector["osdc.io/runner-class"]
	assert.False(t, hasRC,
		"runner placeholder nodeSelector must never include runner-class, even when RunnerClass is set")
	assert.Equal(t, "github-runner", runner.Spec.NodeSelector["workload-type"])

	// instance-type + git-cache-not-ready + node-fleet + GPU = 4.
	// Runner-class MUST NOT appear as a toleration.
	assert.Len(t, runner.Spec.Tolerations, 4)
	keys := tolerationKeySet(runner.Spec.Tolerations)
	assert.Contains(t, keys, "instance-type")
	assert.Contains(t, keys, "git-cache-not-ready")
	assert.Contains(t, keys, "node-fleet")
	assert.Contains(t, keys, "nvidia.com/gpu")
	assert.NotContains(t, keys, "osdc.io/runner-class",
		"runner placeholder tolerations must never include runner-class, even when RunnerClass is set")

	// GPU toleration uses Exists operator (template uses Exists).
	// node-fleet toleration on the runner pod must carry the RUNNER
	// fleet value — never the workflow fleet.
	// git-cache-not-ready toleration must be operator: Exists (the runner
	// pool carries this as an unconditional startupTaint).
	for _, tol := range runner.Spec.Tolerations {
		switch tol.Key {
		case "nvidia.com/gpu":
			assert.Equal(t, corev1.TolerationOpExists, tol.Operator)
		case "node-fleet":
			assert.Equal(t, "c7i-runner", tol.Value,
				"runner placeholder node-fleet toleration must use RunnerNodeFleet")
			assert.NotEqual(t, "gpu-fleet", tol.Value,
				"runner placeholder must NEVER use the workflow NodeFleet value")
		case "git-cache-not-ready":
			assert.Equal(t, corev1.TolerationOpExists, tol.Operator,
				"git-cache-not-ready toleration must use operator: Exists")
		}
	}
}

func TestWorkflowPlaceholder_WithGPU_WithFleet_WithRunnerClass(t *testing.T) {
	cfg := Config{
		WorkflowGPU: 2,
		NodeFleet:   "gpu-fleet",
		RunnerClass: "gpu-large",
		WorkflowCPU: "4",
	}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "gpu-slot"))
	pairs, _ := pm.ListPairs(ctx)
	wf := pairs["gpu-slot"].WorkflowPod

	// Workflow has no hard nodeSelector regardless of config.
	assert.Nil(t, wf.Spec.NodeSelector)

	// Affinity: required GPU node selector + preferred runner-class + preferred fleet.
	require.NotNil(t, wf.Spec.Affinity)
	require.NotNil(t, wf.Spec.Affinity.NodeAffinity)
	preferred := wf.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	assert.Len(t, preferred, 2, "runner-class preference + fleet preference")
	require.NotNil(t, wf.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
		"GPU workflow has required GPU node selector")

	// Tolerations: instance-type + node-fleet + GPU = 3.
	assert.Len(t, wf.Spec.Tolerations, 3)
	keys := tolerationKeySet(wf.Spec.Tolerations)
	assert.Contains(t, keys, "instance-type")
	assert.Contains(t, keys, "node-fleet")
	assert.Contains(t, keys, "nvidia.com/gpu")
	assert.NotContains(t, keys, "git-cache-not-ready",
		"workflow must not tolerate git-cache-not-ready")

	// Verify GPU resources on workflow pod.
	gpuReq := wf.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"]
	assert.Equal(t, "2", gpuReq.String())
	gpuLim := wf.Spec.Containers[0].Resources.Limits["nvidia.com/gpu"]
	assert.Equal(t, "2", gpuLim.String())
}

func TestWorkflowDiskResource(t *testing.T) {
	cfg := Config{WorkflowDisk: "200Gi"}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "disk-slot"))
	pairs, _ := pm.ListPairs(ctx)
	wf := pairs["disk-slot"].WorkflowPod
	diskReq := wf.Spec.Containers[0].Resources.Requests[corev1.ResourceEphemeralStorage]
	assert.Equal(t, "200Gi", diskReq.String())
	diskLim := wf.Spec.Containers[0].Resources.Limits[corev1.ResourceEphemeralStorage]
	assert.Equal(t, "200Gi", diskLim.String(),
		"setQuantity must mirror Requests into Limits")
}

// Both placeholder pods must carry karpenter.sh/do-not-disrupt=true so
// Karpenter does not consolidate the pre-warmed nodes out from under us.
func TestPlaceholderPods_KarpenterDoNotDisruptAnnotation(t *testing.T) {
	pm, _ := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "ann-slot"))
	pairs, _ := pm.ListPairs(ctx)
	pair := pairs["ann-slot"]

	for _, pod := range []*corev1.Pod{pair.RunnerPod, pair.WorkflowPod} {
		require.NotNil(t, pod)
		assert.Equal(t, "true", pod.Annotations["karpenter.sh/do-not-disrupt"],
			"placeholder pods must opt out of Karpenter consolidation")
	}
}

// Defensive fallback: when PlaceholderTimeout is unset (zero), the placeholder
// container falls back to `sleep infinity`. This preserves behavior for tests
// and any caller that constructs a Config{} without setting the timeout.
func TestPlaceholderPods_SleepCommand_ZeroTimeoutFallsBackToInfinity(t *testing.T) {
	pm, _ := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "cmd-slot"))
	pairs, _ := pm.ListPairs(ctx)
	pair := pairs["cmd-slot"]

	for _, pod := range []*corev1.Pod{pair.RunnerPod, pair.WorkflowPod} {
		require.NotNil(t, pod)
		require.Len(t, pod.Spec.Containers, 1)
		assert.Equal(t, []string{"sleep", "infinity"}, pod.Spec.Containers[0].Command)
	}
}

// When PlaceholderTimeout is set, the placeholder container's sleep argument
// must be PlaceholderTimeout * 1.5 seconds. This is the defensive
// self-terminate that bounds pod lifetime if the listener crashes before
// CleanupAll/CleanupTimedOut run. 5min * 1.5 = 7.5min = 450s.
func TestPlaceholderPods_SleepCommand_UsesTimeoutTimes1_5(t *testing.T) {
	pm, _ := newTestPM(t, Config{PlaceholderTimeout: 5 * time.Minute})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "cmd-slot"))
	pairs, _ := pm.ListPairs(ctx)
	pair := pairs["cmd-slot"]

	for _, pod := range []*corev1.Pod{pair.RunnerPod, pair.WorkflowPod} {
		require.NotNil(t, pod)
		require.Len(t, pod.Spec.Containers, 1)
		assert.Equal(t, []string{"sleep", "450"}, pod.Spec.Containers[0].Command)
	}
}

// Sub-second timeouts truncate to zero seconds; the helper floors to 1 so the
// resulting `sleep` argument is always a valid positive integer.
func TestPlaceholderPods_SleepCommand_FloorsToOneSecond(t *testing.T) {
	pm, _ := newTestPM(t, Config{PlaceholderTimeout: 100 * time.Millisecond})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "cmd-slot"))
	pairs, _ := pm.ListPairs(ctx)
	pair := pairs["cmd-slot"]

	for _, pod := range []*corev1.Pod{pair.RunnerPod, pair.WorkflowPod} {
		require.NotNil(t, pod)
		require.Len(t, pod.Spec.Containers, 1)
		assert.Equal(t, []string{"sleep", "1"}, pod.Spec.Containers[0].Command)
	}
}

// setQuantity must mirror every parsed Request into Limits (equal request/limit
// is the contract documented on the helper). Verify on the runner pod where
// CPU and memory both come from setQuantity.
func TestRunnerPlaceholder_RequestsEqualLimits(t *testing.T) {
	cfg := Config{RunnerCPU: "750m", RunnerMemory: "512Mi"}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "eq-slot"))
	pairs, _ := pm.ListPairs(ctx)
	runner := pairs["eq-slot"].RunnerPod

	res := runner.Spec.Containers[0].Resources
	assert.Equal(t, res.Requests.Cpu().String(), res.Limits.Cpu().String(),
		"runner CPU request and limit must match")
	assert.Equal(t, res.Requests.Memory().String(), res.Limits.Memory().String(),
		"runner memory request and limit must match")
}

// Empty resource strings must be skipped (not parsed) so the resulting
// pod has no Requests/Limits entry for that resource. Regression coverage
// for the setQuantity early-return on empty input.
func TestSetQuantity_EmptyValueOmitsResource(t *testing.T) {
	// All resource strings empty → no Requests / Limits keys set.
	pm, _ := newTestPM(t, Config{})
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "empty-slot"))
	pairs, _ := pm.ListPairs(ctx)

	wf := pairs["empty-slot"].WorkflowPod
	assert.Empty(t, wf.Spec.Containers[0].Resources.Requests,
		"empty workflow resource strings must produce no Requests entries")
	assert.Empty(t, wf.Spec.Containers[0].Resources.Limits,
		"empty workflow resource strings must produce no Limits entries")
}

// An invalid resource quantity must be skipped, not panic. Production code
// switched from resource.MustParse → resource.ParseQuantity precisely to
// degrade gracefully on bad operator input.
func TestSetQuantity_InvalidValueSkippedNotPanic(t *testing.T) {
	cfg := Config{RunnerCPU: "not-a-quantity", RunnerMemory: "512Mi"}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NotPanics(t, func() {
		_ = pm.CreatePair(ctx, "bad-slot")
	})

	pairs, _ := pm.ListPairs(ctx)
	runner := pairs["bad-slot"].RunnerPod
	res := runner.Spec.Containers[0].Resources
	_, hasCPUReq := res.Requests[corev1.ResourceCPU]
	_, hasCPULim := res.Limits[corev1.ResourceCPU]
	assert.False(t, hasCPUReq, "invalid CPU value must not produce a Requests entry")
	assert.False(t, hasCPULim, "invalid CPU value must not produce a Limits entry")
	// Memory is still set (valid value).
	assert.Equal(t, "512Mi", res.Requests.Memory().String())
}

// truncatePodName must (a) yield a name within the 63-char limit, (b) preserve
// uniqueness across long names that share a prefix via a sha256 hex suffix,
// and (c) produce names that are still valid DNS-1123 subdomains.
func TestPodNameTruncation_HashSuffixPreservesUniqueness(t *testing.T) {
	long1 := "ph-r-" + strings.Repeat("x", 80) + "-aaa"
	long2 := "ph-r-" + strings.Repeat("x", 80) + "-bbb"

	got1 := truncatePodName(long1)
	got2 := truncatePodName(long2)

	assert.Len(t, got1, maxPodNameLen)
	assert.Len(t, got2, maxPodNameLen)
	assert.NotEqual(t, got1, got2,
		"distinct long names must produce distinct truncated names via hash suffix")

	// The suffix is `-` plus 8 hex chars (8 = 4 bytes hex-encoded).
	const suffixLen = 9
	suffix1 := got1[len(got1)-suffixLen:]
	assert.Equal(t, "-", suffix1[:1])
	for _, c := range suffix1[1:] {
		assert.True(t,
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"truncated suffix must be lower-case hex, got %q", suffix1)
	}
}

// Split-fleet invariant: with a different RunnerNodeFleet and NodeFleet,
// the runner placeholder must use RunnerNodeFleet for nodeSelector AND
// node-fleet toleration; the workflow placeholder must use NodeFleet for
// the same fields. The two fleets must NEVER be conflated.
func TestPlaceholders_SplitFleet_RunnerAndWorkflowUseDifferentFleets(t *testing.T) {
	cfg := Config{
		NodeFleet:       "g4dn",       // workflow pool (per-scale-set)
		RunnerNodeFleet: "c7i-runner", // runner pool (cluster-wide)
	}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "split-slot"))
	pairs, _ := pm.ListPairs(ctx)
	pair := pairs["split-slot"]
	require.NotNil(t, pair.RunnerPod)
	require.NotNil(t, pair.WorkflowPod)

	// Runner: RunnerNodeFleet drives nodeSelector + node-fleet toleration.
	assert.Equal(t, "c7i-runner", pair.RunnerPod.Spec.NodeSelector["node-fleet"],
		"runner placeholder nodeSelector must use RunnerNodeFleet")
	for _, tol := range pair.RunnerPod.Spec.Tolerations {
		if tol.Key == "node-fleet" {
			assert.Equal(t, "c7i-runner", tol.Value,
				"runner placeholder node-fleet toleration must use RunnerNodeFleet")
		}
	}

	// Workflow: NodeFleet (workflow pool) drives node-fleet toleration —
	// workflow uses soft affinity, NOT a hard nodeSelector.
	assert.Nil(t, pair.WorkflowPod.Spec.NodeSelector,
		"workflow placeholder must not have a hard nodeSelector")
	for _, tol := range pair.WorkflowPod.Spec.Tolerations {
		if tol.Key == "node-fleet" {
			assert.Equal(t, "g4dn", tol.Value,
				"workflow placeholder node-fleet toleration must use NodeFleet (unchanged)")
		}
	}
}

// Runner placeholder must always include the git-cache-not-ready toleration
// with operator: Exists, because the runner pool inherits the unconditional
// startupTaint. This applies even with no fleet/class/GPU configured.
func TestRunnerPlaceholder_GitCacheNotReadyToleration_IsOperatorExists(t *testing.T) {
	cfg := Config{RunnerNodeFleet: "c7i-runner"}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "gc-slot"))
	pairs, _ := pm.ListPairs(ctx)
	runner := pairs["gc-slot"].RunnerPod
	require.NotNil(t, runner)

	var found bool
	for _, tol := range runner.Spec.Tolerations {
		if tol.Key == "git-cache-not-ready" {
			found = true
			assert.Equal(t, corev1.TolerationOpExists, tol.Operator,
				"git-cache-not-ready toleration must use operator: Exists")
			// With Exists operator, Value must be empty (Kubernetes contract).
			assert.Equal(t, "", tol.Value,
				"git-cache-not-ready toleration with operator:Exists must have empty value")
			assert.Equal(t, corev1.TaintEffectNoSchedule, tol.Effect)
		}
	}
	assert.True(t, found,
		"runner placeholder must include git-cache-not-ready toleration")
}

// Workflow placeholder must NEVER use the runner-pool fleet — that would
// collapse the topology separation. With both fleets set, workflow uses
// NodeFleet (per-scale-set, e.g. g4dn) and ignores RunnerNodeFleet entirely.
func TestWorkflowPlaceholder_DoesNotUseRunnerNodeFleet(t *testing.T) {
	cfg := Config{
		NodeFleet:       "g4dn",
		RunnerNodeFleet: "c7i-runner",
	}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "wf-slot"))
	pairs, _ := pm.ListPairs(ctx)
	wf := pairs["wf-slot"].WorkflowPod
	require.NotNil(t, wf)

	// Workflow tolerations must not contain c7i-runner under any key.
	for _, tol := range wf.Spec.Tolerations {
		assert.NotEqual(t, "c7i-runner", tol.Value,
			"workflow placeholder tolerations must NEVER reference RunnerNodeFleet")
	}

	// Workflow affinity preferences must reference NodeFleet (g4dn), not
	// RunnerNodeFleet (c7i-runner).
	require.NotNil(t, wf.Spec.Affinity)
	require.NotNil(t, wf.Spec.Affinity.NodeAffinity)
	for _, term := range wf.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
		for _, expr := range term.Preference.MatchExpressions {
			if expr.Key == "node-fleet" {
				assert.Contains(t, expr.Values, "g4dn",
					"workflow node-fleet affinity must reference NodeFleet")
				assert.NotContains(t, expr.Values, "c7i-runner",
					"workflow node-fleet affinity must NEVER reference RunnerNodeFleet")
			}
		}
	}
}

// Bug regression: the runner pool (e.g. c7i-runner) carries no
// osdc.io/runner-class label or taint, so the placeholder-runner pod must
// NEVER include runner-class in its nodeSelector or tolerations regardless
// of the configured RunnerClass. With runner-class on the placeholder, the
// pod stays Pending forever and no preemption can occur.
//
// This test verifies the invariant across the full {RunnerClass set, unset}
// matrix to lock in the fix.
func TestRunnerPlaceholder_NeverIncludesRunnerClass(t *testing.T) {
	cases := []struct {
		name        string
		runnerClass string
	}{
		{name: "RunnerClass empty", runnerClass: ""},
		{name: "RunnerClass set to release", runnerClass: "release"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				RunnerNodeFleet: "c7i-runner",
				RunnerClass:     tc.runnerClass,
			}
			pm, _ := newTestPM(t, cfg)
			ctx := context.Background()

			require.NoError(t, pm.CreatePair(ctx, "rc-slot"))
			pairs, _ := pm.ListPairs(ctx)
			runner := pairs["rc-slot"].RunnerPod
			require.NotNil(t, runner)

			// nodeSelector must never include runner-class.
			_, hasRC := runner.Spec.NodeSelector["osdc.io/runner-class"]
			assert.False(t, hasRC,
				"runner placeholder nodeSelector must never include osdc.io/runner-class (RunnerClass=%q)",
				tc.runnerClass)

			// tolerations must never include runner-class.
			for _, tol := range runner.Spec.Tolerations {
				assert.NotEqual(t, "osdc.io/runner-class", tol.Key,
					"runner placeholder tolerations must never include osdc.io/runner-class (RunnerClass=%q)",
					tc.runnerClass)
			}
		})
	}
}

// The workflow placeholder MUST still consume RunnerClass via its preferred
// node affinity (weight 100). Only the runner placeholder drops runner-class —
// the workflow pod's scheduling pattern is unchanged.
func TestWorkflowPlaceholder_StillUsesRunnerClassInPreferredAffinity(t *testing.T) {
	cfg := Config{
		NodeFleet:   "g4dn",
		RunnerClass: "release",
	}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "wf-rc-slot"))
	pairs, _ := pm.ListPairs(ctx)
	wf := pairs["wf-rc-slot"].WorkflowPod
	require.NotNil(t, wf)
	require.NotNil(t, wf.Spec.Affinity)
	require.NotNil(t, wf.Spec.Affinity.NodeAffinity)

	var foundRC bool
	for _, term := range wf.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
		for _, expr := range term.Preference.MatchExpressions {
			if expr.Key == "osdc.io/runner-class" {
				foundRC = true
				assert.Equal(t, int32(100), term.Weight,
					"workflow runner-class preferred affinity weight must be 100")
				assert.Contains(t, expr.Values, "release")
			}
		}
	}
	assert.True(t, foundRC,
		"workflow placeholder must still include runner-class in preferred affinity")
}

func tolerationKeySet(tolerations []corev1.Toleration) map[string]struct{} {
	s := make(map[string]struct{}, len(tolerations))
	for _, t := range tolerations {
		s[t.Key] = struct{}{}
	}
	return s
}
