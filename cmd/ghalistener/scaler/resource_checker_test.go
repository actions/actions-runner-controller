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
// container resource requests and nodeSelector.
func buildEphemeralRunnerSet(requests corev1.ResourceList, nodeSelector map[string]string) *v1alpha1.EphemeralRunnerSet {
	return &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testRSName,
			Namespace: testNS,
		},
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				PodTemplateSpec: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						NodeSelector: nodeSelector,
						Containers: []corev1.Container{
							{
								Name: "runner",
								Resources: corev1.ResourceRequirements{
									Requests: requests,
								},
							},
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
	ers := buildEphemeralRunnerSet(
		corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
		nil,
	)
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceCPU:                    resource.MustParse("1"),
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("1"),
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceName("huawei.com/npu"): resource.MustParse("1"),
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("4"),
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("4Gi"),
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("1"),
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("1"),
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
	ers := buildEphemeralRunnerSet(corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	}, nil)
	node := buildNode("node1", corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("8"),
	}, nil)
	// Running pod consuming 6 CPU on the target node
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

func TestHasSufficientResources_NoRequests(t *testing.T) {
	ers := buildEphemeralRunnerSet(nil, nil) // no resource requests
	cs := buildFakeClientset()
	checker := &KubernetesResourceChecker{
		clientset: cs, ephemeralRunnerSetNS: testNS,
		ephemeralRunnerSetName: testRSName, logger: discardLogger,
		ersGetter: fakeERSGetter(ers),
	}
	ok, err := checker.HasSufficientResources(context.Background(), 5)
	require.NoError(t, err)
	assert.True(t, ok) // skip check when no requests defined
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
