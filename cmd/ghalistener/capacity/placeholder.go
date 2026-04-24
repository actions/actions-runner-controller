package capacity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	placeholderImage = "public.ecr.aws/docker/library/alpine:3.21"

	labelManagedBy       = "app.kubernetes.io/managed-by"
	labelScaleSet        = "actions.github.com/scale-set-name"
	labelPlaceholderID   = "capacity.actions.github.com/slot-id"
	labelPlaceholderRole = "capacity.actions.github.com/role"
	labelListenerPod     = "capacity.actions.github.com/listener-pod"

	managedByValue = "capacity-monitor"

	rolePlaceholderRunner   = "placeholder-runner"
	rolePlaceholderWorkflow = "placeholder-workflow"

	maxPodNameLen = 63
)

// PlaceholderPair groups the runner and workflow placeholder pods for a
// single pre-warmed slot.
type PlaceholderPair struct {
	SlotID      string
	RunnerPod   *corev1.Pod
	WorkflowPod *corev1.Pod
	CreatedAt   time.Time
}

// BothRunning returns true when both pods in the pair are in the
// Running phase.
func (p *PlaceholderPair) BothRunning() bool {
	return p.RunnerPod != nil && p.WorkflowPod != nil &&
		p.RunnerPod.Status.Phase == corev1.PodRunning &&
		p.WorkflowPod.Status.Phase == corev1.PodRunning
}

// AnyPendingTooLong returns true if either pod has been in the Pending
// phase longer than timeout.
func (p *PlaceholderPair) AnyPendingTooLong(timeout time.Duration) bool {
	now := time.Now()
	for _, pod := range []*corev1.Pod{p.RunnerPod, p.WorkflowPod} {
		if pod == nil {
			continue
		}
		if pod.Status.Phase == corev1.PodPending &&
			now.Sub(pod.CreationTimestamp.Time) > timeout {
			return true
		}
	}
	return false
}

// PlaceholderManager creates, lists, and cleans up placeholder pod
// pairs via the Kubernetes API.
type PlaceholderManager struct {
	clientset  kubernetes.Interface
	namespace  string
	listenerID string // listener pod name, used for label-based ownership
	config     Config
	logger     *slog.Logger
}

// NewPlaceholderManager creates a new manager.
func NewPlaceholderManager(
	clientset kubernetes.Interface,
	namespace string,
	listenerID string,
	config Config,
	logger *slog.Logger,
) *PlaceholderManager {
	return &PlaceholderManager{
		clientset:  clientset,
		namespace:  namespace,
		listenerID: listenerID,
		config:     config,
		logger:     logger.With("component", "placeholder-manager"),
	}
}

// CreatePair creates a placeholder-runner and placeholder-workflow pod
// pair. Both pods are scheduled independently (landing-place agnostic)
// to reserve cluster-level capacity.
func (pm *PlaceholderManager) CreatePair(ctx context.Context, slotID string) error {
	runnerPod := pm.buildRunnerPlaceholder(slotID)
	if _, err := pm.clientset.CoreV1().Pods(pm.namespace).Create(ctx, runnerPod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating runner placeholder: %w", err)
	}

	workflowPod := pm.buildWorkflowPlaceholder(slotID)
	if _, err := pm.clientset.CoreV1().Pods(pm.namespace).Create(ctx, workflowPod, metav1.CreateOptions{}); err != nil {
		// Best-effort cleanup of the runner pod we already created.
		_ = pm.clientset.CoreV1().Pods(pm.namespace).Delete(ctx, runnerPod.Name, metav1.DeleteOptions{})
		return fmt.Errorf("creating workflow placeholder: %w", err)
	}
	return nil
}

// DeletePair deletes both pods in a placeholder pair by slot ID.
func (pm *PlaceholderManager) DeletePair(ctx context.Context, slotID string) error {
	sel := fmt.Sprintf("%s=%s,%s=%s,%s=%s",
		labelManagedBy, managedByValue,
		labelListenerPod, pm.listenerID,
		labelPlaceholderID, slotID,
	)
	return pm.clientset.CoreV1().Pods(pm.namespace).DeleteCollection(
		ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: sel},
	)
}

// ListPairs returns all placeholder pairs owned by this listener,
// grouped by slot ID.
func (pm *PlaceholderManager) ListPairs(ctx context.Context) (map[string]*PlaceholderPair, error) {
	sel := fmt.Sprintf("%s=%s,%s=%s",
		labelManagedBy, managedByValue,
		labelListenerPod, pm.listenerID,
	)
	pods, err := pm.clientset.CoreV1().Pods(pm.namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, fmt.Errorf("listing placeholder pods: %w", err)
	}
	return groupBySlot(pods.Items), nil
}

