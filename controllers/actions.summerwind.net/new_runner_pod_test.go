package actionssummerwindnet

import (
	"testing"

	arcv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/github"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newRunnerPod(template corev1.Pod, runnerSpec arcv1alpha1.RunnerConfig, githubBaseURL string, d RunnerPodDefaults) (corev1.Pod, error) {
	return newRunnerPodWithContainerMode("", template, runnerSpec, githubBaseURL, d)
}

func setEnv(c *corev1.Container, name, value string) {
	for j := range c.Env {
		e := &c.Env[j]

		if e.Name == name {
			e.Value = value
			return
		}
	}
}

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
						Resources: corev1.VolumeResourceRequirements{
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
					Name: "var-run",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium:    corev1.StorageMediumMemory,
							SizeLimit: resource.NewScaledQuantity(1, resource.Mega),
						},
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
							Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
							Value: "actions-runner-controller/NA",
						},
						{
							Name:  "DOCKER_HOST",
							Value: "unix:///run/docker.sock",
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
							Name:      "var-run",
							MountPath: "/run",
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{},
				},
				{
					Name:  "docker",
					Image: "default-docker-image",
					Args: []string{
						"dockerd",
						"--host=unix:///run/docker.sock",
						"--group=$(DOCKER_GROUP_GID)",
					},
					Env: []corev1.EnvVar{
						{
							Name:  "DOCKER_GROUP_GID",
							Value: "1234",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
						{
							Name:      "var-run",
							MountPath: "/run",
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
						{
							Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
							Value: "actions-runner-controller/NA",
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
						{
							Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
							Value: "actions-runner-controller/NA",
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
			description: "it should respect DOCKER_GROUP_GID of the dockerd sidecar container",
			template: corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "docker",
							Env: []corev1.EnvVar{
								{
									Name:  "DOCKER_GROUP_GID",
									Value: "2345",
								},
							},
						},
					},
				},
			},
			config: arcv1alpha1.RunnerConfig{},
			want: newTestPod(base, func(p *corev1.Pod) {
				setEnv(&p.Spec.Containers[1], "DOCKER_GROUP_GID", "2345")
			}),
		},
		{
			description: "it should add DOCKER_GROUP_GID=1001 to the dockerd sidecar container for Ubuntu 20.04 runners",
			template:    corev1.Pod{},
			config: arcv1alpha1.RunnerConfig{
				Image: "ghcr.io/summerwind/actions-runner:ubuntu-20.04-20210726-1",
			},
			want: newTestPod(base, func(p *corev1.Pod) {
				setEnv(&p.Spec.Containers[1], "DOCKER_GROUP_GID", "1001")
				p.Spec.Containers[0].Image = "ghcr.io/summerwind/actions-runner:ubuntu-20.04-20210726-1"
			}),
		},
		{
			description: "it should add DOCKER_GROUP_GID=121 to the dockerd sidecar container for Ubuntu 22.04 runners",
			template:    corev1.Pod{},
			config: arcv1alpha1.RunnerConfig{
				Image: "ghcr.io/summerwind/actions-runner:ubuntu-22.04-20210726-1",
			},
			want: newTestPod(base, func(p *corev1.Pod) {
				setEnv(&p.Spec.Containers[1], "DOCKER_GROUP_GID", "121")
				p.Spec.Containers[0].Image = "ghcr.io/summerwind/actions-runner:ubuntu-22.04-20210726-1"
			}),
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
						Name: "var-run",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								Medium:    corev1.StorageMediumMemory,
								SizeLimit: resource.NewScaledQuantity(1, resource.Mega),
							},
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
						Name:      "var-run",
						MountPath: "/run",
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
						Name: "var-run",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								Medium:    corev1.StorageMediumMemory,
								SizeLimit: resource.NewScaledQuantity(1, resource.Mega),
							},
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
			got, err := newRunnerPod(tc.template, tc.config, githubBaseURL, RunnerPodDefaults{
				RunnerImage:               defaultRunnerImage,
				RunnerImagePullSecrets:    defaultRunnerImagePullSecrets,
				DockerImage:               defaultDockerImage,
				DockerRegistryMirror:      defaultDockerRegistryMirror,
				DockerGID:                 "1234",
				UseRunnerStatusUpdateHook: false,
			})
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
					Name: "var-run",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium:    corev1.StorageMediumMemory,
							SizeLimit: resource.NewScaledQuantity(1, resource.Mega),
						},
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
							Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
							Value: "actions-runner-controller/NA",
						},
						{
							Name:  "DOCKER_HOST",
							Value: "unix:///run/docker.sock",
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
							Name:      "var-run",
							MountPath: "/run",
						},
					},
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: &corev1.SecurityContext{},
				},
				{
					Name:  "docker",
					Image: "default-docker-image",
					Args: []string{
						"dockerd",
						"--host=unix:///run/docker.sock",
						"--group=$(DOCKER_GROUP_GID)",
					},
					Env: []corev1.EnvVar{
						{
							Name:  "DOCKER_GROUP_GID",
							Value: "1234",
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "runner",
							MountPath: "/runner",
						},
						{
							Name:      "var-run",
							MountPath: "/run",
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
							Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
							Value: "actions-runner-controller/NA",
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
							Name:  "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT",
							Value: "actions-runner-controller/NA",
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
									{
										Name:      "var-run",
										MountPath: "/run",
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
						Name: "var-run",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								Medium:    corev1.StorageMediumMemory,
								SizeLimit: resource.NewScaledQuantity(1, resource.Mega),
							},
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
						Name:      "var-run",
						MountPath: "/run",
					},
					{
						Name:      "runner",
						MountPath: "/runner",
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
						Name: "var-run",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								Medium:    corev1.StorageMediumMemory,
								SizeLimit: resource.NewScaledQuantity(1, resource.Mega),
							},
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
		defaultDockerGID              = "1234"
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
				GitHubClient: multiClient,
				Scheme:       scheme,
				RunnerPodDefaults: RunnerPodDefaults{
					RunnerImage:            defaultRunnerImage,
					RunnerImagePullSecrets: defaultRunnerImagePullSecrets,
					DockerImage:            defaultDockerImage,
					DockerRegistryMirror:   defaultDockerRegistryMirror,
					DockerGID:              defaultDockerGID,
				},
			}
			got, err := r.newPod(tc.runner)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
