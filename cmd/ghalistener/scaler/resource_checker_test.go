package scaler

import (
	"context"
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
