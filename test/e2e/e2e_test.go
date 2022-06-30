package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/testing"
	"github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

type DeployKind int

const (
	RunnerSets DeployKind = iota
	RunnerDeployments
)

var (
	controllerImageRepo = "actionsrunnercontrollere2e/actions-runner-controller"
	controllerImageTag  = "e2e"
	controllerImage     = testing.Img(controllerImageRepo, controllerImageTag)
	runnerImageRepo     = "actionsrunnercontrollere2e/actions-runner"
	runnerDindImageRepo = "actionsrunnercontrollere2e/actions-runner-dind"
	runnerImageTag      = "e2e"
	runnerImage         = testing.Img(runnerImageRepo, runnerImageTag)
	runnerDindImage     = testing.Img(runnerDindImageRepo, runnerImageTag)

	prebuildImages = []testing.ContainerImage{
		controllerImage,
		runnerImage,
		runnerDindImage,
	}

	builds = []testing.DockerBuild{
		{
			Dockerfile:   "../../Dockerfile",
			Args:         []testing.BuildArg{},
			Image:        controllerImage,
			EnableBuildX: true,
		},
		{
			Dockerfile: "../../runner/actions-runner.dockerfile",
			Args: []testing.BuildArg{
				{
					Name:  "RUNNER_VERSION",
					Value: "2.294.0",
				},
			},
			Image:        runnerImage,
			EnableBuildX: true,
		},
		{
			Dockerfile: "../../runner/actions-runner-dind.dockerfile",
			Args: []testing.BuildArg{
				{
					Name:  "RUNNER_VERSION",
					Value: "2.294.0",
				},
			},
			Image:        runnerDindImage,
			EnableBuildX: true,
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
		"SYNC_PERIOD=" + "30s",
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
// If you're using VS Code, open `Workspace Settings` and search for `go test flags`, edit the `.vscode/settings.json` and put the below:
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

	skipRunnerCleanUp := os.Getenv("ARC_E2E_SKIP_RUNNER_CLEANUP") != ""
	retainCluster := os.Getenv("ARC_E2E_RETAIN_CLUSTER") != ""
	skipTestIDCleanUp := os.Getenv("ARC_E2E_SKIP_TEST_ID_CLEANUP") != ""

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

	t.Run("RunnerSets", func(t *testing.T) {
		var (
			testID string
		)

		t.Run("get or generate test ID", func(t *testing.T) {
			testID = env.GetOrGenerateTestID(t)
		})

		if !skipTestIDCleanUp {
			t.Cleanup(func() {
				env.DeleteTestID(t)
			})
		}

		t.Run("install actions-runner-controller v0.24.1", func(t *testing.T) {
			env.installActionsRunnerController(t, "summerwind/actions-runner-controller", "v0.24.1", testID)
		})

		t.Run("deploy runners", func(t *testing.T) {
			env.deploy(t, RunnerSets, testID)
		})

		if !skipRunnerCleanUp {
			t.Cleanup(func() {
				env.undeploy(t, RunnerSets, testID)
			})
		}

		t.Run("install edge actions-runner-controller", func(t *testing.T) {
			env.installActionsRunnerController(t, controllerImageRepo, controllerImageTag, testID)
		})

		if t.Failed() {
			return
		}

		t.Run("Install workflow", func(t *testing.T) {
			env.installActionsWorkflow(t, testID)
		})

		if t.Failed() {
			return
		}

		t.Run("Verify workflow run result", func(t *testing.T) {
			env.verifyActionsWorkflowRun(t, testID)
		})
	})

	t.Run("RunnerDeployments", func(t *testing.T) {
		var (
			testID string
		)

		t.Run("get or generate test ID", func(t *testing.T) {
			testID = env.GetOrGenerateTestID(t)
		})

		if !skipTestIDCleanUp {
			t.Cleanup(func() {
				env.DeleteTestID(t)
			})
		}

		t.Run("install actions-runner-controller v0.24.1", func(t *testing.T) {
			env.installActionsRunnerController(t, "summerwind/actions-runner-controller", "v0.24.1", testID)
		})

		t.Run("deploy runners", func(t *testing.T) {
			env.deploy(t, RunnerDeployments, testID)
		})

		if !skipRunnerCleanUp {
			t.Cleanup(func() {
				env.undeploy(t, RunnerDeployments, testID)
			})
		}

		t.Run("install edge actions-runner-controller", func(t *testing.T) {
			env.installActionsRunnerController(t, controllerImageRepo, controllerImageTag, testID)
		})

		if t.Failed() {
			return
		}

		t.Run("Install workflow", func(t *testing.T) {
			env.installActionsWorkflow(t, testID)
		})

		if t.Failed() {
			return
		}

		t.Run("Verify workflow run result", func(t *testing.T) {
			env.verifyActionsWorkflowRun(t, testID)
		})
	})

	if retainCluster {
		t.FailNow()
	}
}

type env struct {
	*testing.Env

	// Uses GITHUB_APP_ID, GITHUB_APP_INSTALLATION_ID, and GITHUB_APP_PRIVATE_KEY
	// to let ARC authenticate as a GitHub App
	useApp bool

	testName                                    string
	repoToCommit                                string
	appID, appInstallationID, appPrivateKeyFile string
	githubToken, testRepo, testOrg, testOrgRepo string
	githubTokenWebhook                          string
	testEnterprise                              string
	testEphemeral                               string
	scaleDownDelaySecondsAfterScaleOut          int64
	minReplicas                                 int64
	dockerdWithinRunnerContainer                bool
}

func initTestEnv(t *testing.T) *env {
	t.Helper()

	testingEnv := testing.Start(t, testing.Preload(images...))

	e := &env{Env: testingEnv}

	testName := t.Name()

	t.Logf("Initializing test with name %s", testName)

	e.testName = testName
	e.githubToken = testing.Getenv(t, "GITHUB_TOKEN")
	e.appID = testing.Getenv(t, "GITHUB_APP_ID")
	e.appInstallationID = testing.Getenv(t, "GITHUB_APP_INSTALLATION_ID")
	e.appPrivateKeyFile = testing.Getenv(t, "GITHUB_APP_PRIVATE_KEY_FILE")
	e.githubTokenWebhook = testing.Getenv(t, "WEBHOOK_GITHUB_TOKEN")
	e.repoToCommit = testing.Getenv(t, "TEST_COMMIT_REPO")
	e.testRepo = testing.Getenv(t, "TEST_REPO", "")
	e.testOrg = testing.Getenv(t, "TEST_ORG", "")
	e.testOrgRepo = testing.Getenv(t, "TEST_ORG_REPO", "")
	e.testEnterprise = testing.Getenv(t, "TEST_ENTERPRISE", "")
	e.testEphemeral = testing.Getenv(t, "TEST_EPHEMERAL", "")

	e.scaleDownDelaySecondsAfterScaleOut, _ = strconv.ParseInt(testing.Getenv(t, "TEST_RUNNER_SCALE_DOWN_DELAY_SECONDS_AFTER_SCALE_OUT", "10"), 10, 32)
	e.minReplicas, _ = strconv.ParseInt(testing.Getenv(t, "TEST_RUNNER_MIN_REPLICAS", "1"), 10, 32)

	var err error
	e.dockerdWithinRunnerContainer, err = strconv.ParseBool(testing.Getenv(t, "TEST_RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER", "false"))
	if err != nil {
		panic(fmt.Sprintf("unable to parse bool from TEST_RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER: %v", err))
	}

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

func (e *env) installActionsRunnerController(t *testing.T, repo, tag, testID string) {
	t.Helper()

	e.createControllerNamespaceAndServiceAccount(t)

	scriptEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig(),
		"ACCEPTANCE_TEST_DEPLOYMENT_TOOL=" + "helm",
	}

	varEnv := []string{
		"WEBHOOK_GITHUB_TOKEN=" + e.githubTokenWebhook,
		"TEST_ID=" + testID,
		"NAME=" + repo,
		"VERSION=" + tag,
	}

	if e.useApp {
		varEnv = append(varEnv,
			"ACCEPTANCE_TEST_SECRET_TYPE=app",
			"APP_ID="+e.appID,
			"APP_INSTALLATION_ID="+e.appInstallationID,
			"APP_PRIVATE_KEY_FILE="+e.appPrivateKeyFile,
		)
	} else {
		varEnv = append(varEnv,
			"ACCEPTANCE_TEST_SECRET_TYPE=token",
			"GITHUB_TOKEN="+e.githubToken,
		)
	}

	scriptEnv = append(scriptEnv, varEnv...)
	scriptEnv = append(scriptEnv, commonScriptEnv...)

	e.RunScript(t, "../../acceptance/deploy.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv})
}

func (e *env) deploy(t *testing.T, kind DeployKind, testID string) {
	t.Helper()
	e.do(t, "apply", kind, testID)
}

func (e *env) undeploy(t *testing.T, kind DeployKind, testID string) {
	t.Helper()
	e.do(t, "delete", kind, testID)
}

func (e *env) do(t *testing.T, op string, kind DeployKind, testID string) {
	t.Helper()

	e.createControllerNamespaceAndServiceAccount(t)

	scriptEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig(),
		"OP=" + op,
	}

	switch kind {
	case RunnerSets:
		scriptEnv = append(scriptEnv, "USE_RUNNERSET=1")
	case RunnerDeployments:
		scriptEnv = append(scriptEnv, "USE_RUNNERSET=false")
	default:
		t.Fatalf("Invalid deploy kind %v", kind)
	}

	varEnv := []string{
		"TEST_ENTERPRISE=" + e.testEnterprise,
		"TEST_REPO=" + e.testRepo,
		"TEST_ORG=" + e.testOrg,
		"TEST_ORG_REPO=" + e.testOrgRepo,
		"RUNNER_LABEL=" + e.runnerLabel(testID),
		"TEST_EPHEMERAL=" + e.testEphemeral,
		fmt.Sprintf("RUNNER_SCALE_DOWN_DELAY_SECONDS_AFTER_SCALE_OUT=%d", e.scaleDownDelaySecondsAfterScaleOut),
		fmt.Sprintf("REPO_RUNNER_MIN_REPLICAS=%d", e.minReplicas),
		fmt.Sprintf("ORG_RUNNER_MIN_REPLICAS=%d", e.minReplicas),
		fmt.Sprintf("ENTERPRISE_RUNNER_MIN_REPLICAS=%d", e.minReplicas),
	}

	if e.dockerdWithinRunnerContainer {
		varEnv = append(varEnv,
			"RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER=true",
			"RUNNER_NAME="+runnerDindImageRepo,
		)
	} else {
		varEnv = append(varEnv,
			"RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER=false",
			"RUNNER_NAME="+runnerImageRepo,
		)
	}

	scriptEnv = append(scriptEnv, varEnv...)
	scriptEnv = append(scriptEnv, commonScriptEnv...)

	e.RunScript(t, "../../acceptance/deploy_runners.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv})
}

func (e *env) runnerLabel(testID string) string {
	return "test-" + testID
}

func (e *env) createControllerNamespaceAndServiceAccount(t *testing.T) {
	t.Helper()

	e.KubectlEnsureNS(t, "actions-runner-system", testing.KubectlConfig{})
	e.KubectlEnsureClusterRoleBindingServiceAccount(t, "default-admin", "cluster-admin", "default:default", testing.KubectlConfig{})
}

func (e *env) installActionsWorkflow(t *testing.T, testID string) {
	t.Helper()

	installActionsWorkflow(t, e.testName+" "+testID, e.runnerLabel(testID), testResultCMNamePrefix, e.repoToCommit, e.testJobs(testID))
}

func (e *env) testJobs(testID string) []job {
	return createTestJobs(testID, testResultCMNamePrefix, 6)
}

func (e *env) verifyActionsWorkflowRun(t *testing.T, testID string) {
	t.Helper()

	verifyActionsWorkflowRun(t, e.Env, e.testJobs(testID))
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

const Branch = "main"

func installActionsWorkflow(t *testing.T, testName, runnerLabel, testResultCMNamePrefix, testRepo string, testJobs []job) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wfName := "E2E " + testName
	wf := testing.Workflow{
		Name: wfName,
		On: testing.On{
			Push: &testing.Push{
				Branches: []string{Branch},
			},
		},
		Jobs: map[string]testing.Job{},
	}

	kubernetesContainerMode := os.Getenv("TEST_CONTAINER_MODE") == "kubernetes"

	var container string
	if kubernetesContainerMode {
		container = "golang:1.18"
	}

	for _, j := range testJobs {
		steps := []testing.Step{
			{
				Uses: testing.ActionsCheckout,
			},
		}

		if !kubernetesContainerMode {
			steps = append(steps,
				testing.Step{
					// This might be the easiest way to handle permissions without use of securityContext
					// https://stackoverflow.com/questions/50156124/kubernetes-nfs-persistent-volumes-permission-denied#comment107483717_53186320
					Run: "sudo chmod 777 -R \"${RUNNER_TOOL_CACHE}\" \"${HOME}/.cache\" \"/var/lib/docker\"",
				},
				testing.Step{
					// This might be the easiest way to handle permissions without use of securityContext
					// https://stackoverflow.com/questions/50156124/kubernetes-nfs-persistent-volumes-permission-denied#comment107483717_53186320
					Run: "ls -lah \"${RUNNER_TOOL_CACHE}\" \"${HOME}/.cache\" \"/var/lib/docker\"",
				},
				testing.Step{
					Uses: "actions/setup-go@v3",
					With: &testing.With{
						GoVersion: "1.18.2",
					},
				},
			)
		}

		steps = append(steps,
			testing.Step{
				Run: "go version",
			},
			testing.Step{
				Run: "go build .",
			},
		)

		if !kubernetesContainerMode {
			steps = append(steps,
				testing.Step{
					// https://github.com/docker/buildx/issues/413#issuecomment-710660155
					// To prevent setup-buildx-action from failing with:
					//   error: could not create a builder instance with TLS data loaded from environment. Please use `docker context create <context-name>` to create a context for current environment and then create a builder instance with `docker buildx create <context-name>`
					Run: "docker context create mycontext",
				},
				testing.Step{
					Run: "docker context use mycontext",
				},
				testing.Step{
					Name: "Set up Docker Buildx",
					Uses: "docker/setup-buildx-action@v1",
					With: &testing.With{
						BuildkitdFlags: "--debug",
						Endpoint:       "mycontext",
						// As the consequence of setting `install: false`, it doesn't install buildx as an alias to `docker build`
						// so we need to use `docker buildx build` in the next step
						Install: false,
					},
				},
				testing.Step{
					Run: "docker buildx build --platform=linux/amd64 " +
						"--cache-from=type=local,src=/home/runner/.cache/buildx " +
						"--cache-to=type=local,dest=/home/runner/.cache/buildx-new,mode=max " +
						".",
				},
				testing.Step{
					// https://github.com/docker/build-push-action/blob/master/docs/advanced/cache.md#local-cache
					// See https://github.com/moby/buildkit/issues/1896 for why this is needed
					Run: "rm -rf /home/runner/.cache/buildx && mv /home/runner/.cache/buildx-new /home/runner/.cache/buildx",
				},
				testing.Step{
					Run: "ls -lah /home/runner/.cache/*",
				},
				testing.Step{
					Uses: "azure/setup-kubectl@v1",
					With: &testing.With{
						Version: "v1.20.2",
					},
				},
				testing.Step{
					Run: fmt.Sprintf("./test.sh %s %s", t.Name(), j.testArg),
				},
			)
		}

		wf.Jobs[j.name] = testing.Job{
			RunsOn:    runnerLabel,
			Container: container,
			Steps:     steps,
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
		Branch: Branch,
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
