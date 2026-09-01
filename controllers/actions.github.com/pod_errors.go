package actionsgithubcom

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// PodContainerError represents a detected container-level error from a pod's status.
type PodContainerError struct {
	ContainerName string
	Reason        string
	Message       string
	Category      string
	IsInit        bool
}

func (e *PodContainerError) String() string {
	prefix := "container"
	if e.IsInit {
		prefix = "init-container"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s %q: %s: %s", prefix, e.ContainerName, e.Reason, e.Message)
	}
	return fmt.Sprintf("%s %q: %s", prefix, e.ContainerName, e.Reason)
}

// extractPodContainerErrors inspects pod container statuses for terminal error states.
// It checks both waiting and terminated states across init containers and regular containers.
// Returns nil if no terminal errors are detected.
func extractPodContainerErrors(pod *corev1.Pod) []PodContainerError {
	var errors []PodContainerError

	for i := range pod.Status.InitContainerStatuses {
		cs := &pod.Status.InitContainerStatuses[i]
		if err := checkContainerStatus(cs, true); err != nil {
			errors = append(errors, *err)
		}
	}

	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if err := checkContainerStatus(cs, false); err != nil {
			errors = append(errors, *err)
		}
	}

	return errors
}

func checkContainerStatus(cs *corev1.ContainerStatus, isInit bool) *PodContainerError {
	if cs.State.Waiting != nil {
		if category, ok := terminalContainerWaitingReasons[cs.State.Waiting.Reason]; ok {
			return &PodContainerError{
				ContainerName: cs.Name,
				Reason:        cs.State.Waiting.Reason,
				Message:       cs.State.Waiting.Message,
				Category:      category,
				IsInit:        isInit,
			}
		}
	}

	if cs.State.Terminated != nil {
		if category, ok := terminalContainerTerminatedReasons[cs.State.Terminated.Reason]; ok {
			return &PodContainerError{
				ContainerName: cs.Name,
				Reason:        cs.State.Terminated.Reason,
				Message:       cs.State.Terminated.Message,
				Category:      category,
				IsInit:        isInit,
			}
		}
	}

	return nil
}

// formatPodContainerErrors produces a human-readable summary of all detected container errors.
func formatPodContainerErrors(errors []PodContainerError) string {
	if len(errors) == 0 {
		return ""
	}
	if len(errors) == 1 {
		return errors[0].String()
	}
	parts := make([]string, len(errors))
	for i, e := range errors {
		parts[i] = e.String()
	}
	return strings.Join(parts, "; ")
}
