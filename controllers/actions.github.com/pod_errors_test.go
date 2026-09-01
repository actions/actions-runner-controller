package actionsgithubcom

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestExtractPodContainerErrors_ImagePullBackOff(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ImagePullBackOff",
							Message: "Back-off pulling image \"ghcr.io/org/runner:v3\"",
						},
					},
				},
			},
		},
	}

	errors := extractPodContainerErrors(pod)
	assert.Len(t, errors, 1)
	assert.Equal(t, "runner", errors[0].ContainerName)
	assert.Equal(t, "ImagePullBackOff", errors[0].Reason)
	assert.Equal(t, "ImagePull", errors[0].Category)
	assert.False(t, errors[0].IsInit)
	assert.Contains(t, errors[0].String(), "ghcr.io/org/runner:v3")
}

func TestExtractPodContainerErrors_InitContainerError(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "setup",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ErrImagePull",
							Message: "unauthorized: authentication required",
						},
					},
				},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "PodInitializing",
							Message: "",
						},
					},
				},
			},
		},
	}

	errors := extractPodContainerErrors(pod)
	assert.Len(t, errors, 1)
	assert.Equal(t, "setup", errors[0].ContainerName)
	assert.True(t, errors[0].IsInit)
	assert.Equal(t, "ImagePull", errors[0].Category)
}

func TestExtractPodContainerErrors_OOMKilled(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:   "OOMKilled",
							Message:  "",
							ExitCode: 137,
						},
					},
				},
			},
		},
	}

	errors := extractPodContainerErrors(pod)
	assert.Len(t, errors, 1)
	assert.Equal(t, "OOMKilled", errors[0].Reason)
	assert.Equal(t, "ResourceLimit", errors[0].Category)
}

func TestExtractPodContainerErrors_NoErrors(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}

	errors := extractPodContainerErrors(pod)
	assert.Nil(t, errors)
}

func TestExtractPodContainerErrors_NonTerminalWaiting(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "ContainerCreating",
						},
					},
				},
			},
		},
	}

	errors := extractPodContainerErrors(pod)
	assert.Nil(t, errors)
}

func TestExtractPodContainerErrors_MultipleErrors(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init-creds",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "ImagePullBackOff",
							Message: "pull access denied",
						},
					},
				},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CreateContainerConfigError",
							Message: "secret not found",
						},
					},
				},
			},
		},
	}

	errors := extractPodContainerErrors(pod)
	assert.Len(t, errors, 2)

	formatted := formatPodContainerErrors(errors)
	assert.Contains(t, formatted, "init-container")
	assert.Contains(t, formatted, "ImagePullBackOff")
	assert.Contains(t, formatted, "CreateContainerConfigError")
}

func TestFormatPodContainerErrors_Empty(t *testing.T) {
	assert.Equal(t, "", formatPodContainerErrors(nil))
}

func TestFormatPodContainerErrors_Single(t *testing.T) {
	errors := []PodContainerError{
		{
			ContainerName: "runner",
			Reason:        "ImagePullBackOff",
			Message:       "image not found",
			Category:      "ImagePull",
		},
	}
	result := formatPodContainerErrors(errors)
	assert.Equal(t, `container "runner": ImagePullBackOff: image not found`, result)
}
