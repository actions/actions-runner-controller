package actionsgithubcom

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func renderAdmissionChart(ctx context.Context, chart, template, namespace, values string) []byte {
	GinkgoHelper()
	cmd := exec.CommandContext(ctx, "helm", "template", "chart-contract",
		filepath.Join("../../charts", chart), "--namespace", namespace,
		"--show-only", "templates/"+template, "--values", "-")
	cmd.Stdin = strings.NewReader(values)
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	return output
}

var _ = Describe("Experimental chart contracts", func() {
	var namespace string
	BeforeEach(func(ctx SpecContext) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "chart-contract-"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		namespace = ns.Name
		DeferCleanup(func(ctx SpecContext) {
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})
	})

	for _, mode := range []string{"", "dind", "kubernetes"} {
		for _, policy := range []string{"", "Never", "OnFailure", "Always"} {
			It(fmt.Sprintf("admits runner mode %q restartPolicy %q through ResourceBuilder", mode, policy), func(ctx SpecContext) {
				values := fmt.Sprintf(`
auth:
  url: https://github.com/example
  secretName: existing-auth
controllerServiceAccount:
  name: controller
  namespace: arc-system
runner:
  mode: %q
  container:
    env:
      - {name: CUSTOM_MARKER, value: expected}
      - {name: ACTIONS_RUNNER_POD_NAME, value: custom-pod}
      - name: DOCKER_HOST
        valueFrom:
          secretKeyRef: {name: docker-host, key: address}
    resources:
      requests: {cpu: 250m, memory: 512Mi}
    securityContext: {runAsUser: 1000, runAsGroup: 0, allowPrivilegeEscalation: false}
    volumeMounts:
      - {name: custom, mountPath: /custom}
  pod:
    spec:
      volumes:
        - {name: custom, emptyDir: {}}
`, mode)
				want := corev1.RestartPolicyNever
				if policy != "" {
					values += "      restartPolicy: " + policy + "\n"
					want = corev1.RestartPolicy(policy)
				}
				var ars actionsv1alpha1.AutoscalingRunnerSet
				Expect(yaml.UnmarshalStrict(renderAdmissionChart(ctx, "gha-runner-scale-set-experimental", "autoscalingrunnserset.yaml", namespace, values), &ars)).To(Succeed())
				Expect(k8sClient.Create(ctx, &ars)).To(Succeed())
				admitted := &actionsv1alpha1.AutoscalingRunnerSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&ars), admitted)).To(Succeed())
				Expect(admitted.Spec.ListenerConfig).To(Equal(ars.Spec.ListenerConfig))

				cache := NewResourceCache()
				builder := ResourceBuilder{ResourceCache: &cache}
				ars.Annotations[runnerScaleSetIDAnnotationKey] = "1"
				ers, err := builder.newEphemeralRunnerSet(&ars)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, ers)).To(Succeed())
				runner, err := builder.newEphemeralRunner(ers)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, runner)).To(Succeed())
				pod, err := builder.newEphemeralRunnerPod(runner, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "jit"}})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
					Name: pod.Spec.ServiceAccountName, Namespace: namespace,
				}})).To(Succeed())
				Expect(k8sClient.Create(ctx, pod)).To(Succeed())
				var live corev1.Pod
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), &live)).To(Succeed())
				Expect(live.Spec.RestartPolicy).To(Equal(want))
				Expect(ars.Spec.Template.Spec.RestartPolicy).To(Equal(want))
				Expect(live.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "CUSTOM_MARKER", Value: "expected"}))
				Expect(live.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "ACTIONS_RUNNER_POD_NAME", Value: "custom-pod"}))
				Expect(live.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{
					Name: "DOCKER_HOST",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "docker-host"}, Key: "address",
					}},
				}))
				Expect(live.Spec.Containers[0].Resources.Requests.Cpu().String()).To(Equal("250m"))
				Expect(live.Spec.Containers[0].VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "custom", MountPath: "/custom"}))
				if mode == "dind" {
					Expect(*live.Spec.InitContainers[1].RestartPolicy).To(Equal(corev1.ContainerRestartPolicyAlways))
				}
			})
		}
	}

	It("retains all supported scaler keys through API admission", func(ctx SpecContext) {
		values := `
auth:
  url: https://github.com/example
  secretName: existing-auth
controllerServiceAccount:
  name: controller
  namespace: arc-system
listener:
  scaler: {qps: 30, burst: 60, scaleQPS: 25, scaleBurst: 50, workers: 8}
`
		var ars actionsv1alpha1.AutoscalingRunnerSet
		Expect(yaml.UnmarshalStrict(renderAdmissionChart(ctx, "gha-runner-scale-set-experimental", "autoscalingrunnserset.yaml", namespace, values), &ars)).To(Succeed())
		Expect(k8sClient.Create(ctx, &ars)).To(Succeed())
		var live actionsv1alpha1.AutoscalingRunnerSet
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&ars), &live)).To(Succeed())
		Expect(live.Spec.ListenerConfig).To(Equal(ars.Spec.ListenerConfig))
	})

	for _, chart := range []string{"gha-runner-scale-set-controller", "gha-runner-scale-set-controller-experimental"} {
		for _, address := range []string{"", ":8080", "127.0.0.1:8080", "[::]:8080", "localhost:8080", ":65535", "0"} {
			It(fmt.Sprintf("admits %s metrics %q", chart, address), func(ctx SpecContext) {
				values := ""
				if address != "" {
					values = fmt.Sprintf("metrics:\n  controllerManagerAddr: %q\n  listenerAddr: ':9090'\n  listenerEndpoint: /metrics\n", address)
					if strings.HasSuffix(chart, "-experimental") {
						values = "controller:\n  " + strings.ReplaceAll(strings.TrimSuffix(values, "\n"), "\n", "\n  ") + "\n"
					}
				}
				var deployment appsv1.Deployment
				Expect(yaml.UnmarshalStrict(renderAdmissionChart(ctx, chart, "deployment.yaml", namespace, values), &deployment)).To(Succeed())
				Expect(k8sClient.Create(ctx, &deployment)).To(Succeed())
				var live appsv1.Deployment
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&deployment), &live)).To(Succeed())
				if address == "" || address == "0" {
					Expect(live.Spec.Template.Spec.Containers[0].Ports).To(BeEmpty())
					Expect(live.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--metrics-addr=0"))
				} else {
					Expect(live.Spec.Template.Spec.Containers[0].Ports).To(HaveLen(1))
					Expect(live.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort).To(BeNumerically(">", 0))
					Expect(live.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--metrics-addr=" + address))
				}
			})
		}
	}
})
