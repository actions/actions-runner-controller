package scaler

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	arcconst "github.com/actions/actions-runner-controller/controllers/actions.github.com"
)

const (
	testNS     = "test-ns"
	testRSName = "test-runner-set"
)

// buildFakeClientset creates a fake k8s clientset pre-populated with the given objects.
func buildFakeClientset(objs ...runtime.Object) *fake.Clientset {
	return fake.NewClientset(objs...)
}

// buildEphemeralRunnerSet returns a minimal EphemeralRunnerSet with the given
// annotations and nodeSelector.
func buildEphemeralRunnerSet(annotations map[string]string, nodeSelector map[string]string) *v1alpha1.EphemeralRunnerSet {
	return &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testRSName,
			Namespace:   testNS,
			Annotations: annotations,
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				PodTemplateSpec: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						NodeSelector: nodeSelector,
						Containers: []corev1.Container{
							{Name: "runner"},
						},
					},
				},
			},
		},
	}
}

// buildNode returns a Node with the given allocatable resources and labels.
func buildNode(name string, allocatable corev1.ResourceList, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status:     corev1.NodeStatus{Allocatable: allocatable},
	}
}

// buildPod returns a Running pod bound to nodeName with the given resource requests.
func buildPod(name, nodeName string, phase corev1.PodPhase, requests corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{Resources: corev1.ResourceRequirements{Requests: requests}},
			},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

// fakeERSGetter returns an ersGetter that always returns the given EphemeralRunnerSet.
func fakeERSGetter(ers *v1alpha1.EphemeralRunnerSet) ersGetter {
	return func(_ context.Context, _, _ string) (*v1alpha1.EphemeralRunnerSet, error) {
		return ers, nil
	}
}

func TestHasSufficientResources_SufficientCPUAndMemory(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU:    "1",
		arcconst.AnnotationKeyJobMemory: "1Gi",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("16Gi"),
	}, nil)

	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset:              cs,
		ephemeralRunnerSetNS:   testNS,
		ephemeralRunnerSetName: testRSName,
		logger:                 discardLogger,
		ersGetter:              fakeERSGetter(ers),
	}

	ok, err := checker.HasSufficientResources(context.Background(), 4)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHasSufficientResources_CustomResourceSufficient(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "1",
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:1",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU:                    resource.MustParse("8"),
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("4"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHasSufficientResources_CustomResourceInsufficient(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:1",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("3"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4) // needs 4, only 3
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSufficientResources_InsufficientCPU(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "4",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("8"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 3) // needs 12, only 8
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSufficientResources_InsufficientMemory(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobMemory: "4Gi",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 3) // needs 12Gi, only 8Gi
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSufficientResources_NodeSelectorSufficient(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "1",
	}, map[string]string{"arch": "arm64"})
	arm := buildNode("arm-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("8"),
	}, map[string]string{"arch": "arm64"})
	x86 := buildNode("x86-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}, map[string]string{"arch": "amd64"})
	cs := buildFakeClientset(arm, x86)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4) // 4 × 1 CPU <= 8
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHasSufficientResources_NodeSelectorInsufficient(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "1",
	}, map[string]string{"arch": "arm64"})
	arm := buildNode("arm-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}, map[string]string{"arch": "arm64"})
	cs := buildFakeClientset(arm)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4) // needs 4, only 2
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSufficientResources_RunningPodConsuming(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("8"),
	}, nil)
	runningPod := buildPod("running-pod", "node1", corev1.PodRunning, corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("6"),
	})
	cs := buildFakeClientset(node, runningPod)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 2) // needs 4, available 8-6=2
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSufficientResources_NoLabels(t *testing.T) {
	ers := buildEphemeralRunnerSet(nil, nil) // no job resource labels
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 5)
	require.NoError(t, err)
	assert.True(t, ok) // no labels → skip check
}

func TestHasSufficientResources_OnlyCPULabelChecked(t *testing.T) {
	// Only job-cpu annotation set; memory not declared so not checked even if tight.
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "1",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("1Ki"), // extremely small, but not checked
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4)
	require.NoError(t, err)
	assert.True(t, ok) // memory not declared → not checked
}

