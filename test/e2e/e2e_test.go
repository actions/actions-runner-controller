package e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/testing"
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
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipped as -short is set")
	}

	Img := func(repo, tag string) testing.ContainerImage {
		return testing.ContainerImage{
			Repo: repo,
			Tag:  tag,
		}
	}

	controllerImageRepo := "actionsrunnercontrollere2e/actions-runner-controller"
	controllerImageTag := "e2e"
	controllerImage := Img(controllerImageRepo, controllerImageTag)
	runnerImageRepo := "actionsrunnercontrollere2e/actions-runner"
	runnerImageTag := "e2e"
	runnerImage := Img(runnerImageRepo, runnerImageTag)

	prebuildImages := []testing.ContainerImage{
		controllerImage,
		runnerImage,
	}

	builds := []testing.DockerBuild{
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

	certManagerVersion := "v1.1.1"

	images := []testing.ContainerImage{
		Img("docker", "dind"),
		Img("quay.io/brancz/kube-rbac-proxy", "v0.10.0"),
		Img("quay.io/jetstack/cert-manager-controller", certManagerVersion),
		Img("quay.io/jetstack/cert-manager-cainjector", certManagerVersion),
		Img("quay.io/jetstack/cert-manager-webhook", certManagerVersion),
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
		certmanagerVersion := "v1.1.1"

		if err := k.Apply(ctx, fmt.Sprintf("https://github.com/jetstack/cert-manager/releases/download/%s/cert-manager.yaml", certmanagerVersion), testing.KubectlConfig{NoValidate: true}); err != nil {
			t.Fatal(err)
		}

		certmanagerKubectlCfg := testing.KubectlConfig{
			Env:       kubectlEnv,
			Namespace: "cert-manager",
			Timeout:   90 * time.Second,
		}

		if err := k.WaitUntilDeployAvailable(ctx, "cert-manager-cainjector", certmanagerKubectlCfg); err != nil {
			t.Fatal(err)
		}

		if err := k.WaitUntilDeployAvailable(ctx, "cert-manager-webhook", certmanagerKubectlCfg.WithTimeout(60*time.Second)); err != nil {
			t.Fatal(err)
		}

		if err := k.WaitUntilDeployAvailable(ctx, "cert-manager", certmanagerKubectlCfg.WithTimeout(60*time.Second)); err != nil {
			t.Fatal(err)
		}

		if err := k.RunKubectlEnsureNS(ctx, "actions-runner-system", testing.KubectlConfig{Env: kubectlEnv}); err != nil {
			t.Fatal(err)
		}
	})

	// If you're using VS Code and wanting to run this test locally,
	// Browse "Workspace Settings" and search for "go test env file" and put e.g. "${workspaceFolder}/.test.env" there
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		t.Fatal("GITHUB_TOKEN must be set")
	}

	scriptEnv := []string{
		"KUBECONFIG=" + k.Kubeconfig(),
		"NAME=" + controllerImageRepo,
		"VERSION=" + controllerImageTag,
		"RUNNER_NAME=" + runnerImageRepo,
		"RUNNER_TAG=" + runnerImageTag,
		"TEST_REPO=" + "actions-runner-controller/mumoshu-actions-test",
		"TEST_ORG=" + "actions-runner-controller",
		"TEST_ORG_REPO=" + "actions-runner-controller/mumoshu-actions-test-org-runners",
		"SYNC_PERIOD=" + "10s",
		"USE_RUNNERSET=" + "1",
		"ACCEPTANCE_TEST_DEPLOYMENT_TOOL=" + "helm",
		"ACCEPTANCE_TEST_SECRET_TYPE=token",
		"GITHUB_TOKEN=" + githubToken,
	}

	t.Run("install actions-runner-controller", func(t *testing.T) {
		if err := k.RunScript(ctx, "../../acceptance/deploy.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv}); err != nil {
			t.Fatal(err)
		}
	})
}
