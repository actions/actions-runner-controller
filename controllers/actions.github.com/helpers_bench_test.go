package actionsgithubcom

import (
	"fmt"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// benchmarkEphemeralRunnerSet builds a runner set roughly the size of a real
// ARC deployment: a runner container, a dind sidecar, an init container,
// resource limits, volumes and a dozen environment variables.
func benchmarkEphemeralRunnerSet() *v1alpha1.EphemeralRunnerSet {
	env := make([]corev1.EnvVar, 0, 12)
	for i := range 12 {
		env = append(env, corev1.EnvVar{Name: fmt.Sprintf("VAR_%d", i), Value: fmt.Sprintf("value-%d", i)})
	}
	q := resource.MustParse

	return &v1alpha1.EphemeralRunnerSet{
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: 10,
			PatchID:  7,
			EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    "https://github.com/octo-org",
				GitHubConfigSecret: "gh-config",
				RunnerScaleSetID:   42,
				PodTemplateSpec: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						ServiceAccountName: "runner-sa",
						RestartPolicy:      corev1.RestartPolicyNever,
						NodeSelector:       map[string]string{"kubernetes.io/os": "linux", "node.kubernetes.io/pool": "runners"},
						Tolerations: []corev1.Toleration{{
							Key: "dedicated", Operator: corev1.TolerationOpEqual,
							Value: "runners", Effect: corev1.TaintEffectNoSchedule,
						}},
						Volumes: []corev1.Volume{
							{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
							{Name: "dind-sock", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						},
						InitContainers: []corev1.Container{{
							Name: "init-dind", Image: "docker:dind",
							Command:      []string{"cp"},
							Args:         []string{"-r", "/usr/local/bin/.", "/dind"},
							VolumeMounts: []corev1.VolumeMount{{Name: "dind-sock", MountPath: "/dind"}},
						}},
						Containers: []corev1.Container{
							{
								Name:    "runner",
								Image:   "ghcr.io/actions/actions-runner:2.337.0",
								Command: []string{"/home/runner/run.sh"},
								Env:     env,
								VolumeMounts: []corev1.VolumeMount{
									{Name: "work", MountPath: "/home/runner/_work"},
									{Name: "dind-sock", MountPath: "/var/run"},
								},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{corev1.ResourceCPU: q("500m"), corev1.ResourceMemory: q("1Gi")},
									Limits:   corev1.ResourceList{corev1.ResourceCPU: q("2"), corev1.ResourceMemory: q("4Gi")},
								},
							},
							{
								Name: "dind", Image: "docker:dind", Env: env[:6],
								VolumeMounts: []corev1.VolumeMount{{Name: "dind-sock", MountPath: "/var/run"}},
							},
						},
					},
				},
			},
		},
	}
}

// BenchmarkEphemeralRunnerSetActionableSpecChanged measures the drift check that
// runs on every AutoscalingRunnerSet reconcile. Reconciles are driven by
// EphemeralRunnerSet status churn via Owns(), so this executes constantly and
// its allocation count feeds directly into controller GC pressure.
//
// For reference on the machine this was written on: Semantic.DeepEqual is around
// 65us/241 allocs, versus 332us/405 allocs for cmp.Equal. If this regresses by an
// order of magnitude, something switched the comparison back to a reflection
// heavy implementation.
func BenchmarkEphemeralRunnerSetActionableSpecChanged(b *testing.B) {
	b.Run("no drift", func(b *testing.B) {
		current, desired := benchmarkEphemeralRunnerSet(), benchmarkEphemeralRunnerSet()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if ephemeralRunnerSetActionableSpecChanged(current, desired) {
				b.Fatal("expected no drift")
			}
		}
	})

	b.Run("drift", func(b *testing.B) {
		current, desired := benchmarkEphemeralRunnerSet(), benchmarkEphemeralRunnerSet()
		desired.Spec.EphemeralRunnerSpec.PodTemplateSpec.Spec.Containers[0].Image = "ghcr.io/actions/actions-runner:2.338.0"
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if !ephemeralRunnerSetActionableSpecChanged(current, desired) {
				b.Fatal("expected drift")
			}
		}
	})
}

// BenchmarkListenerPodSpecRequiresRecreation measures the listener pod drift
// check, which also runs on every AutoscalingListener reconcile.
func BenchmarkListenerPodSpecRequiresRecreation(b *testing.B) {
	desired := desiredListenerPod()
	live := livePodFromDesired(desired)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if listenerPodSpecRequiresRecreation(live, desired) {
			b.Fatal("expected no recreation")
		}
	}
}
