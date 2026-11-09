package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/actions-runner-controller/vault"
	"github.com/actions/actions-runner-controller/vault/azurekeyvault"
	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

const contractValues = `
auth:
  url: https://github.com/example
  secretName: existing-auth
controllerServiceAccount:
  name: controller
  namespace: arc-system
`

func renderContract(t *testing.T, values string, set map[string]string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contractValues+values), 0600))
	chart, err := filepath.Abs("..")
	require.NoError(t, err)
	return helm.RenderTemplateContextE(t, t.Context(), &helm.Options{
		Logger:         logger.Discard,
		ValuesFiles:    []string{path},
		SetValues:      set,
		KubectlOptions: k8s.NewKubectlOptions("", "", "runners"),
	}, chart, "contract", []string{"templates/autoscalingrunnserset.yaml"})
}

func runnerSetContract(t *testing.T, values string, set map[string]string) *actionsv1alpha1.AutoscalingRunnerSet {
	t.Helper()
	output, err := renderContract(t, values, set)
	require.NoError(t, err)
	var ars actionsv1alpha1.AutoscalingRunnerSet
	require.NoError(t, yaml.UnmarshalStrict([]byte(output), &ars))
	return &ars
}

func TestExperimentalAzureVaultDispatch(t *testing.T) {
	certificate := filepath.Join(t.TempDir(), "missing.pem")
	output, err := renderContract(t, fmt.Sprintf(`
secretResolution:
  type: azureKeyVault
  azureKeyVault:
    url: https://example.vault.azure.net
    tenantId: tenant
    clientId: client
    certificatePath: %q
`, certificate), nil)
	require.NoError(t, err)
	var ars actionsv1alpha1.AutoscalingRunnerSet
	assert.NoError(t, yaml.UnmarshalStrict([]byte(output), &ars))
	require.NotNil(t, ars.Spec.VaultConfig)
	assert.Equal(t, vault.VaultTypeAzureKeyVault, ars.Spec.VaultConfig.Type)
	assert.NoError(t, ars.Spec.VaultConfig.Type.Validate())
	assert.Equal(t, "tenant", ars.Spec.VaultConfig.AzureKeyVault.TenantID)
	assert.Equal(t, "client", ars.Spec.VaultConfig.AzureKeyVault.ClientID)
	assert.Equal(t, certificate, ars.Spec.VaultConfig.AzureKeyVault.CertificatePath)
	assert.Equal(t, "existing-auth", ars.Spec.GitHubConfigSecret)
	assert.Equal(t, "existing-auth", ars.GitHubConfigSecret(), "the selector passed to the vault resolver")

	// A missing local certificate proves dispatch reached Azure without any network or credentials.
	resolver := secretresolver.New(fake.NewClientBuilder().Build(), nil)
	_, err = resolver.GetAppConfig(t.Context(), &ars)
	require.ErrorContains(t, err, "failed to create Azure Key Vault client")
	assert.ErrorContains(t, err, "cert path")
	assert.ErrorContains(t, err, "does not exist")
	assert.NotContains(t, err.Error(), "unknown vault type")

	validCertificate, err := filepath.Abs("../../../vault/azurekeyvault/testdata/server.crt")
	require.NoError(t, err)
	valid := runnerSetContract(t, fmt.Sprintf(`
secretResolution:
  type: azureKeyVault
  azureKeyVault:
    url: https://example.vault.azure.net
    tenantId: tenant
    clientId: client
    certificatePath: %q
`, validCertificate), nil)
	config := valid.Spec.VaultConfig.AzureKeyVault
	azureConfig := azurekeyvault.Config{
		URL: config.URL, TenantID: config.TenantID, ClientID: config.ClientID, CertificatePath: config.CertificatePath,
	}
	assert.NoError(t, azureConfig.Validate())

	for _, discriminator := range []string{"azure_key_vault", "hashicorpVault", "", "true"} {
		t.Run("unsupported/"+discriminator, func(t *testing.T) {
			_, err := renderContract(t, fmt.Sprintf("secretResolution:\n  type: %q\n", discriminator), nil)
			require.ErrorContains(t, err, "Unsupported keyVault type")
		})
	}
	for _, discriminator := range []string{"true", "42", "[]", "{}"} {
		t.Run("invalid-type/"+discriminator, func(t *testing.T) {
			_, err := renderContract(t, "secretResolution:\n  type: "+discriminator+"\n", nil)
			require.ErrorContains(t, err, "Unsupported keyVault type")
		})
	}
	_, err = renderContract(t, "secretResolution:\n  type: azureKeyVault\n  azureKeyVault:\n    secretKey: ignored\n", nil)
	require.ErrorContains(t, err, "secretResolution.azureKeyVault.secretKey is not supported; use auth.secretName")
	assert.Nil(t, runnerSetContract(t, "", nil).Spec.VaultConfig)
}

