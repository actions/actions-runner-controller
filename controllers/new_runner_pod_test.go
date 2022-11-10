package controllers

import (
	"testing"

	arcv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newWorkGenericEphemeralVolume(t *testing.T, storageReq string) corev1.Volume {
	GBs, err := resource.ParseQuantity(storageReq)
	if err != nil {
		t.Fatalf("%v", err)
	}

	return corev1.Volume{
		Name: "work",
		VolumeSource: corev1.VolumeSource{
			Ephemeral: &corev1.EphemeralVolumeSource{
				VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						StorageClassName: strPtr("runner-work-dir"),
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: GBs,
							},
						},
					},
				},
			},
		},
	}
}

func TestNewRunnerPod(t *testing.T) {
	workGenericEphemeralVolume := newWorkGenericEphemeralVolume(t, "10Gi")

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
				"actions-runner": "",
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
							Name:  "RUNNER_STATUS_UPDATE_HOOK",
							Value: "false",
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
					SecurityContext: &corev1.SecurityContext{},
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
					Lifecycle: &corev1.Lifecycle{
						PreStop: &corev1.LifecycleHandler{
							Exec: &corev1.ExecAction{
								Command: []string{
									"/bin/sh",
									"-c",
									"timeout \"${RUNNER_GRACEFUL_STOP_TIMEOUT:-15}\" /bin/sh -c \"echo 'Prestop hook started'; while [ -f /runner/.runner ]; do sleep 1; done; echo 'Waiting for dockerd to start'; while ! pgrep -x dockerd; do sleep 1; done; echo 'Prestop hook stopped'\" >/proc/1/fd/1 2>&1",
								},
							},
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	boolPtr := func(v bool) *bool {
		return &v
	}

	dinrBase := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"actions-runner": "",
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
							Name:  "RUNNER_STATUS_UPDATE_HOOK",
							Value: "false",
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
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	dockerDisabled := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"actions-runner": "",
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
							Name:  "RUNNER_STATUS_UPDATE_HOOK",
							Value: "false",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
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
				p.Spec.Containers[0].SecurityContext.Privileged = boolPtr(true)
			}),
		},
		{
			description: "Mount generic ephemeral volume onto work (with explicit volumeMount)",
			template: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "runner",
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "work",
									MountPath: "/runner/_work",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						workGenericEphemeralVolume,
					},
				},
			},
			want: newTestPod(base, func(p *corev1.Pod) {
				p.Spec.Volumes = []corev1.Volume{
					workGenericEphemeralVolume,
					{
						Name: "runner",
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
				}
				p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
					{
						Name:      "work",
						MountPath: "/runner/_work",
					},
					{
						Name:      "runner",
						MountPath: "/runner",
					},
					{
						Name:      "certs-client",
						MountPath: "/certs/client",
						ReadOnly:  true,
					},
				}
			}),
		},
		{
			description: "Mount generic ephemeral volume onto work (without explicit volumeMount)",
			template: corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						workGenericEphemeralVolume,
					},
				},
			},
			want: newTestPod(base, func(p *corev1.Pod) {
				p.Spec.Volumes = []corev1.Volume{
					workGenericEphemeralVolume,
					{
						Name: "runner",
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
				}
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
			got, err := newRunnerPod(tc.template, tc.config, defaultRunnerImage, defaultRunnerImagePullSecrets, defaultDockerImage, defaultDockerRegistryMirror, githubBaseURL, false)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func TestNewRunnerPodFromRunnerController(t *testing.T) {
	workGenericEphemeralVolume := newWorkGenericEphemeralVolume(t, "10Gi")

	type testcase struct {
		description string

		runner arcv1alpha1.Runner
		want   corev1.Pod
	}

	boolPtr := func(v bool) *bool {
		return &v
	}

	base := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "runner",
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"pod-template-hash": "8857b86c7",
				"actions-runner":    "",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "actions.summerwind.dev/v1alpha1",
					Kind:               "Runner",
					Name:               "runner",
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
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
							Name:  "RUNNER_STATUS_UPDATE_HOOK",
							Value: "false",
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
							Name:  "RUNNER_NAME",
							Value: "runner",
						},
						{
							Name:  "RUNNER_TOKEN",
							Value: "",
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
					SecurityContext: &corev1.SecurityContext{},
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
					Lifecycle: &corev1.Lifecycle{
						PreStop: &corev1.LifecycleHandler{
							Exec: &corev1.ExecAction{
								Command: []string{
									"/bin/sh",
									"-c",
									"timeout \"${RUNNER_GRACEFUL_STOP_TIMEOUT:-15}\" /bin/sh -c \"echo 'Prestop hook started'; while [ -f /runner/.runner ]; do sleep 1; done; echo 'Waiting for dockerd to start'; while ! pgrep -x dockerd; do sleep 1; done; echo 'Prestop hook stopped'\" >/proc/1/fd/1 2>&1",
								},
							},
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	dinrBase := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "runner",
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"pod-template-hash": "8857b86c7",
				"actions-runner":    "",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "actions.summerwind.dev/v1alpha1",
					Kind:               "Runner",
					Name:               "runner",
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
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
							Name:  "RUNNER_STATUS_UPDATE_HOOK",
							Value: "false",
						},
						{
							Name:  "RUNNER_NAME",
							Value: "runner",
						},
						{
							Name:  "RUNNER_TOKEN",
							Value: "",
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
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	dockerDisabled := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "runner",
			Labels: map[string]string{
				"actions-runner-controller/inject-registration-token": "true",
				"pod-template-hash": "8857b86c7",
				"actions-runner":    "",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "actions.summerwind.dev/v1alpha1",
					Kind:               "Runner",
					Name:               "runner",
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
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
							Name:  "RUNNER_STATUS_UPDATE_HOOK",
							Value: "false",
						},
						{
							Name:  "RUNNER_NAME",
							Value: "runner",
						},
						{
							Name:  "RUNNER_TOKEN",
							Value: "",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
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
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerConfig: arcv1alpha1.RunnerConfig{},
				},
			},
			want: newTestPod(base, nil),
		},
		{
			description: "dockerdWithinRunnerContainer=true should set privileged=true and omit the dind sidecar container",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerConfig: arcv1alpha1.RunnerConfig{
						DockerdWithinRunnerContainer: boolPtr(true),
					},
				},
			},
			want: newTestPod(dinrBase, nil),
		},
		{
			description: "in the default config you should provide both dockerdWithinRunnerContainer=true and runnerImage",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerConfig: arcv1alpha1.RunnerConfig{
						DockerdWithinRunnerContainer: boolPtr(true),
						Image:                        "dind-runner-image",
					},
				},
			},
			want: newTestPod(dinrBase, func(p *corev1.Pod) {
				p.Spec.Containers[0].Image = "dind-runner-image"
			}),
		},
		{
			description: "dockerEnabled=false should have no effect when dockerdWithinRunnerContainer=true",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerConfig: arcv1alpha1.RunnerConfig{
						DockerdWithinRunnerContainer: boolPtr(true),
						DockerEnabled:                boolPtr(false),
					},
				},
			},
			want: newTestPod(dinrBase, nil),
		},
		{
			description: "dockerEnabled=false should omit the dind sidecar and set privileged=false and envvars DOCKER_ENABLED=false and DOCKERD_IN_RUNNER=false",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerConfig: arcv1alpha1.RunnerConfig{
						DockerEnabled: boolPtr(false),
					},
				},
			},
			want: newTestPod(dockerDisabled, nil),
		},
		{
			description: "TODO: dockerEnabled=false results in privileged=false by default but you can override it",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerConfig: arcv1alpha1.RunnerConfig{
						DockerEnabled: boolPtr(false),
					},
					RunnerPodSpec: arcv1alpha1.RunnerPodSpec{
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
			},

			want: newTestPod(dockerDisabled, func(p *corev1.Pod) {
				p.Spec.Containers[0].SecurityContext.Privileged = boolPtr(true)
			}),
		},
		{
			description: "Mount generic ephemeral volume onto work (with explicit volumeMount)",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerPodSpec: arcv1alpha1.RunnerPodSpec{
						Containers: []corev1.Container{
							{
								Name: "runner",
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "work",
										MountPath: "/runner/_work",
									},
								},
							},
						},
						Volumes: []corev1.Volume{
							workGenericEphemeralVolume,
						},
					},
				},
			},
			want: newTestPod(base, func(p *corev1.Pod) {
				p.Spec.Volumes = []corev1.Volume{
					{
						Name: "runner",
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
					workGenericEphemeralVolume,
				}
				p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
					{
						Name:      "work",
						MountPath: "/runner/_work",
					},
					{
						Name:      "runner",
						MountPath: "/runner",
					},
					{
						Name:      "certs-client",
						MountPath: "/certs/client",
						ReadOnly:  true,
					},
				}
			}),
		},
		{
			description: "Mount generic ephemeral volume onto work (without explicit volumeMount)",
			runner: arcv1alpha1.Runner{
				ObjectMeta: metav1.ObjectMeta{
					Name: "runner",
				},
				Spec: arcv1alpha1.RunnerSpec{
					RunnerPodSpec: arcv1alpha1.RunnerPodSpec{
						Volumes: []corev1.Volume{
							workGenericEphemeralVolume,
						},
					},
				},
			},
			want: newTestPod(base, func(p *corev1.Pod) {
				p.Spec.Volumes = []corev1.Volume{
					{
						Name: "runner",
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
					workGenericEphemeralVolume,
				}
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

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = arcv1alpha1.AddToScheme(scheme)

	for i := range testcases {
		tc := testcases[i]

		rr := &testResourceReader{
			objects: map[types.NamespacedName]client.Object{},
		}

		multiClient := NewMultiGitHubClient(rr, &github.Client{GithubBaseURL: githubBaseURL})

		t.Run(tc.description, func(t *testing.T) {
			r := &RunnerReconciler{
				RunnerImage:            defaultRunnerImage,
				RunnerImagePullSecrets: defaultRunnerImagePullSecrets,
				DockerImage:            defaultDockerImage,
				DockerRegistryMirror:   defaultDockerRegistryMirror,
				GitHubClient:           multiClient,
				Scheme:                 scheme,
			}
			got, err := r.newPod(tc.runner)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
