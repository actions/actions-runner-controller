package scaler

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
)

// ResourceChecker decides whether the cluster has enough resources to run count runners.
type ResourceChecker interface {
	HasSufficientResources(ctx context.Context, count int) (bool, error)
}

// ersGetter abstracts fetching an EphemeralRunnerSet so tests can inject a fake.
type ersGetter func(ctx context.Context, ns, name string) (*v1alpha1.EphemeralRunnerSet, error)

// KubernetesResourceChecker implements ResourceChecker against a live cluster.
type KubernetesResourceChecker struct {
	clientset              kubernetes.Interface
	restClient             rest.Interface
	ephemeralRunnerSetNS   string
	ephemeralRunnerSetName string
	logger                 *slog.Logger
	// ersGetter is set to a real k8s REST call in production; overridable in tests.
	ersGetter ersGetter
}

// NewKubernetesResourceChecker constructs a checker wired to the live k8s API.
func NewKubernetesResourceChecker(
	cs *kubernetes.Clientset,
	ns, name string,
	logger *slog.Logger,
) *KubernetesResourceChecker {
	c := &KubernetesResourceChecker{
		clientset:              cs,
		restClient:             cs.RESTClient(),
		ephemeralRunnerSetNS:   ns,
		ephemeralRunnerSetName: name,
		logger:                 logger,
	}
	c.ersGetter = c.fetchERS
	return c
}

func (c *KubernetesResourceChecker) fetchERS(ctx context.Context, ns, name string) (*v1alpha1.EphemeralRunnerSet, error) {
	ers := &v1alpha1.EphemeralRunnerSet{}
	err := c.restClient.
		Get().
		Prefix("apis", v1alpha1.GroupVersion.Group, v1alpha1.GroupVersion.Version).
		Namespace(ns).
		Resource("ephemeralrunnersets").
		Name(name).
		Do(ctx).
		Into(ers)
	return ers, err
}

// HasSufficientResources returns true if the cluster has enough resources to
// schedule count additional runners. Errors are returned so the caller can
// fail-open.
func (c *KubernetesResourceChecker) HasSufficientResources(ctx context.Context, count int) (bool, error) {
	ers, err := c.ersGetter(ctx, c.ephemeralRunnerSetNS, c.ephemeralRunnerSetName)
	if err != nil {
		return false, fmt.Errorf("fetch EphemeralRunnerSet: %w", err)
	}

	containers := ers.Spec.EphemeralRunnerSpec.Spec.Containers
	if len(containers) == 0 || len(containers[0].Resources.Requests) == 0 {
		return true, nil
	}
	runnerRequests := containers[0].Resources.Requests
	nodeSelector := ers.Spec.EphemeralRunnerSpec.Spec.NodeSelector

	// Filter target nodes
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("list nodes: %w", err)
	}
	targetNodes := filterNodes(nodeList.Items, nodeSelector)
	targetNodeNames := make(map[string]struct{}, len(targetNodes))
	for _, n := range targetNodes {
		targetNodeNames[n.Name] = struct{}{}
	}

	// Sum allocatable across target nodes
	clusterAllocatable := sumResourceLists(targetNodes)

	// Sum used resources (Running/Pending pods on target nodes + unbound Pending)
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("list pods: %w", err)
	}
	usedResources := sumPodRequests(podList.Items, targetNodeNames)

	// Decision: check each requested resource
	available := subtractResources(clusterAllocatable, usedResources)
	for resName, req := range runnerRequests {
		avail := available[resName]
		needed := multiplyQuantity(req, count)
		if needed.Cmp(avail) > 0 {
			c.logger.Info("Insufficient resources, rejecting scale-up",
				"resource", resName,
				"requested", needed.String(),
				"available", avail.String(),
			)
			return false, nil
		}
	}
	return true, nil
}

func filterNodes(nodes []corev1.Node, selector map[string]string) []corev1.Node {
	if len(selector) == 0 {
		return nodes
	}
	var result []corev1.Node
	for _, n := range nodes {
		if labelsMatch(n.Labels, selector) {
			result = append(result, n)
		}
	}
	return result
}

func labelsMatch(nodeLabels, selector map[string]string) bool {
	for k, v := range selector {
		if nodeLabels[k] != v {
			return false
		}
	}
	return true
}

func sumResourceLists(nodes []corev1.Node) map[corev1.ResourceName]resource.Quantity {
	total := make(map[corev1.ResourceName]resource.Quantity)
	for _, n := range nodes {
		for res, qty := range n.Status.Allocatable {
			sum := total[res].DeepCopy()
			sum.Add(qty)
			total[res] = sum
		}
	}
	return total
}

func sumPodRequests(pods []corev1.Pod, targetNodes map[string]struct{}) map[corev1.ResourceName]resource.Quantity {
	total := make(map[corev1.ResourceName]resource.Quantity)
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
		_, onTarget := targetNodes[pod.Spec.NodeName]
		isUnbound := pod.Spec.NodeName == ""
		if !onTarget && !isUnbound {
			continue
		}
		for _, c := range pod.Spec.Containers {
			for res, qty := range c.Resources.Requests {
				sum := total[res].DeepCopy()
				sum.Add(qty)
				total[res] = sum
			}
		}
	}
	return total
}

func subtractResources(
	allocatable, used map[corev1.ResourceName]resource.Quantity,
) map[corev1.ResourceName]resource.Quantity {
	result := make(map[corev1.ResourceName]resource.Quantity, len(allocatable))
	for res, qty := range allocatable {
		avail := qty.DeepCopy()
		if u, ok := used[res]; ok {
			avail.Sub(u)
		}
		result[res] = avail
	}
	return result
}

func multiplyQuantity(q resource.Quantity, n int) resource.Quantity {
	result := resource.NewMilliQuantity(0, q.Format)
	for i := 0; i < n; i++ {
		result.Add(q)
	}
	return *result
}
