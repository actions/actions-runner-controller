package capacity

import (
	"context"
	"strings"
	"testing"

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

// Runner placeholder mirrors the runner.yaml.tpl template's `template:`
// section: hard nodeSelector with workload-type, optional node-fleet,
// optional runner-class. Tolerations: instance-type Exists, git-cache,
// optional node-fleet, optional GPU, optional runner-class. NO
// workload-type toleration (it's a node label, not a taint).
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
	assert.False(t, hasRC, "no runner-class without RunnerClass config")

	// Without NodeFleet/GPU/RunnerClass: only instance-type + git-cache-not-ready.
	assert.Len(t, runner.Spec.Tolerations, 2)
	tolerationKeys := tolerationKeySet(runner.Spec.Tolerations)
	assert.Contains(t, tolerationKeys, "instance-type")
	assert.Contains(t, tolerationKeys, "git-cache-not-ready")
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
		WorkflowGPU: 2,
		NodeFleet:   "gpu-fleet",
		RunnerClass: "gpu-large",
	}
	pm, _ := newTestPM(t, cfg)
	ctx := context.Background()

	require.NoError(t, pm.CreatePair(ctx, "gpu-slot"))
	pairs, _ := pm.ListPairs(ctx)
	runner := pairs["gpu-slot"].RunnerPod

	// Runner has hard nodeSelector for fleet + class.
	assert.Equal(t, "gpu-fleet", runner.Spec.NodeSelector["node-fleet"])
	assert.Equal(t, "gpu-large", runner.Spec.NodeSelector["osdc.io/runner-class"])
	assert.Equal(t, "github-runner", runner.Spec.NodeSelector["workload-type"])

	// instance-type + git-cache-not-ready + node-fleet + GPU + runner-class = 5.
	assert.Len(t, runner.Spec.Tolerations, 5)
	keys := tolerationKeySet(runner.Spec.Tolerations)
	assert.Contains(t, keys, "instance-type")
	assert.Contains(t, keys, "git-cache-not-ready")
	assert.Contains(t, keys, "node-fleet")
	assert.Contains(t, keys, "nvidia.com/gpu")
	assert.Contains(t, keys, "osdc.io/runner-class")

	// GPU toleration uses Exists operator (template uses Exists).
	for _, tol := range runner.Spec.Tolerations {
		if tol.Key == "nvidia.com/gpu" {
			assert.Equal(t, corev1.TolerationOpExists, tol.Operator)
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

// Both placeholder containers must run `sleep infinity` (not `sleep 900`).
// A finite sleep would let the placeholder exit and free the node, defeating
// the purpose of pre-warming.
func TestPlaceholderPods_SleepInfinityCommand(t *testing.T) {
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

func tolerationKeySet(tolerations []corev1.Toleration) map[string]struct{} {
	s := make(map[string]struct{}, len(tolerations))
	for _, t := range tolerations {
		s[t.Key] = struct{}{}
	}
	return s
}