const customizedRunner = `
runner:
  container:
    name: ignored
    image: example.com/custom-runner:v1
    command: ["/custom/run"]
    args: ["--custom"]
    imagePullPolicy: Always
    stdin: false
    tty: false
    env:
      - {name: CUSTOM_MARKER, value: expected}
    resources:
      requests: {cpu: 250m, memory: 512Mi}
      limits: {cpu: "1", memory: 1Gi}
    securityContext:
      runAsUser: 1000
      runAsGroup: 0
      privileged: false
      allowPrivilegeEscalation: false
    volumeMounts:
      - {name: custom, mountPath: /custom, readOnly: false}
  pod:
    spec:
      volumes:
        - {name: custom, emptyDir: {}}
`

func TestExperimentalRunnerCustomization(t *testing.T) {
	for _, mode := range []string{"", "dind", "kubernetes"} {
		for _, namespace := range []string{"runners", "custom-namespace"} {
			t.Run(mode+"/"+namespace, func(t *testing.T) {
				set := map[string]string{"runner.mode": mode, "namespaceOverride": namespace}
				ars := runnerSetContract(t, customizedRunner, set)
				assert.Equal(t, namespace, ars.Namespace)
				require.NotEmpty(t, ars.Spec.Template.Spec.Containers)
				c := ars.Spec.Template.Spec.Containers[0]
				assert.Equal(t, "runner", c.Name)
				assert.Equal(t, "example.com/custom-runner:v1", c.Image)
				assert.Equal(t, []string{"/custom/run"}, c.Command)
				assert.Equal(t, []string{"--custom"}, c.Args)
				assert.Equal(t, corev1.PullAlways, c.ImagePullPolicy)
				assert.Contains(t, c.Env, corev1.EnvVar{Name: "CUSTOM_MARKER", Value: "expected"})
				assert.Equal(t, "250m", c.Resources.Requests.Cpu().String())
				assert.Equal(t, "512Mi", c.Resources.Requests.Memory().String())
				assert.Equal(t, "1", c.Resources.Limits.Cpu().String())
				assert.Equal(t, "1Gi", c.Resources.Limits.Memory().String())
				require.NotNil(t, c.SecurityContext)
				assert.Equal(t, int64(1000), *c.SecurityContext.RunAsUser)
				assert.Equal(t, int64(0), *c.SecurityContext.RunAsGroup)
				assert.False(t, *c.SecurityContext.Privileged)
				assert.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
				assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{Name: "custom", MountPath: "/custom"})
				assertUniqueContainerLists(t, c)

				defaults := runnerSetContract(t, "", set).Spec.Template.Spec.Containers[0]
				assert.Equal(t, "ghcr.io/actions/actions-runner:latest", defaults.Image)
				assert.Equal(t, []string{"/home/runner/run.sh"}, defaults.Command)
				for _, env := range defaults.Env {
					assert.Contains(t, c.Env, env)
				}
				for _, mount := range defaults.VolumeMounts {
					assert.Contains(t, c.VolumeMounts, mount)
				}
				if mode == "dind" {
					assert.Contains(t, c.Env, corev1.EnvVar{Name: "DOCKER_HOST", Value: "unix:///var/run/docker.sock"})
					assert.Equal(t, "dind", ars.Spec.Template.Spec.InitContainers[1].Name)
					assert.Equal(t, corev1.ContainerRestartPolicyAlways, *ars.Spec.Template.Spec.InitContainers[1].RestartPolicy)
				}
				if mode == "kubernetes" {
					assert.Contains(t, c.Env, corev1.EnvVar{Name: "ACTIONS_RUNNER_CONTAINER_HOOKS", Value: "/home/runner/k8s/index.js"})
				}
			})
		}
	}
}