// CleanupTimedOut deletes pairs where any pod has been Pending longer
// than PlaceholderTimeout. Returns the number of pairs deleted.
func (pm *PlaceholderManager) CleanupTimedOut(ctx context.Context) (int, error) {
	pairs, err := pm.ListPairs(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for slotID, pair := range pairs {
		if pair.AnyPendingTooLong(pm.config.PlaceholderTimeout) {
			if err := pm.DeletePair(ctx, slotID); err != nil {
				pm.logger.Error("failed to delete timed-out pair", "slotID", slotID, "error", err)
				continue
			}
			deleted++
		}
	}
	return deleted, nil
}

// CleanupOrphans deletes placeholder pods from previous listener
// instances of the SAME scale set (different listener-pod label value
// but same scale-set-name label). Scoping to scale set prevents
// listeners from deleting each other's placeholders.
func (pm *PlaceholderManager) CleanupOrphans(ctx context.Context) error {
	sel := fmt.Sprintf("%s=%s,%s=%s,%s!=%s",
		labelManagedBy, managedByValue,
		labelScaleSet, pm.config.ScaleSetName,
		labelListenerPod, pm.listenerID,
	)
	return pm.clientset.CoreV1().Pods(pm.namespace).DeleteCollection(
		ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: sel},
	)
}

// CleanupAll deletes all placeholder pods owned by this listener.
func (pm *PlaceholderManager) CleanupAll(ctx context.Context) error {
	sel := fmt.Sprintf("%s=%s,%s=%s",
		labelManagedBy, managedByValue,
		labelListenerPod, pm.listenerID,
	)
	return pm.clientset.CoreV1().Pods(pm.namespace).DeleteCollection(
		ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: sel},
	)
}

// ---- pod builders ----

// setQuantity safely parses raw and assigns it to both Requests[name] and
// Limits[name] (mirroring the template, which sets equal request/limit).
// Logs and skips on parse error rather than panicking.
func (pm *PlaceholderManager) setQuantity(
	res *corev1.ResourceRequirements,
	name corev1.ResourceName,
	raw string,
	configKey string,
) {
	if raw == "" {
		return
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		pm.logger.Warn("invalid resource quantity, skipping",
			"configKey", configKey, "value", raw, "error", err)
		return
	}
	res.Requests[name] = q
	res.Limits[name] = q
}

// buildRunnerPlaceholder constructs a placeholder pod that mirrors the
// runner pod spec defined in modules/arc-runners/templates/runner.yaml.tpl
// (the `template:` section). Specs MUST stay in sync with that template.
func (pm *PlaceholderManager) buildRunnerPlaceholder(slotID string) *corev1.Pod {
	name := truncatePodName(fmt.Sprintf("ph-r-%s-%s", pm.config.ScaleSetName, slotID))

	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	pm.setQuantity(&resources, corev1.ResourceCPU, pm.config.RunnerCPU, "RunnerCPU")
	pm.setQuantity(&resources, corev1.ResourceMemory, pm.config.RunnerMemory, "RunnerMemory")

	// Runner nodeSelector: workload-type, node-fleet, optional runner-class.
	nodeSelector := map[string]string{
		"workload-type": "github-runner",
	}
	if pm.config.NodeFleet != "" {
		nodeSelector["node-fleet"] = pm.config.NodeFleet
	}
	if pm.config.RunnerClass != "" {
		nodeSelector["osdc.io/runner-class"] = pm.config.RunnerClass
	}

	// Runner tolerations: node-fleet, instance-type Exists, git-cache-not-ready,
	// plus optional GPU and runner-class. NOT workload-type (that's a node label).
	tolerations := []corev1.Toleration{
		{
			Key:      "instance-type",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      "git-cache-not-ready",
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	if pm.config.NodeFleet != "" {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "node-fleet",
			Operator: corev1.TolerationOpEqual,
			Value:    pm.config.NodeFleet,
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}
	if pm.config.WorkflowGPU > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "nvidia.com/gpu",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}
	if pm.config.RunnerClass != "" {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "osdc.io/runner-class",
			Operator: corev1.TolerationOpEqual,
			Value:    pm.config.RunnerClass,
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	pod := pm.placeholderPodShell(name, slotID, rolePlaceholderRunner, resources)
	pod.Spec.NodeSelector = nodeSelector
	pod.Spec.Tolerations = tolerations
	pod.Spec.PriorityClassName = "placeholder-runner"
	return pod
}

// buildWorkflowPlaceholder constructs a placeholder pod that mirrors the
// workflow/job pod spec defined in modules/arc-runners/templates/runner.yaml.tpl
// (the `job-pod.yaml:` section inside the ConfigMap). Specs MUST stay in
// sync with that template.
func (pm *PlaceholderManager) buildWorkflowPlaceholder(slotID string) *corev1.Pod {
	name := truncatePodName(fmt.Sprintf("ph-w-%s-%s", pm.config.ScaleSetName, slotID))

	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	pm.setQuantity(&resources, corev1.ResourceCPU, pm.config.WorkflowCPU, "WorkflowCPU")
	pm.setQuantity(&resources, corev1.ResourceMemory, pm.config.WorkflowMemory, "WorkflowMemory")
	pm.setQuantity(&resources, corev1.ResourceEphemeralStorage, pm.config.WorkflowDisk, "WorkflowDisk")
	if pm.config.WorkflowGPU > 0 {
		// GPU count is an int — formatted as a plain integer string,
		// which is always valid for a resource.Quantity, so safe to parse.
		pm.setQuantity(&resources, "nvidia.com/gpu",
			strconv.Itoa(pm.config.WorkflowGPU), "WorkflowGPU")
	}

	// Workflow tolerations: node-fleet, instance-type Exists, optional GPU.
	// NO git-cache-not-ready (matches template — workflow waits for cache).
	tolerations := []corev1.Toleration{
		{
			Key:      "instance-type",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	if pm.config.NodeFleet != "" {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "node-fleet",
			Operator: corev1.TolerationOpEqual,
			Value:    pm.config.NodeFleet,
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}
	if pm.config.WorkflowGPU > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "nvidia.com/gpu",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	pod := pm.placeholderPodShell(name, slotID, rolePlaceholderWorkflow, resources)
	// Workflow pods use soft affinity (preferredDuringScheduling), no hard nodeSelector.
	pod.Spec.Affinity = pm.buildWorkflowAffinity()
	pod.Spec.Tolerations = tolerations
	pod.Spec.PriorityClassName = "placeholder-workflow"
	return pod
}

// buildWorkflowAffinity builds the soft node affinity for workflow placeholders,
// mirroring the job-pod template:
//   - weight 50 preference for node-fleet + workload-type
//   - optional weight 100 preference for runner-class (when set)
//   - optional GPU node selector requirement (when WorkflowGPU > 0)
func (pm *PlaceholderManager) buildWorkflowAffinity() *corev1.Affinity {
	preferredTerms := []corev1.PreferredSchedulingTerm{}

	// Optional runner-class preference (template uses higher weight for class match).
	if pm.config.RunnerClass != "" {
		preferredTerms = append(preferredTerms, corev1.PreferredSchedulingTerm{
			Weight: 100,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      "osdc.io/runner-class",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{pm.config.RunnerClass},
					},
				},
			},
		})
	}

	// Same-fleet preference (always present, weight 50 per template).
	fleetMatchExpressions := []corev1.NodeSelectorRequirement{
		{
			Key:      "workload-type",
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"github-runner"},
		},
	}
	if pm.config.NodeFleet != "" {
		fleetMatchExpressions = append([]corev1.NodeSelectorRequirement{
			{
				Key:      "node-fleet",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{pm.config.NodeFleet},
			},
		}, fleetMatchExpressions...)
	}
	preferredTerms = append(preferredTerms, corev1.PreferredSchedulingTerm{
		Weight: 50,
		Preference: corev1.NodeSelectorTerm{
			MatchExpressions: fleetMatchExpressions,
		},
	})

	affinity := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: preferredTerms,
		},
	}

	// Optional hard GPU node selector requirement.
	if pm.config.WorkflowGPU > 0 {
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "nvidia.com/gpu.present",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{"true"},
						},
					},
				},
			},
		}
	}

	return affinity
}

