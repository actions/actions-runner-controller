package kjobmgr

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namespace = "default"
	image     = "huangtingluo/jit-runner-image"
	jobName   = "autoscaler-prototype-runner-job"
	podName   = "autoscaler-prototype-runner-pod"
)

var (
	labels = client.MatchingLabels{
		"app": "autoscaler",
	}
)

func defaultJobResource(jitConfig string) *batchv1.Job {
	// Use runner ID instead
	uid := strings.Split(uuid.New().String(), "-")[0]
	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Job",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v-%v", jobName, uid),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%v-%v", podName, uid),
					Namespace: namespace,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  podName,
							Image: image,
							Args:  []string{"--jitconfig", jitConfig},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
}