func assertUniqueContainerLists(t *testing.T, c corev1.Container) {
	t.Helper()
	envs, mounts := map[string]bool{}, map[string]bool{}
	for _, env := range c.Env {
		assert.False(t, envs[env.Name], "duplicate env %s", env.Name)
		envs[env.Name] = true
		assert.False(t, env.Value != "" && env.ValueFrom != nil, "value and valueFrom on %s", env.Name)
	}
	for _, mount := range c.VolumeMounts {
		assert.False(t, mounts[mount.MountPath], "duplicate mountPath %s", mount.MountPath)
		mounts[mount.MountPath] = true
	}
}

func TestExperimentalRunnerOverrides(t *testing.T) {
	for _, mode := range []string{"", "dind", "kubernetes"} {
		t.Run(mode, func(t *testing.T) {
			ars := runnerSetContract(t, `
githubServerTLS:
  runnerMountPath: /certs
  certificateFrom:
    configMapKeyRef: {name: ca, key: ca.crt}
runner:
  kubernetesMode:
    extensionRef: hooks
    requireJobContainer: false
  dind:
    waitForDockerInSeconds: 0
  container:
    env:
      - name: DOCKER_HOST
        valueFrom:
          secretKeyRef: {name: docker-host, key: address}
      - {name: ACTIONS_RUNNER_POD_NAME, value: custom-pod}
      - {name: ACTIONS_RUNNER_CONTAINER_HOOK_TEMPLATE, value: /custom/hooks.yaml}
      - {name: NODE_EXTRA_CA_CERTS, value: /custom/ca.crt}
      - {name: RUNNER_UPDATE_CA_CERTS, value: "0"}
    volumeMounts:
      - {name: work, mountPath: /home/runner/_work, readOnly: false}
      - {name: hook-extension, mountPath: /home/runner/k8s/hook-template.yaml, readOnly: false}
      - {name: github-server-tls-cert, mountPath: /custom/certs, readOnly: false}
`, map[string]string{"runner.mode": mode})
			c := ars.Spec.Template.Spec.Containers[0]
			assertUniqueContainerLists(t, c)
			envs := map[string]corev1.EnvVar{}
			for _, env := range c.Env {
				envs[env.Name] = env
			}
			assert.Equal(t, "", envs["DOCKER_HOST"].Value)
			require.NotNil(t, envs["DOCKER_HOST"].ValueFrom)
			assert.Equal(t, "docker-host", envs["DOCKER_HOST"].ValueFrom.SecretKeyRef.Name)
			assert.Equal(t, "custom-pod", envs["ACTIONS_RUNNER_POD_NAME"].Value)
			assert.Nil(t, envs["ACTIONS_RUNNER_POD_NAME"].ValueFrom)
			assert.Equal(t, "/custom/hooks.yaml", envs["ACTIONS_RUNNER_CONTAINER_HOOK_TEMPLATE"].Value)
			assert.Equal(t, "/custom/ca.crt", envs["NODE_EXTRA_CA_CERTS"].Value)
			assert.Equal(t, "0", envs["RUNNER_UPDATE_CA_CERTS"].Value)
			assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{Name: "hook-extension", MountPath: "/home/runner/k8s/hook-template.yaml"})
			assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{Name: "github-server-tls-cert", MountPath: "/custom/certs"})
			if mode == "dind" {
				assert.Equal(t, "0", envs["RUNNER_WAIT_FOR_DOCKER_IN_SECONDS"].Value)
				assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{Name: "dind-sock", MountPath: "/var/run"})
			}
			if mode == "kubernetes" {
				assert.Equal(t, "false", envs["ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER"].Value)
			}
		})
	}
}

