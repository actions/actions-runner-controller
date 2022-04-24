package controllers

import (
	"testing"

	arcv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewRunnerPod(t *testing.T) {
	type testcase struct {
		description string

		template corev1.Pod
		config   arcv1alpha1.RunnerConfig
		want     corev1.Pod
	}

	base := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"runnerset-name": "runner",
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "runner",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "work",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "certs-client",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "runner",
					Image: "default-runner-image",
					Env: []corev1.EnvVar{
						{
							Name:  "RUNNER_ORG",
							Value: "",
						},
						{
							Name:  "RUNNER_REPO",
							Value: "",
						},
						{
							Name:  "RUNNER_ENTERPRISE",
							Value: "",
						},
						{
							Name:  "RUNNER_LABELS",
							Value: "",
						},
						{
							Name:  "RUNNER_GROUP",
							Value: "",
						},
						{
							Name:  "DOCKER_ENABLED",
							Value: "true",
						},
						{
							Name:  "DOCKERD_IN_RUNNER",
							Value: "false",
						},
						{
							Name:  "GITHUB_URL",
							Value: "api.github.com",
						},
						{
							Name:  "RUNNER_WORKDIR",
							Value: "/runner/_work",
						},
						{
							Name:  "RUNNER_EPHEMERAL",
							Value: "true",
						},
						{
							Name:  "DOCKER_HOST",
							Value: "tcp://localhost:2376",
						},
						{
							Name:  "DOCKER_TLS_VERIFY",
							Value: "1",
						},
						{
							Name:  "DOCKER_CERT_PATH",
							Value: "/certs/client",
						},
						{
							Name:  "RUNNER_FEATURE_FLAG_EPHEMERAL",
							Value: "true",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
						{
							Name:      "work",
							MountPath: "/runner/_work",
						},
						{
							Name:      "certs-client",
							MountPath: "/certs/client",
							ReadOnly:  true,
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{
						Privileged: func() *bool { v := false; return &v }(),
					},
				},
				{
					Name:  "docker",
					Image: "default-docker-image",
					Env: []corev1.EnvVar{
						{
							Name:  "DOCKER_TLS_CERTDIR",
							Value: "/certs",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
						{
							Name:      "certs-client",
							MountPath: "/certs/client",
						},
						{
							Name:      "work",
							MountPath: "/runner/_work",
						},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: func(b bool) *bool { return &b }(true),
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
		},
	}

	boolPtr := func(v bool) *bool {
		return &v
	}

	dinrBase := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"runnerset-name": "runner",
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "runner",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "runner",
					Image: "default-runner-image",
					Env: []corev1.EnvVar{
						{
							Name:  "RUNNER_ORG",
							Value: "",
						},
						{
							Name:  "RUNNER_REPO",
							Value: "",
						},
						{
							Name:  "RUNNER_ENTERPRISE",
							Value: "",
						},
						{
							Name:  "RUNNER_LABELS",
							Value: "",
						},
						{
							Name:  "RUNNER_GROUP",
							Value: "",
						},
						{
							Name:  "DOCKER_ENABLED",
							Value: "true",
						},
						{
							Name:  "DOCKERD_IN_RUNNER",
							Value: "true",
						},
						{
							Name:  "GITHUB_URL",
							Value: "api.github.com",
						},
						{
							Name:  "RUNNER_WORKDIR",
							Value: "/runner/_work",
						},
						{
							Name:  "RUNNER_EPHEMERAL",
							Value: "true",
						},
						{
							Name:  "RUNNER_FEATURE_FLAG_EPHEMERAL",
							Value: "true",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{
						Privileged: boolPtr(true),
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
		},
	}

	dockerDisabled := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"runnerset-name": "runner",
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "runner",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "runner",
					Image: "default-runner-image",
					Env: []corev1.EnvVar{
						{
							Name:  "RUNNER_ORG",
							Value: "",
						},
						{
							Name:  "RUNNER_REPO",
							Value: "",
						},
						{
							Name:  "RUNNER_ENTERPRISE",
							Value: "",
						},
						{
							Name:  "RUNNER_LABELS",
							Value: "",
						},
						{
							Name:  "RUNNER_GROUP",
							Value: "",
						},
						{
							Name:  "DOCKER_ENABLED",
							Value: "false",
						},
						{
							Name:  "DOCKERD_IN_RUNNER",
							Value: "false",
						},
						{
							Name:  "GITHUB_URL",
							Value: "api.github.com",
						},
						{
							Name:  "RUNNER_WORKDIR",
							Value: "/runner/_work",
						},
						{
							Name:  "RUNNER_EPHEMERAL",
							Value: "true",
						},
						{
							Name:  "RUNNER_FEATURE_FLAG_EPHEMERAL",
							Value: "true",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{
						Privileged: boolPtr(false),
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
		},
	}

	newTestPod := func(base corev1.Pod, f func(*corev1.Pod)) corev1.Pod {
		pod := base.DeepCopy()
		if f != nil {
			f(pod)
		}
		return *pod
	}

	testcases := []testcase{
		{
			description: "it should have unprivileged runner and privileged sidecar docker container",
			template:    corev1.Pod{},
			config:      arcv1alpha1.RunnerConfig{},
			want:        newTestPod(base, nil),
		},
		{
			description: "dockerdWithinRunnerContainer=true should set privileged=true and omit the dind sidecar container",
			template:    corev1.Pod{},
			config: arcv1alpha1.RunnerConfig{
				DockerdWithinRunnerContainer: boolPtr(true),
			},
			want: newTestPod(dinrBase, nil),
		},
		{
			description: "in the default config you should provide both dockerdWithinRunnerContainer=true and runnerImage",
			template:    corev1.Pod{},
			config: arcv1alpha1.RunnerConfig{
				DockerdWithinRunnerContainer: boolPtr(true),
				Image:                        "dind-runner-image",
			},
			want: newTestPod(dinrBase, func(p *corev1.Pod) {
				p.Spec.Containers[0].Image = "dind-runner-image"
			}),
		},
		{
			description: "dockerEnabled=false should have no effect when dockerdWithinRunnerContainer=true",
			template:    corev1.Pod{},
			config: arcv1alpha1.RunnerConfig{
				DockerdWithinRunnerContainer: boolPtr(true),
				DockerEnabled:                boolPtr(false),
			},
			want: newTestPod(dinrBase, nil),
		},
		{
			description: "dockerEnabled=false should omit the dind sidecar and set privileged=false and envvars DOCKER_ENABLED=false and DOCKERD_IN_RUNNER=false",
			template:    corev1.Pod{},
			config: arcv1alpha1.RunnerConfig{
				DockerEnabled: boolPtr(false),
			},
			want: newTestPod(dockerDisabled, nil),
		},
		{
			description: "TODO: dockerEnabled=false results in privileged=false by default but you can override it",
			template: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "runner",
							SecurityContext: &corev1.SecurityContext{
								Privileged: boolPtr(true),
							},
						},
					},
				},
			},
			config: arcv1alpha1.RunnerConfig{
				DockerEnabled: boolPtr(false),
			},
			want: newTestPod(dockerDisabled, func(p *corev1.Pod) {
				// TODO
				// p.Spec.Containers[0].SecurityContext.Privileged = boolPtr(true)
			}),
		},
	}

	var (
		defaultRunnerImage            = "default-runner-image"
		defaultRunnerImagePullSecrets = []string{}
		defaultDockerImage            = "default-docker-image"
		defaultDockerRegistryMirror   = ""
		githubBaseURL                 = "api.github.com"
	)

	for i := range testcases {
		tc := testcases[i]
		t.Run(tc.description, func(t *testing.T) {
			got, err := newRunnerPod("runner", tc.template, tc.config, defaultRunnerImage, defaultRunnerImagePullSecrets, defaultDockerImage, defaultDockerRegistryMirror, githubBaseURL, false)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
