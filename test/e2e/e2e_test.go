package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/testing"
	"github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var (
	Img = func(repo, tag string) testing.ContainerImage {
		return testing.ContainerImage{
			Repo: repo,
			Tag:  tag,
		}
	}

	controllerImageRepo = "actionsrunnercontrollere2e/actions-runner-controller"
	controllerImageTag  = "e2e"
	controllerImage     = Img(controllerImageRepo, controllerImageTag)
	runnerImageRepo     = "actionsrunnercontrollere2e/actions-runner"
	runnerImageTag      = "e2e"
	runnerImage         = Img(runnerImageRepo, runnerImageTag)

	prebuildImages = []testing.ContainerImage{
		controllerImage,
		runnerImage,
	}

	builds = []testing.DockerBuild{
		{
			Dockerfile: "../../Dockerfile",
			Args:       []testing.BuildArg{},
			Image:      controllerImage,
		},
		{
			Dockerfile: "../../runner/Dockerfile",
			Args:       []testing.BuildArg{},
			Image:      runnerImage,
		},
	}

	certManagerVersion = "v1.1.1"

	images = []testing.ContainerImage{
		Img("docker", "dind"),
		Img("quay.io/brancz/kube-rbac-proxy", "v0.10.0"),
		Img("quay.io/jetstack/cert-manager-controller", certManagerVersion),
		Img("quay.io/jetstack/cert-manager-cainjector", certManagerVersion),
		Img("quay.io/jetstack/cert-manager-webhook", certManagerVersion),
	}
)

