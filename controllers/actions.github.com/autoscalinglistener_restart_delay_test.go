package actionsgithubcom

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestListenerRestartDelay(t *testing.T) {
	now := time.Now()

	podWith := func(state corev1.ContainerState) *corev1.Pod {
		return &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: autoscalingListenerContainerName, State: state},
				},
			},
		}
	}
	terminated := func(exitCode int32, finishedAt time.Time) *corev1.Pod {
		t := &corev1.ContainerStateTerminated{ExitCode: exitCode}
		if !finishedAt.IsZero() {
			t.FinishedAt = metav1.NewTime(finishedAt)
		}
		return podWith(corev1.ContainerState{Terminated: t})
	}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want time.Duration
	}{
		{
			name: "no container status",
			pod:  &corev1.Pod{},
			want: 0,
		},
		{
			name: "container is running",
			pod:  podWith(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}),
			want: 0,
		},
		{
			name: "clean exit is recreated immediately",
			pod:  terminated(0, now),
			want: 0,
		},
		{
			name: "failure without a finish time is recreated immediately",
			pod:  terminated(1, time.Time{}),
			want: 0,
		},
		{
			name: "fresh failure waits out the delay",
			pod:  terminated(1, now.Add(-5*time.Second)),
			want: listenerFailedRestartDelay - 5*time.Second,
		},
		{
			name: "failure older than the delay is recreated immediately",
			pod:  terminated(1, now.Add(-listenerFailedRestartDelay-time.Second)),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// metav1.Time drops sub-second precision when it is not serialized, so compare exactly
			// against the value the helper actually sees.
			assert.Equal(t, tt.want, listenerRestartDelay(tt.pod, now).Round(time.Second))
		})
	}
}