// placeholderPodShell returns a Pod with metadata, labels, and a single
// sleep container. Caller is responsible for setting NodeSelector,
// Tolerations, Affinity, and PriorityClassName as appropriate.
func (pm *PlaceholderManager) placeholderPodShell(
	name, slotID, role string,
	resources corev1.ResourceRequirements,
) *corev1.Pod {
	var zero int64
	labels := map[string]string{
		labelManagedBy:       managedByValue,
		labelScaleSet:        pm.config.ScaleSetName,
		labelPlaceholderID:   slotID,
		labelPlaceholderRole: role,
		labelListenerPod:     pm.listenerID,
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pm.namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"karpenter.sh/do-not-disrupt": "true",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:      "placeholder",
					Image:     placeholderImage,
					Command:   []string{"sleep", "infinity"},
					Resources: resources,
				},
			},
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: &zero,
		},
	}
}

// ---- helpers ----

func groupBySlot(pods []corev1.Pod) map[string]*PlaceholderPair {
	pairs := make(map[string]*PlaceholderPair)
	for i := range pods {
		pod := &pods[i]
		slotID := pod.Labels[labelPlaceholderID]
		if slotID == "" {
			continue
		}
		pair, ok := pairs[slotID]
		if !ok {
			pair = &PlaceholderPair{
				SlotID:    slotID,
				CreatedAt: pod.CreationTimestamp.Time,
			}
			pairs[slotID] = pair
		}
		if pod.CreationTimestamp.Time.Before(pair.CreatedAt) {
			pair.CreatedAt = pod.CreationTimestamp.Time
		}
		switch pod.Labels[labelPlaceholderRole] {
		case rolePlaceholderRunner:
			pair.RunnerPod = pod
		case rolePlaceholderWorkflow:
			pair.WorkflowPod = pod
		}
	}
	return pairs
}

// truncatePodName ensures the name fits the K8s 63-char pod name limit.
// When truncation is needed, it appends an 8-char sha256 hex suffix of
// the original name to preserve uniqueness across collisions.
func truncatePodName(name string) string {
	if len(name) <= maxPodNameLen {
		return name
	}
	hash := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(hash[:4]) // 8 hex chars
	return name[:maxPodNameLen-9] + "-" + suffix
}
