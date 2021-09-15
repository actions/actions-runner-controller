package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/testing"
	"github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var (
	controllerImageRepo = "actionsrunnercontrollere2e/actions-runner-controller"
	controllerImageTag  = "e2e"
	controllerImage     = testing.Img(controllerImageRepo, controllerImageTag)
	runnerImageRepo     = "actionsrunnercontrollere2e/actions-runner"
	runnerImageTag      = "e2e"
	runnerImage         = testing.Img(runnerImageRepo, runnerImageTag)

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
		testing.Img("docker", "dind"),
		testing.Img("quay.io/brancz/kube-rbac-proxy", "v0.10.0"),
		testing.Img("quay.io/jetstack/cert-manager-controller", certManagerVersion),
		testing.Img("quay.io/jetstack/cert-manager-cainjector", certManagerVersion),
		testing.Img("quay.io/jetstack/cert-manager-webhook", certManagerVersion),
	}

	commonScriptEnv = []string{
		"SYNC_PERIOD=" + "10s",
		"NAME=" + controllerImageRepo,
		"VERSION=" + controllerImageTag,
		"RUNNER_NAME=" + runnerImageRepo,
		"RUNNER_TAG=" + runnerImageTag,
	}

	testResultCMNamePrefix = "test-result-"
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
//
// Instead of relying on "stages" to make it possible to rerun individual tests like terratest,
// you use the "run subtest" feature provided by IDE like VS Code, IDEA, and GoLand.
// Our `testing` package automatically checks for the running test name and skips the cleanup tasks
// whenever the whole test failed, so that you can immediately start fixing issues and rerun inidividual tests.
// See the below link for how terratest handles this:
// https://terratest.gruntwork.io/docs/testing-best-practices/iterating-locally-using-test-stages/
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipped as -short is set")
	}

	env := initTestEnv(t)
	env.useRunnerSet = true

	t.Run("build and load images", func(t *testing.T) {
		env.buildAndLoadImages(t)
	})

	t.Run("install cert-manager", func(t *testing.T) {
		env.installCertManager(t)
	})

	if t.Failed() {
		return
	}

	t.Run("install actions-runner-controller and runners", func(t *testing.T) {
		env.installActionsRunnerController(t)
	})

	if t.Failed() {
		return
	}

	t.Run("Install workflow", func(t *testing.T) {
		env.installActionsWorkflow(t)
	})

	if t.Failed() {
		return
	}

	t.Run("Verify workflow run result", func(t *testing.T) {
		env.verifyActionsWorkflowRun(t)
	})
}

func TestE2ERunnerDeploy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipped as -short is set")
	}

	env := initTestEnv(t)

	t.Run("build and load images", func(t *testing.T) {
		env.buildAndLoadImages(t)
	})

	t.Run("install cert-manager", func(t *testing.T) {
		env.installCertManager(t)
	})

	if t.Failed() {
		return
	}

	t.Run("install actions-runner-controller and runners", func(t *testing.T) {
		env.installActionsRunnerController(t)
	})

	if t.Failed() {
		return
	}

	t.Run("Install workflow", func(t *testing.T) {
		env.installActionsWorkflow(t)
	})

	if t.Failed() {
		return
	}

	t.Run("Verify workflow run result", func(t *testing.T) {
		env.verifyActionsWorkflowRun(t)
	})
}

type env struct {
	*testing.Env

	useRunnerSet bool

	testID                                                   string
	runnerLabel, githubToken, testRepo, testOrg, testOrgRepo string
	testJobs                                                 []job
}

func initTestEnv(t *testing.T) *env {
	t.Helper()

	testingEnv := testing.Start(t, testing.Preload(images...))

	e := &env{Env: testingEnv}

	id := e.ID()

	testID := t.Name() + " " + id

	t.Logf("Using test id %s", testID)

	e.testID = testID
	e.runnerLabel = "test-" + id
	e.githubToken = testing.Getenv(t, "GITHUB_TOKEN")
	e.testRepo = testing.Getenv(t, "TEST_REPO")
	e.testOrg = testing.Getenv(t, "TEST_ORG")
	e.testOrgRepo = testing.Getenv(t, "TEST_ORG_REPO")
	e.testJobs = createTestJobs(id, testResultCMNamePrefix, 2)

	return e
}

func (e *env) f() {
}

func (e *env) buildAndLoadImages(t *testing.T) {
	t.Helper()

	e.DockerBuild(t, builds)
	e.KindLoadImages(t, prebuildImages)
}