func TestExperimentalRunnerRestartPolicy(t *testing.T) {
	for _, mode := range []string{"", "dind", "kubernetes"} {
		for _, policy := range []string{"", "Never", "OnFailure", "Always"} {
			t.Run(mode+"/"+policy, func(t *testing.T) {
				set := map[string]string{"runner.mode": mode}
				want := corev1.RestartPolicyNever
				if policy != "" {
					set["runner.pod.spec.restartPolicy"] = policy
					want = corev1.RestartPolicy(policy)
				}
				ars := runnerSetContract(t, "", set)
				assert.Equal(t, want, ars.Spec.Template.Spec.RestartPolicy)
			})
		}
	}
}

func TestExperimentalRunnerIncludesDoNotMutateValues(t *testing.T) {
	chart := filepath.Join(t.TempDir(), "chart")
	require.NoError(t, os.CopyFS(chart, os.DirFS("..")))
	probe := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: probe
data:
{{- $before := toJson .Values }}
{{- $first := include "runner-mode-dind.runner-container" . }}
{{- $second := include "runner-mode-kubernetes.runner-container" . }}
  unchanged: {{ eq $before (toJson .Values) | quote }}
  repeatable: {{ eq $first (include "runner-mode-dind.runner-container" .) | quote }}
  kubernetesRepeatable: {{ eq $second (include "runner-mode-kubernetes.runner-container" .) | quote }}
`
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates/probe.yaml"), []byte(probe), 0600))
	values := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(values, []byte(contractValues+customizedRunner), 0600))
	output := helm.RenderTemplateContext(t, t.Context(), &helm.Options{
		Logger: logger.Discard, ValuesFiles: []string{values},
	}, chart, "contract", []string{"templates/probe.yaml"})
	var probeConfig corev1.ConfigMap
	require.NoError(t, yaml.UnmarshalStrict([]byte(output), &probeConfig))
	for key, value := range probeConfig.Data {
		assert.Equal(t, "true", value, key)
	}
}

func TestExperimentalRunnerRejectsMalformedLists(t *testing.T) {
	for _, mode := range []string{"", "dind", "kubernetes"} {
		for _, field := range []string{"env", "volumeMounts"} {
			for _, value := range []string{"false", "0", `"invalid"`, "{}"} {
				t.Run(strings.Join([]string{mode, field, value}, "/"), func(t *testing.T) {
					_, err := renderContract(t, fmt.Sprintf("runner:\n  container:\n    %s: %s\n", field, value), map[string]string{"runner.mode": mode})
					require.ErrorContains(t, err, "runner.container."+field+" must be a list")
				})
			}
		}

	}
}

func TestExperimentalRunnerLegacyEnvPrecedence(t *testing.T) {
	for _, mode := range []string{"dind", "kubernetes"} {
		t.Run(mode, func(t *testing.T) {
			ars := runnerSetContract(t, `
runner:
  env:
    - {name: LEGACY_ONLY, value: retained}
    - {name: OVERRIDE, value: old}
    - name: DOCKER_HOST
      valueFrom:
        secretKeyRef: {name: docker-host, key: address}
  container:
    env:
      - {name: OVERRIDE, value: new}
      - {name: DOCKER_HOST, value: "unix:///custom/docker.sock"}
`, map[string]string{"runner.mode": mode})
			c := ars.Spec.Template.Spec.Containers[0]
			assertUniqueContainerLists(t, c)
			assert.Contains(t, c.Env, corev1.EnvVar{Name: "LEGACY_ONLY", Value: "retained"})
			assert.Contains(t, c.Env, corev1.EnvVar{Name: "OVERRIDE", Value: "new"})
			assert.Contains(t, c.Env, corev1.EnvVar{Name: "DOCKER_HOST", Value: "unix:///custom/docker.sock"})
		})
	}
}

func TestExperimentalRunnerRejectsDuplicateIdentities(t *testing.T) {
	for _, mode := range []string{"", "dind", "kubernetes"} {
		for field, list := range map[string]string{
			"env":          "[{name: DUP, value: first}, {name: DUP, value: second}]",
			"volumeMounts": "[{name: a, mountPath: /same}, {name: b, mountPath: /same}]",
		} {
			t.Run(mode+"/"+field, func(t *testing.T) {
				_, err := renderContract(t, "runner:\n  container:\n    "+field+": "+list+"\n", map[string]string{"runner.mode": mode})
				require.ErrorContains(t, err, "runner.container."+field+" contains duplicate")
			})
		}
	}
}