// If you're willing to run this test via VS Code "run test" or "debug test",
// almost certainly you'd want to make the default go test timeout from 30s to longer and enough value.
// Press Cmd + Shift + P, type "Workspace Settings" and open it, and type "go test timeout" and set e.g. 600s there.
// See https://github.com/golang/vscode-go/blob/master/docs/settings.md#gotesttimeout for more information.
//
// This tests ues testing.Logf extensively for debugging purpose.
// But messages logged via Logf shows up only when the test failed by default.
// To always enable logging, do not forget to pass `-test.v` to `go test`.
// If you're using VS Code, open `Workspace Settings` and search for `go test flags`, edit the `settings.json` and put the below:
//   "go.testFlags": ["-v"]
//
// This function requires a few environment variables to be set to provide some test data.
// If you're using VS Code and wanting to run this test locally,
// Browse "Workspace Settings" and search for "go test env file" and put e.g. "${workspaceFolder}/.test.env" there.
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipped as -short is set")
	}

	k := testing.Start(t, testing.Cluster{}, testing.Preload(images...))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	t.Run("build images", func(t *testing.T) {
		if err := k.BuildImages(ctx, builds); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("load images", func(t *testing.T) {
		if err := k.LoadImages(ctx, prebuildImages); err != nil {
			t.Fatal(err)
		}
	})

	kubectlEnv := []string{
		"KUBECONFIG=" + k.Kubeconfig(),
	}

	t.Run("install cert-manager", func(t *testing.T) {
		applyCfg := testing.KubectlConfig{NoValidate: true, Env: kubectlEnv}

		if err := k.Apply(ctx, fmt.Sprintf("https://github.com/jetstack/cert-manager/releases/download/%s/cert-manager.yaml", certManagerVersion), applyCfg); err != nil {
			t.Fatal(err)
		}

		waitCfg := testing.KubectlConfig{
			Env:       kubectlEnv,
			Namespace: "cert-manager",
			Timeout:   90 * time.Second,
		}

		if err := k.WaitUntilDeployAvailable(ctx, "cert-manager-cainjector", waitCfg); err != nil {
			t.Fatal(err)
		}

		if err := k.WaitUntilDeployAvailable(ctx, "cert-manager-webhook", waitCfg.WithTimeout(60*time.Second)); err != nil {
			t.Fatal(err)
		}

		if err := k.WaitUntilDeployAvailable(ctx, "cert-manager", waitCfg.WithTimeout(60*time.Second)); err != nil {
			t.Fatal(err)
		}

		if err := k.RunKubectlEnsureNS(ctx, "actions-runner-system", testing.KubectlConfig{Env: kubectlEnv}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("make default serviceaccount cluster-admin", func(t *testing.T) {
		cfg := testing.KubectlConfig{Env: kubectlEnv}
		bindingName := "default-admin"
		if _, err := k.GetClusterRoleBinding(ctx, bindingName, cfg); err != nil {
			if err := k.CreateClusterRoleBindingServiceAccount(ctx, bindingName, "cluster-admin", "default:default", cfg); err != nil {
				t.Fatal(err)
			}
		}
	})

	cmCfg := testing.KubectlConfig{
		Env: kubectlEnv,
	}
	testInfoName := "test-info"

	m, _ := k.GetCMLiterals(ctx, testInfoName, cmCfg)

	t.Run("Save test ID", func(t *testing.T) {
		if m == nil {
			id := RandStringBytesRmndr(10)
			m = map[string]string{"id": id}
			if err := k.CreateCMLiterals(ctx, testInfoName, m, cmCfg); err != nil {
				t.Fatal(err)
			}
		}
	})

	id := m["id"]

	runnerLabel := "test-" + id

	testID := t.Name() + " " + id

	t.Logf("Using test id %s", testID)

	githubToken := getenv(t, "GITHUB_TOKEN")
	testRepo := getenv(t, "TEST_REPO")
	testOrg := getenv(t, "TEST_ORG")
	testOrgRepo := getenv(t, "TEST_ORG_REPO")

	if t.Failed() {
		return
	}

	t.Run("install actions-runner-controller and runners", func(t *testing.T) {
		scriptEnv := []string{
			"KUBECONFIG=" + k.Kubeconfig(),
			"ACCEPTANCE_TEST_DEPLOYMENT_TOOL=" + "helm",
			"ACCEPTANCE_TEST_SECRET_TYPE=token",
			"NAME=" + controllerImageRepo,
			"VERSION=" + controllerImageTag,
			"RUNNER_NAME=" + runnerImageRepo,
			"RUNNER_TAG=" + runnerImageTag,
			"TEST_REPO=" + testRepo,
			"TEST_ORG=" + testOrg,
			"TEST_ORG_REPO=" + testOrgRepo,
			"SYNC_PERIOD=" + "10s",
			"USE_RUNNERSET=" + "1",
			"GITHUB_TOKEN=" + githubToken,
			"RUNNER_LABEL=" + runnerLabel,
		}

		if err := k.RunScript(ctx, "../../acceptance/deploy.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv}); err != nil {
			t.Fatal(err)
		}
	})

	testResultCMName := fmt.Sprintf("test-result-%s", id)

	if t.Failed() {
		return
	}

	t.Run("Install workflow", func(t *testing.T) {
		wfName := "E2E " + testID
		wf := testing.Workflow{
			Name: wfName,
			On: testing.On{
				Push: &testing.Push{
					Branches: []string{"main"},
				},
			},
			Jobs: map[string]testing.Job{
				"test": {
					RunsOn: runnerLabel,
					Steps: []testing.Step{
						{
							Uses: testing.ActionsCheckoutV2,
						},
						{
							Uses: "azure/setup-kubectl@v1",
							With: &testing.With{
								Version: "v1.20.2",
							},
						},
						{
							Run: "./test.sh",
						},
					},
				},
			},
		}

		wfContent, err := yaml.Marshal(wf)
		if err != nil {
			t.Fatal(err)
		}

		script := []byte(fmt.Sprintf(`#!/usr/bin/env bash
set -vx
echo hello from %s
kubectl delete cm %s || true
kubectl create cm %s --from-literal=status=ok
`, testID, testResultCMName, testResultCMName))

		g := testing.GitRepo{
			Dir:           filepath.Join(t.TempDir(), "gitrepo"),
			Name:          testRepo,
			CommitMessage: wfName,
			Contents: map[string][]byte{
				".github/workflows/workflow.yaml": wfContent,
				"test.sh":                         script,
			},
		}

		if err := g.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	})

	if t.Failed() {
		return
	}

	t.Run("Verify workflow run result", func(t *testing.T) {
		gomega.NewGomegaWithT(t).Eventually(func() (string, error) {
			m, err := k.GetCMLiterals(ctx, testResultCMName, cmCfg)
			if err != nil {
				return "", err
			}

			result := m["status"]

			return result, nil
		}, 60*time.Second, 10*time.Second).Should(gomega.Equal("ok"))
	})
}

func getenv(t *testing.T, name string) string {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		t.Fatal(name + " must be set")
	}
	return v
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

const letterBytes = "abcdefghijklmnopqrstuvwxyz"

// Copied from https://stackoverflow.com/a/31832326 with thanks
func RandStringBytesRmndr(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Int63()%int64(len(letterBytes))]
	}
	return string(b)
}