func (e *env) installCertManager(t *testing.T) {
	t.Helper()

	applyCfg := testing.KubectlConfig{NoValidate: true}

	e.KubectlApply(t, fmt.Sprintf("https://github.com/jetstack/cert-manager/releases/download/%s/cert-manager.yaml", certManagerVersion), applyCfg)

	waitCfg := testing.KubectlConfig{
		Namespace: "cert-manager",
		Timeout:   90 * time.Second,
	}

	e.KubectlWaitUntilDeployAvailable(t, "cert-manager-cainjector", waitCfg)
	e.KubectlWaitUntilDeployAvailable(t, "cert-manager-webhook", waitCfg.WithTimeout(60*time.Second))
	e.KubectlWaitUntilDeployAvailable(t, "cert-manager", waitCfg.WithTimeout(60*time.Second))
}

func (e *env) installActionsRunnerController(t *testing.T) {
	t.Helper()

	e.createControllerNamespaceAndServiceAccount(t)

	scriptEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig(),
		"ACCEPTANCE_TEST_DEPLOYMENT_TOOL=" + "helm",
		"ACCEPTANCE_TEST_SECRET_TYPE=token",
	}

	if e.useRunnerSet {
		scriptEnv = append(scriptEnv, "USE_RUNNERSET=1")
	}

	varEnv := []string{
		"TEST_REPO=" + e.testRepo,
		"TEST_ORG=" + e.testOrg,
		"TEST_ORG_REPO=" + e.testOrgRepo,
		"GITHUB_TOKEN=" + e.githubToken,
		"RUNNER_LABEL=" + e.runnerLabel,
	}

	scriptEnv = append(scriptEnv, varEnv...)
	scriptEnv = append(scriptEnv, commonScriptEnv...)

	e.RunScript(t, "../../acceptance/deploy.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv})
}

func (e *env) createControllerNamespaceAndServiceAccount(t *testing.T) {
	t.Helper()

	e.KubectlEnsureNS(t, "actions-runner-system", testing.KubectlConfig{})
	e.KubectlEnsureClusterRoleBindingServiceAccount(t, "default-admin", "cluster-admin", "default:default", testing.KubectlConfig{})
}

func (e *env) installActionsWorkflow(t *testing.T) {
	t.Helper()

	installActionsWorkflow(t, e.testID, e.runnerLabel, testResultCMNamePrefix, e.testRepo, e.testJobs)
}

func (e *env) verifyActionsWorkflowRun(t *testing.T) {
	t.Helper()

	verifyActionsWorkflowRun(t, e.Env, e.testJobs)
}

type job struct {
	name, testArg, configMapName string
}

func createTestJobs(id, testResultCMNamePrefix string, numJobs int) []job {
	var testJobs []job

	for i := 0; i < numJobs; i++ {
		name := fmt.Sprintf("test%d", i)
		testArg := fmt.Sprintf("%s%d", id, i)
		configMapName := testResultCMNamePrefix + testArg

		testJobs = append(testJobs, job{name: name, testArg: testArg, configMapName: configMapName})
	}

	return testJobs
}

func installActionsWorkflow(t *testing.T, testID, runnerLabel, testResultCMNamePrefix, testRepo string, testJobs []job) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wfName := "E2E " + testID
	wf := testing.Workflow{
		Name: wfName,
		On: testing.On{
			Push: &testing.Push{
				Branches: []string{"master"},
			},
		},
		Jobs: map[string]testing.Job{},
	}

	for _, j := range testJobs {
		wf.Jobs[j.name] = testing.Job{
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
					Run: fmt.Sprintf("./test.sh %s %s", t.Name(), j.testArg),
				},
			},
		}
	}

	wfContent, err := yaml.Marshal(wf)
	if err != nil {
		t.Fatal(err)
	}

	script := []byte(fmt.Sprintf(`#!/usr/bin/env bash
set -vx
name=$1
id=$2
echo hello from $name
kubectl delete cm %s$id || true
kubectl create cm %s$id --from-literal=status=ok
`, testResultCMNamePrefix, testResultCMNamePrefix))

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
}

func verifyActionsWorkflowRun(t *testing.T, env *testing.Env, testJobs []job) {
	t.Helper()

	var expected []string

	for _ = range testJobs {
		expected = append(expected, "ok")
	}

	gomega.NewGomegaWithT(t).Eventually(func() ([]string, error) {
		var results []string

		var errs []error

		for i := range testJobs {
			testResultCMName := testJobs[i].configMapName

			kubectlEnv := []string{
				"KUBECONFIG=" + env.Kubeconfig(),
			}

			cmCfg := testing.KubectlConfig{
				Env: kubectlEnv,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			m, err := env.Kubectl.GetCMLiterals(ctx, testResultCMName, cmCfg)
			if err != nil {
				errs = append(errs, err)
			} else {
				result := m["status"]
				results = append(results, result)
			}
		}

		var err error

		if len(errs) > 0 {
			var msg string

			for i, e := range errs {
				msg += fmt.Sprintf("error%d: %v\n", i, e)
			}

			err = fmt.Errorf("%d errors occurred: %s", len(errs), msg)
		}

		return results, err
	}, 3*60*time.Second, 10*time.Second).Should(gomega.Equal(expected))
}