func TestHasSufficientResources_JobArchFiltersSufficientNodes(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU:  "1",
		arcconst.AnnotationKeyJobArch: "arm64",
	}, nil)
	arm := buildNode("arm-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("8"),
	}, map[string]string{"kubernetes.io/arch": "arm64"})
	x86 := buildNode("x86-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("100"),
	}, map[string]string{"kubernetes.io/arch": "amd64"})
	cs := buildFakeClientset(arm, x86)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4) // 4×1 <= 8 (arm only)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHasSufficientResources_JobArchFiltersInsufficientNodes(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU:  "1",
		arcconst.AnnotationKeyJobArch: "arm64",
	}, nil)
	arm := buildNode("arm-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}, map[string]string{"kubernetes.io/arch": "arm64"})
	x86 := buildNode("x86-node", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("100"),
	}, map[string]string{"kubernetes.io/arch": "amd64"})
	cs := buildFakeClientset(arm, x86)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 4) // needs 4, arm only has 2
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHasSufficientResources_InvalidResourceCountLabel(t *testing.T) {
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:not-a-number",
	}, nil)
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	_, err := checker.HasSufficientResources(context.Background(), 1)
	assert.Error(t, err)
}

func TestHasSufficientResources_MissingResourceCountLabel(t *testing.T) {
	// job-npu with missing colon separator → error
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu",
	}, nil)
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	_, err := checker.HasSufficientResources(context.Background(), 1)
	assert.Error(t, err)
}

func TestHasSufficientResources_ERSFetchError(t *testing.T) {
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: func(_ context.Context, _, _ string) (*v1alpha1.EphemeralRunnerSet, error) {
			return nil, fmt.Errorf("not found")
		},
	}
	_, err := checker.HasSufficientResources(context.Background(), 1)
	assert.Error(t, err) // caller should fail-open
}

// --- AdjustCount tests ---

func TestAdjustCount_PartialAllocation(t *testing.T) {
	// 7 NPU available, each runner needs 2 → can fit 3, not 4
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("7"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	n, err := checker.AdjustCount(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, 3, n) // floor(7/2) = 3
}

func TestAdjustCount_FullAllocation(t *testing.T) {
	// 8 NPU available, each runner needs 2 → all 4 fit
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("8"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	n, err := checker.AdjustCount(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
}

func TestAdjustCount_ZeroWhenNoResources(t *testing.T) {
	// 1 NPU available, each runner needs 2 → 0 fit
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("1"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	n, err := checker.AdjustCount(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestAdjustCount_BottleneckResourceLimits(t *testing.T) {
	// CPU allows 8 runners, NPU allows 3 → bottleneck is NPU → 3
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobCPU: "1",
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU:                    resource.MustParse("8"),
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("7"),
	}, nil)
	cs := buildFakeClientset(node)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	n, err := checker.AdjustCount(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 3, n) // min(8, floor(7/2)=3) = 3
}

func TestAdjustCount_NoAnnotations_ReturnsRequestedCount(t *testing.T) {
	// no annotations → skip check → return original count
	ers := buildEphemeralRunnerSet(nil, nil)
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	n, err := checker.AdjustCount(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
}

func TestAdjustCount_ERSFetchError(t *testing.T) {
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: func(_ context.Context, _, _ string) (*v1alpha1.EphemeralRunnerSet, error) {
			return nil, fmt.Errorf("not found")
		},
	}
	_, err := checker.AdjustCount(context.Background(), 1)
	assert.Error(t, err)
}

func TestAdjustCount_RunningRunnersCountedTowardTotal(t *testing.T) {
	// Bug scenario: 2 runners already running (using 4 NPU), available=4 NPU.
	// GitHub wants 4 runners (2 running + 2 queued).
	// floor(available/2) = 2 additional → total feasible = 2+2 = 4, not 2.
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("8"),
	}, nil)
	// 2 runner pods already running in the ERS namespace, each using 2 NPU
	runner1 := buildPod("runner-1", "node1", corev1.PodRunning, corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("2"),
	})
	runner1.Namespace = testNS
	runner2 := buildPod("runner-2", "node1", corev1.PodRunning, corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("2"),
	})
	runner2.Namespace = testNS

	cs := buildFakeClientset(node, runner1, runner2)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	// GitHub says 4 jobs assigned (2 running + 2 queued)
	n, err := checker.AdjustCount(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, 4, n) // 2 running + floor((8-4)/2)=2 additional = 4
}

func TestAdjustCount_RunningRunnersPartialAdditional(t *testing.T) {
	// 2 runners running (4 NPU used), only 3 NPU left → 1 additional.
	// total feasible = 2 + 1 = 3, GitHub wants 4 → adjusted = 3.
	ers := buildEphemeralRunnerSet(map[string]string{
		arcconst.AnnotationKeyJobNPU: "huawei.com/npu:2",
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("7"),
	}, nil)
	runner1 := buildPod("runner-1", "node1", corev1.PodRunning, corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("2"),
	})
	runner1.Namespace = testNS
	runner2 := buildPod("runner-2", "node1", corev1.PodRunning, corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("2"),
	})
	runner2.Namespace = testNS

	cs := buildFakeClientset(node, runner1, runner2)
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	n, err := checker.AdjustCount(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, 3, n) // 2 running + floor(3/2)=1 additional = 3
}
