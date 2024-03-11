package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/testing"
	"github.com/google/go-github/v52/github"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"sigs.k8s.io/yaml"
)

type DeployKind int

const (
	RunnerSets DeployKind = iota
	RunnerDeployments
)

var (
	// See the below link for maintained versions of cert-manager
	// https://cert-manager.io/docs/installation/supported-releases/
	certManagerVersion = "v1.8.2"

	arcStableImageRepo = "summerwind/actions-runner-controller"
	arcStableImageTag  = "v0.25.2"

	testResultCMNamePrefix = "test-result-"

	RunnerVersion               = "2.314.1"
	RunnerContainerHooksVersion = "0.5.1"
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
//
//	"go.testFlags": ["-v"]
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
//
// This functions leaves PVs undeleted. To delete PVs, run:
//
//	kubectl get pv -ojson | jq -rMc '.items[] | select(.status.phase == "Available") | {name:.metadata.name, status:.status.phase} | .name' | xargs kubectl delete pv
//
// If you disk full after dozens of test runs, try:
//
//	docker system prune
//
// and
//
//	kind delete cluster --name teste2e
//
// The former tend to release 200MB-3GB and the latter can result in releasing like 100GB due to kind node contains loaded container images and
// (in case you use it) local provisioners disk image(which is implemented as a directory within the kind node).
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipped as -short is set")
	}

	k8sMinorVer := os.Getenv("ARC_E2E_KUBE_VERSION")
	skipRunnerCleanUp := os.Getenv("ARC_E2E_SKIP_RUNNER_CLEANUP") != ""
	retainCluster := os.Getenv("ARC_E2E_RETAIN_CLUSTER") != ""
	skipTestIDCleanUp := os.Getenv("ARC_E2E_SKIP_TEST_ID_CLEANUP") != ""
	skipArgoTunnelCleanUp := os.Getenv("ARC_E2E_SKIP_ARGO_TUNNEL_CLEAN_UP") != ""

	vars := buildVars(
		os.Getenv("ARC_E2E_IMAGE_REPO"),
		os.Getenv("UBUNTU_VERSION"),
	)

	testedVersions := []struct {
		label                     string
		controller, controllerVer string
		chart, chartVer           string
		opt                       []InstallARCOption
	}{
		{
			label:         "stable",
			controller:    arcStableImageRepo,
			controllerVer: arcStableImageTag,
			chart:         "actions-runner-controller/actions-runner-controller",
			// 0.20.2 accidentally added support for runner-status-update which isn't supported by ARC 0.25.2.
			// With some chart values, the controller end up with crashlooping with `flag provided but not defined: -runner-status-update-hook`.
			chartVer: "0.20.1",
		},
		{
			label:         "edge",
			controller:    vars.controllerImageRepo,
			controllerVer: vars.controllerImageTag,
			chart:         "",
			chartVer:      "",
			opt: []InstallARCOption{
				func(ia *InstallARCConfig) {
					ia.GithubWebhookServerEnvName = "FOO"
					ia.GithubWebhookServerEnvValue = "foo"
				},
			},
		},
	}

	env := initTestEnv(t, k8sMinorVer, vars)
	if vt := os.Getenv("ARC_E2E_VERIFY_TIMEOUT"); vt != "" {
		var err error
		env.VerifyTimeout, err = time.ParseDuration(vt)
		if err != nil {
			t.Fatalf("Failed to parse duration %q: %v", vt, err)
		}
	}
	env.doDockerBuild = os.Getenv("ARC_E2E_DO_DOCKER_BUILD") != ""

	t.Run("build and load images", func(t *testing.T) {
		env.buildAndLoadImages(t)
	})

	if t.Failed() {
		return
	}

	t.Run("install cert-manager", func(t *testing.T) {
		env.installCertManager(t)
	})

	if t.Failed() {
		return
	}

	t.Run("RunnerSets", func(t *testing.T) {
		if os.Getenv("ARC_E2E_SKIP_RUNNERSETS") != "" {
			t.Skip("RunnerSets test has been skipped due to ARC_E2E_SKIP_RUNNERSETS")
		}

		var testID string

		t.Run("get or generate test ID", func(t *testing.T) {
			testID = env.GetOrGenerateTestID(t)
		})

		if !skipTestIDCleanUp {
			t.Cleanup(func() {
				env.DeleteTestID(t)
			})
		}

		if t.Failed() {
			return
		}

		t.Run("install argo-tunnel", func(t *testing.T) {
			env.installArgoTunnel(t)
		})

		if !skipArgoTunnelCleanUp {
			t.Cleanup(func() {
				env.uninstallArgoTunnel(t)
			})
		}

		if t.Failed() {
			return
		}

		for i, v := range testedVersions {
			t.Run("install actions-runner-controller "+v.label, func(t *testing.T) {
				t.Logf("Using controller %s:%s and chart %s:%s", v.controller, v.controllerVer, v.chart, v.chartVer)
				env.installActionsRunnerController(t, v.controller, v.controllerVer, testID, v.chart, v.chartVer, v.opt...)
			})

			if t.Failed() {
				return
			}

			if i > 0 {
				continue
			}

			t.Run("deploy runners", func(t *testing.T) {
				env.deploy(t, RunnerSets, testID)
			})

			if !skipRunnerCleanUp {
				t.Cleanup(func() {
					env.undeploy(t, RunnerSets, testID)
				})
			}

			if t.Failed() {
				return
			}
		}

		t.Run("Install workflow", func(t *testing.T) {
			env.installActionsWorkflow(t, RunnerSets, testID)
		})

		if t.Failed() {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			var cancelled bool
			defer func() {
				if !cancelled {
					t.Logf("Stopping the continuous rolling-update of runners due to error(s)")
				}
				cancel()
			}()

			for i := 1; ; i++ {
				if t.Failed() {
					cancelled = true
					return
				}

				select {
				case _, ok := <-ctx.Done():
					if !ok {
						t.Logf("Stopping the continuous rolling-update of runners")
					}
					cancelled = true
				default:
					time.Sleep(60 * time.Second)

					t.Run(fmt.Sprintf("update runners attempt %d", i), func(t *testing.T) {
						env.deploy(t, RunnerSets, testID, fmt.Sprintf("ROLLING_UPDATE_PHASE=%d", i))
					})
				}
			}
		}()
		t.Cleanup(func() {
			cancel()
		})

		t.Run("Verify workflow run result", func(t *testing.T) {
			env.verifyActionsWorkflowRun(t, ctx, testID)
		})
	})

	t.Run("RunnerDeployments", func(t *testing.T) {
		if os.Getenv("ARC_E2E_SKIP_RUNNERDEPLOYMENT") != "" {
			t.Skip("RunnerSets test has been skipped due to ARC_E2E_SKIP_RUNNERSETS")
		}

		var testID string

		t.Run("get or generate test ID", func(t *testing.T) {
			testID = env.GetOrGenerateTestID(t)
		})

		if !skipTestIDCleanUp {
			t.Cleanup(func() {
				env.DeleteTestID(t)
			})
		}

		if t.Failed() {
			return
		}

		t.Run("install argo-tunnel", func(t *testing.T) {
			env.installArgoTunnel(t)
		})

		if !skipArgoTunnelCleanUp {
			t.Cleanup(func() {
				env.uninstallArgoTunnel(t)
			})
		}

		if t.Failed() {
			return
		}

		for i, v := range testedVersions {
			t.Run("install actions-runner-controller "+v.label, func(t *testing.T) {
				t.Logf("Using controller %s:%s and chart %s:%s", v.controller, v.controllerVer, v.chart, v.chartVer)
				env.installActionsRunnerController(t, v.controller, v.controllerVer, testID, v.chart, v.chartVer, v.opt...)
			})

			if t.Failed() {
				return
			}

			if i > 0 {
				continue
			}

			t.Run("deploy runners", func(t *testing.T) {
				env.deploy(t, RunnerDeployments, testID)
			})

			if !skipRunnerCleanUp {
				t.Cleanup(func() {
					env.undeploy(t, RunnerDeployments, testID)
				})
			}

			if t.Failed() {
				return
			}
		}

		t.Run("Install workflow", func(t *testing.T) {
			env.installActionsWorkflow(t, RunnerDeployments, testID)
		})

		if t.Failed() {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			var cancelled bool
			defer func() {
				if !cancelled {
					t.Logf("Stopping the continuous rolling-update of runners due to error(s)")
				}
				cancel()
			}()

			for i := 1; ; i++ {
				if t.Failed() {
					cancelled = true
					return
				}

				select {
				case _, ok := <-ctx.Done():
					if !ok {
						t.Logf("Stopping the continuous rolling-update of runners")
					}
					cancelled = true
					return
				default:
					time.Sleep(10 * time.Second)

					t.Run(fmt.Sprintf("update runners - attempt %d", i), func(t *testing.T) {
						env.deploy(t, RunnerDeployments, testID, fmt.Sprintf("ROLLING_UPDATE_PHASE=%d", i))
					})

					t.Run(fmt.Sprintf("set deletiontimestamps on runner pods - attempt %d", i), func(t *testing.T) {
						env.setDeletionTimestampsOnRunningPods(t, RunnerDeployments)
					})
				}
			}
		}()
		t.Cleanup(func() {
			cancel()
		})

		t.Run("Verify workflow run result", func(t *testing.T) {
			env.verifyActionsWorkflowRun(t, ctx, testID)
		})
	})

	if retainCluster {
		t.FailNow()
	}
}

type env struct {
	*testing.Env

	Kind *testing.Kind

	// Uses GITHUB_APP_ID, GITHUB_APP_INSTALLATION_ID, and GITHUB_APP_PRIVATE_KEY
	// to let ARC authenticate as a GitHub App
	useApp bool

	testName                                    string
	repoToCommit                                string
	appID, appInstallationID, appPrivateKeyFile string
	githubToken, testRepo, testOrg, testOrgRepo string
	githubTokenWebhook                          string
	createSecretsUsingHelm                      string
	testEnterprise                              string
	testEphemeral                               string
	scaleDownDelaySecondsAfterScaleOut          int64
	minReplicas                                 int64
	dockerdWithinRunnerContainer                bool
	rootlessDocker                              bool
	doDockerBuild                               bool
	containerMode                               string
	runnerServiceAccuontName                    string
	runnerGracefulStopTimeout                   string
	runnerTerminationGracePeriodSeconds         string
	runnerNamespace                             string
	logFormat                                   string
	remoteKubeconfig                            string
	admissionWebhooksTimeout                    string
	imagePullSecretName                         string
	imagePullPolicy                             string
	dindSidecarRepositoryAndTag                 string
	watchNamespace                              string

	vars          vars
	VerifyTimeout time.Duration
}

type vars struct {
	controllerImageRepo, controllerImageTag string

	runnerImageRepo             string
	runnerDindImageRepo         string
	runnerRootlessDindImageRepo string

	dindSidecarImageRepo, dindSidecarImageTag string

	prebuildImages []testing.ContainerImage
	builds         []testing.DockerBuild

	commonScriptEnv []string
}

func buildVars(repo, ubuntuVer string) vars {
	if repo == "" {
		repo = "actionsrunnercontrollere2e"
	}

	var (
		controllerImageRepo         = repo + "/actions-runner-controller"
		controllerImageTag          = "e2e"
		controllerImage             = testing.Img(controllerImageRepo, controllerImageTag)
		runnerImageRepo             = repo + "/actions-runner"
		runnerDindImageRepo         = repo + "/actions-runner-dind"
		runnerRootlessDindImageRepo = repo + "/actions-runner-rootless-dind"
		runnerImageTag              = "e2e"
		runnerImage                 = testing.Img(runnerImageRepo, runnerImageTag)
		runnerDindImage             = testing.Img(runnerDindImageRepo, runnerImageTag)
		runnerRootlessDindImage     = testing.Img(runnerRootlessDindImageRepo, runnerImageTag)

		dindSidecarImageRepo = "docker"
		dindSidecarImageTag  = "24.0.7-dind"
		dindSidecarImage     = testing.Img(dindSidecarImageRepo, dindSidecarImageTag)
	)

	var vs vars

	vs.controllerImageRepo, vs.controllerImageTag = controllerImageRepo, controllerImageTag
	vs.runnerDindImageRepo = runnerDindImageRepo
	vs.runnerRootlessDindImageRepo = runnerRootlessDindImageRepo
	vs.runnerImageRepo = runnerImageRepo

	vs.dindSidecarImageRepo = dindSidecarImageRepo
	vs.dindSidecarImageTag = dindSidecarImageTag

	// vs.controllerImage, vs.controllerImageTag

	vs.prebuildImages = []testing.ContainerImage{
		controllerImage,
		runnerImage,
		runnerDindImage,
		runnerRootlessDindImage,
		dindSidecarImage,
	}

	vs.builds = []testing.DockerBuild{
		{
			Dockerfile:   "../../Dockerfile",
			Args:         []testing.BuildArg{},
			Image:        controllerImage,
			EnableBuildX: true,
		},
		{
			Dockerfile: fmt.Sprintf("../../runner/actions-runner.ubuntu-%s.dockerfile", ubuntuVer),
			Args: []testing.BuildArg{
				{
					Name:  "RUNNER_VERSION",
					Value: RunnerVersion,
				},
				{
					Name:  "RUNNER_CONTAINER_HOOKS_VERSION",
					Value: RunnerContainerHooksVersion,
				},
			},
			Image:        runnerImage,
			EnableBuildX: true,
		},
		{
			Dockerfile: fmt.Sprintf("../../runner/actions-runner-dind.ubuntu-%s.dockerfile", ubuntuVer),
			Args: []testing.BuildArg{
				{
					Name:  "RUNNER_VERSION",
					Value: RunnerVersion,
				},
				{
					Name:  "RUNNER_CONTAINER_HOOKS_VERSION",
					Value: RunnerContainerHooksVersion,
				},
			},
			Image:        runnerDindImage,
			EnableBuildX: true,
		},
		{
			Dockerfile: fmt.Sprintf("../../runner/actions-runner-dind-rootless.ubuntu-%s.dockerfile", ubuntuVer),
			Args: []testing.BuildArg{
				{
					Name:  "RUNNER_VERSION",
					Value: RunnerVersion,
				},
				{
					Name:  "RUNNER_CONTAINER_HOOKS_VERSION",
					Value: RunnerContainerHooksVersion,
				},
			},
			Image:        runnerRootlessDindImage,
			EnableBuildX: true,
		},
	}

	vs.commonScriptEnv = []string{
		"SYNC_PERIOD=" + "30s",
		"RUNNER_TAG=" + runnerImageTag,
	}

	return vs
}

func initTestEnv(t *testing.T, k8sMinorVer string, vars vars) *env {
	t.Helper()

	testingEnv := testing.Start(t, k8sMinorVer)

	e := &env{Env: testingEnv}

	testName := t.Name()

	t.Logf("Initializing test with name %s", testName)

	e.testName = testName
	e.githubToken = testing.Getenv(t, "GITHUB_TOKEN")
	e.appID = testing.Getenv(t, "GITHUB_APP_ID")
	e.appInstallationID = testing.Getenv(t, "GITHUB_APP_INSTALLATION_ID")
	e.appPrivateKeyFile = testing.Getenv(t, "GITHUB_APP_PRIVATE_KEY_FILE")
	e.githubTokenWebhook = testing.Getenv(t, "WEBHOOK_GITHUB_TOKEN")
	e.createSecretsUsingHelm = testing.Getenv(t, "CREATE_SECRETS_USING_HELM")
	e.repoToCommit = testing.Getenv(t, "TEST_COMMIT_REPO")
	e.testRepo = testing.Getenv(t, "TEST_REPO", "")
	e.testOrg = testing.Getenv(t, "TEST_ORG", "")
	e.testOrgRepo = testing.Getenv(t, "TEST_ORG_REPO", "")
	e.testEnterprise = testing.Getenv(t, "TEST_ENTERPRISE", "")
	e.testEphemeral = testing.Getenv(t, "TEST_EPHEMERAL", "")
	e.runnerServiceAccuontName = testing.Getenv(t, "TEST_RUNNER_SERVICE_ACCOUNT_NAME", "")
	e.runnerTerminationGracePeriodSeconds = testing.Getenv(t, "TEST_RUNNER_TERMINATION_GRACE_PERIOD_SECONDS", "30")
	e.runnerGracefulStopTimeout = testing.Getenv(t, "TEST_RUNNER_GRACEFUL_STOP_TIMEOUT", "15")
	e.runnerNamespace = testing.Getenv(t, "TEST_RUNNER_NAMESPACE", "default")
	e.logFormat = testing.Getenv(t, "ARC_E2E_LOG_FORMAT", "")
	e.remoteKubeconfig = testing.Getenv(t, "ARC_E2E_REMOTE_KUBECONFIG", "")
	e.admissionWebhooksTimeout = testing.Getenv(t, "ARC_E2E_ADMISSION_WEBHOOKS_TIMEOUT", "")
	e.imagePullSecretName = testing.Getenv(t, "ARC_E2E_IMAGE_PULL_SECRET_NAME", "")
	// This should be the default for Ubuntu 20.04 based runner images
	e.dindSidecarRepositoryAndTag = vars.dindSidecarImageRepo + ":" + vars.dindSidecarImageTag
	e.vars = vars

	if e.remoteKubeconfig != "" {
		e.imagePullPolicy = "Always"
	} else {
		e.imagePullPolicy = "IfNotPresent"
	}

	e.watchNamespace = testing.Getenv(t, "TEST_WATCH_NAMESPACE", "")

	if e.remoteKubeconfig == "" {
		images := []testing.ContainerImage{
			testing.Img(vars.dindSidecarImageRepo, vars.dindSidecarImageTag),
			testing.Img("quay.io/brancz/kube-rbac-proxy", "v0.10.0"),
			testing.Img("quay.io/jetstack/cert-manager-controller", certManagerVersion),
			testing.Img("quay.io/jetstack/cert-manager-cainjector", certManagerVersion),
			testing.Img("quay.io/jetstack/cert-manager-webhook", certManagerVersion),
			// Otherwise kubelet would fail to pull images from DockerHub due too rate limit:
			//   Warning  Failed     19s                kubelet            Failed to pull image "summerwind/actions-runner-controller:v0.25.2": rpc error: code = Unknown desc = failed to pull and unpack image "docker.io/summerwind/actions-runner-controller:v0.25.2": failed to copy: httpReadSeeker: failed open: unexpected status code https://registry-1.docker.io/v2/summerwind/actions-runner-controller/manifests/sha256:92faf7e9f7f09a6240cdb5eb82eaf448852bdddf2fb77d0a5669fd8e5062b97b: 429 Too Many Requests - Server message: toomanyrequests: You have reached your pull rate limit. You may increase the limit by authenticating and upgrading: https://www.docker.com/increase-rate-limit
			testing.Img(arcStableImageRepo, arcStableImageTag),
		}

		e.Kind = testing.StartKind(t, k8sMinorVer, testing.Preload(images...))
		e.Env.Kubeconfig = e.Kind.Kubeconfig()
	} else {
		e.Env.Kubeconfig = e.remoteKubeconfig

		// Kind automatically installs https://github.com/rancher/local-path-provisioner for PVs.
		// But assuming the remote cluster isn't a kind Kubernetes cluster,
		// we need to install any provisioner manually.
		// Here, we install the local-path-provisioner on the remote cluster too,
		// so that we won't suffer from E2E failures due to the provisioner difference.
		e.KubectlApply(t, "https://raw.githubusercontent.com/rancher/local-path-provisioner/v0.0.22/deploy/local-path-storage.yaml", testing.KubectlConfig{})
	}

	e.scaleDownDelaySecondsAfterScaleOut, _ = strconv.ParseInt(testing.Getenv(t, "TEST_RUNNER_SCALE_DOWN_DELAY_SECONDS_AFTER_SCALE_OUT", "10"), 10, 32)
	e.minReplicas, _ = strconv.ParseInt(testing.Getenv(t, "TEST_RUNNER_MIN_REPLICAS", "1"), 10, 32)

	var err error
	e.dockerdWithinRunnerContainer, err = strconv.ParseBool(testing.Getenv(t, "TEST_RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER", "false"))
	if err != nil {
		panic(fmt.Sprintf("unable to parse bool from TEST_RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER: %v", err))
	}

	e.rootlessDocker, err = strconv.ParseBool(testing.Getenv(t, "TEST_RUNNER_ROOTLESS_DOCKER", "false"))
	if err != nil {
		panic(fmt.Sprintf("unable to parse bool from TEST_RUNNER_ROOTLESS_DOCKER: %v", err))
	}

	e.containerMode = testing.Getenv(t, "TEST_CONTAINER_MODE", "")
	if err != nil {
		panic(fmt.Sprintf("unable to parse bool from TEST_CONTAINER_MODE: %v", err))
	}

	if err := e.checkGitHubToken(t, e.githubToken); err != nil {
		t.Fatal(err)
	}

	if err := e.checkGitHubToken(t, e.githubTokenWebhook); err != nil {
		t.Fatal(err)
	}

	return e
}

func (e *env) checkGitHubToken(t *testing.T, tok string) error {
	t.Helper()

	ctx := context.Background()

	transport := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok})).Transport
	c := github.NewClient(&http.Client{Transport: transport})
	aa, res, err := c.Octocat(context.Background(), "hello")
	if err != nil {
		b, ioerr := io.ReadAll(res.Body)
		if ioerr != nil {
			t.Logf("%v", ioerr)
			return err
		}
		t.Logf(string(b))
		return err
	}

	t.Logf("%s", aa)

	if e.testEnterprise != "" {
		if _, res, err := c.Enterprise.CreateRegistrationToken(ctx, e.testEnterprise); err != nil {
			b, ioerr := io.ReadAll(res.Body)
			if ioerr != nil {
				t.Logf("%v", ioerr)
				return err
			}
			t.Logf(string(b))
			return err
		}
	}

	if e.testOrg != "" {
		if _, res, err := c.Actions.CreateOrganizationRegistrationToken(ctx, e.testOrg); err != nil {
			b, ioerr := io.ReadAll(res.Body)
			if ioerr != nil {
				t.Logf("%v", ioerr)
				return err
			}
			t.Logf(string(b))
			return err
		}
	}

	if e.testRepo != "" {
		s := strings.Split(e.testRepo, "/")
		owner, repo := s[0], s[1]
		if _, res, err := c.Actions.CreateRegistrationToken(ctx, owner, repo); err != nil {
			b, ioerr := io.ReadAll(res.Body)
			if ioerr != nil {
				t.Logf("%v", ioerr)
				return err
			}
			t.Logf(string(b))
			return err
		}
	}

	return nil
}

func (e *env) buildAndLoadImages(t *testing.T) {
	t.Helper()

	e.DockerBuild(t, e.vars.builds)

	if e.remoteKubeconfig == "" {
		e.KindLoadImages(t, e.vars.prebuildImages)
	} else {
		// If it fails with `no basic auth credentials` here, you might have missed logging into the container registry beforehand.
		// For ECR, run something like:
		//   aws ecr get-login-password | docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_DEFAULT_REGION}.amazonaws.com
		// Also note that the authenticated session can be expired in a day or so(probably depends on your AWS config),
		// so you might better write a script to do docker login before running the E2E test.
		e.DockerPush(t, e.vars.prebuildImages)
	}
}

func (e *env) KindLoadImages(t *testing.T, prebuildImages []testing.ContainerImage) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	if err := e.Kind.LoadImages(ctx, prebuildImages); err != nil {
		t.Fatal(err)
	}
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

type InstallARCConfig struct {
	GithubWebhookServerEnvName, GithubWebhookServerEnvValue string
}

type InstallARCOption func(*InstallARCConfig)

func (e *env) installActionsRunnerController(t *testing.T, repo, tag, testID, chart, chartVer string, opts ...InstallARCOption) {
	t.Helper()

	var c InstallARCConfig
	for _, opt := range opts {
		opt(&c)
	}

	e.createControllerNamespaceAndServiceAccount(t)

	scriptEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
		"ACCEPTANCE_TEST_DEPLOYMENT_TOOL=" + "helm",
		"CHART=" + chart,
		"CHART_VERSION=" + chartVer,
	}

	varEnv := []string{
		"WEBHOOK_GITHUB_TOKEN=" + e.githubTokenWebhook,
		"CREATE_SECRETS_USING_HELM=" + e.createSecretsUsingHelm,
		"TEST_ID=" + testID,
		"NAME=" + repo,
		"VERSION=" + tag,
		"ADMISSION_WEBHOOKS_TIMEOUT=" + e.admissionWebhooksTimeout,
		"IMAGE_PULL_SECRET=" + e.imagePullSecretName,
		"IMAGE_PULL_POLICY=" + e.imagePullPolicy,
		"DIND_SIDECAR_REPOSITORY_AND_TAG=" + e.dindSidecarRepositoryAndTag,
		"WATCH_NAMESPACE=" + e.watchNamespace,
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

	if e.logFormat != "" {
		varEnv = append(varEnv,
			"LOG_FORMAT="+e.logFormat,
		)
	}

	varEnv = append(varEnv,
		"GITHUB_WEBHOOK_SERVER_ENV_NAME="+c.GithubWebhookServerEnvName,
		"GITHUB_WEBHOOK_SERVER_ENV_VALUE="+c.GithubWebhookServerEnvValue,
	)

	scriptEnv = append(scriptEnv, varEnv...)
	scriptEnv = append(scriptEnv, e.vars.commonScriptEnv...)

	e.RunScript(t, "../../acceptance/deploy.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv})
}

func (e *env) deploy(t *testing.T, kind DeployKind, testID string, env ...string) {
	t.Helper()
	e.do(t, "apply", kind, testID, env...)
}

func (e *env) undeploy(t *testing.T, kind DeployKind, testID string) {
	t.Helper()
	e.do(t, "delete", kind, testID)
}

func (e *env) setDeletionTimestampsOnRunningPods(t *testing.T, deployKind DeployKind) {
	t.Helper()

	var scope, kind, labelKind string
	if e.testOrg != "" {
		scope = "org"
	} else if e.testEnterprise != "" {
		scope = "enterprise"
	} else {
		scope = "repo"
	}

	if deployKind == RunnerDeployments {
		kind = "runnerdeploy"
		labelKind = "runner-deployment"
	} else {
		kind = "runnerset"
		labelKind = "runnerset"
	}

	label := fmt.Sprintf("%s-name=%s-%s", labelKind, scope, kind)

	ctx := context.Background()
	c := e.getKubectlConfig()

	t.Logf("Finding pods with label %s", label)

	pods, err := e.Kubectl.FindPods(ctx, label, c)
	require.NoError(t, err)

	if len(pods) == 0 {
		return
	}

	t.Logf("Setting deletionTimestamps on pods %s", strings.Join(pods, ", "))

	err = e.Kubectl.DeletePods(ctx, pods, c)
	require.NoError(t, err)

	t.Logf("Deleted pods %s", strings.Join(pods, ", "))
}

func (e *env) do(t *testing.T, op string, kind DeployKind, testID string, env ...string) {
	t.Helper()

	e.createControllerNamespaceAndServiceAccount(t)

	scriptEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
		"OP=" + op,
		"RUNNER_NAMESPACE=" + e.runnerNamespace,
		"RUNNER_SERVICE_ACCOUNT_NAME=" + e.runnerServiceAccuontName,
		"RUNNER_GRACEFUL_STOP_TIMEOUT=" + e.runnerGracefulStopTimeout,
		"RUNNER_TERMINATION_GRACE_PERIOD_SECONDS=" + e.runnerTerminationGracePeriodSeconds,
	}
	scriptEnv = append(scriptEnv, env...)

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
		"RUNNER_CONTAINER_MODE=" + e.containerMode,
	}

	if e.dockerdWithinRunnerContainer && e.containerMode == "kubernetes" {
		t.Fatalf("TEST_RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER cannot be set along with TEST_CONTAINER_MODE=kubernetes")
		t.FailNow()
	}

	if e.dockerdWithinRunnerContainer {
		varEnv = append(varEnv,
			"RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER=true",
		)
		if e.rootlessDocker {
			varEnv = append(varEnv,
				"RUNNER_NAME="+e.vars.runnerRootlessDindImageRepo,
			)
		} else {
			varEnv = append(varEnv,
				"RUNNER_NAME="+e.vars.runnerDindImageRepo,
			)
		}
	} else {
		varEnv = append(varEnv,
			"RUNNER_DOCKERD_WITHIN_RUNNER_CONTAINER=false",
			"RUNNER_NAME="+e.vars.runnerImageRepo,
		)
	}

	scriptEnv = append(scriptEnv, varEnv...)
	scriptEnv = append(scriptEnv, e.vars.commonScriptEnv...)

	e.RunScript(t, "../../acceptance/deploy_runners.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv})
}

func (e *env) installArgoTunnel(t *testing.T) {
	e.doArgoTunnel(t, "apply")
}

func (e *env) uninstallArgoTunnel(t *testing.T) {
	e.doArgoTunnel(t, "delete")
}

func (e *env) doArgoTunnel(t *testing.T, op string) {
	t.Helper()

	scriptEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
		"OP=" + op,
		"TUNNEL_ID=" + os.Getenv("TUNNEL_ID"),
		"TUNNE_NAME=" + os.Getenv("TUNNEL_NAME"),
		"TUNNEL_HOSTNAME=" + os.Getenv("TUNNEL_HOSTNAME"),
	}

	e.RunScript(t, "../../acceptance/argotunnel.sh", testing.ScriptConfig{Dir: "../..", Env: scriptEnv})
}

func (e *env) runnerLabel(testID string) string {
	return "test-" + testID
}

func (e *env) createControllerNamespaceAndServiceAccount(t *testing.T) {
	t.Helper()

	e.KubectlEnsureNS(t, "actions-runner-system", testing.KubectlConfig{})
	e.KubectlEnsureClusterRoleBindingServiceAccount(t, "default-admin", "cluster-admin", "default:default", testing.KubectlConfig{})
}

func (e *env) installActionsWorkflow(t *testing.T, kind DeployKind, testID string) {
	t.Helper()

	installActionsWorkflow(t, e.testName+" "+testID, e.runnerLabel(testID), testResultCMNamePrefix, e.repoToCommit, kind, e.testJobs(testID), !e.rootlessDocker, e.doDockerBuild)
}

func (e *env) testJobs(testID string) []job {
	return createTestJobs(testID, testResultCMNamePrefix, 6)
}

func (e *env) verifyActionsWorkflowRun(t *testing.T, ctx context.Context, testID string) {
	t.Helper()

	verifyActionsWorkflowRun(t, ctx, e.Env, e.testJobs(testID), e.verifyTimeout(), e.getKubectlConfig())
}

func (e *env) verifyTimeout() time.Duration {
	if e.VerifyTimeout > 0 {
		return e.VerifyTimeout
	}

	return 8 * 60 * time.Second
}

func (e *env) getKubectlConfig() testing.KubectlConfig {
	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cmCfg := testing.KubectlConfig{
		Env: kubectlEnv,
	}

	return cmCfg
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

// useSudo also implies rootful docker and the use of buildx cache export/import
func installActionsWorkflow(t *testing.T, testName, runnerLabel, testResultCMNamePrefix, testRepo string, kind DeployKind, testJobs []job, useSudo, doDockerBuild bool) {
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

		var sudo string
		if useSudo {
			sudo = "sudo "
		}

		if !kubernetesContainerMode {
			if kind == RunnerDeployments {
				steps = append(steps,
					testing.Step{
						Run: sudo + "mkdir -p \"${RUNNER_TOOL_CACHE}\" \"${HOME}/.cache\"",
					},
				)

				if useSudo {
					steps = append(steps,
						testing.Step{
							// This might be the easiest way to handle permissions without use of securityContext
							// https://stackoverflow.com/questions/50156124/kubernetes-nfs-persistent-volumes-permission-denied#comment107483717_53186320
							Run: sudo + "mkdir -p \"/var/lib/docker\"",
						},
					)
				}
			}

			if useSudo {
				steps = append(steps,
					testing.Step{
						// This might be the easiest way to handle permissions without use of securityContext
						// https://stackoverflow.com/questions/50156124/kubernetes-nfs-persistent-volumes-permission-denied#comment107483717_53186320
						Run: sudo + "chmod 777 -R \"${RUNNER_TOOL_CACHE}\" \"${HOME}/.cache\"",
					},
					testing.Step{
						Run: sudo + "chmod 777 -R \"/var/lib/docker\"",
					},
					testing.Step{
						// This might be the easiest way to handle permissions without use of securityContext
						// https://stackoverflow.com/questions/50156124/kubernetes-nfs-persistent-volumes-permission-denied#comment107483717_53186320
						Run: "ls -lah \"${RUNNER_TOOL_CACHE}\" \"${HOME}/.cache\"",
					},
					testing.Step{
						// This might be the easiest way to handle permissions without use of securityContext
						// https://stackoverflow.com/questions/50156124/kubernetes-nfs-persistent-volumes-permission-denied#comment107483717_53186320
						Run: "ls -lah \"/var/lib/docker\" || echo ls failed.",
					},
				)
			}

			steps = append(steps,
				testing.Step{
					Uses: "actions/setup-go@v3",
					With: &testing.With{
						GoVersion: "1.22.1",
					},
				},
			)

			// Ensure both the alias and the full command work after
			// https://github.com/actions/actions-runner-controller/pull/2326
			steps = append(steps,
				testing.Step{
					Run: "docker-compose version",
				},
				testing.Step{
					Run: "docker compose version",
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

		if doDockerBuild {
			if !kubernetesContainerMode {
				setupBuildXActionWith := &testing.With{
					BuildkitdFlags: "--debug",
					// As the consequence of setting `install: false`, it doesn't install buildx as an alias to `docker build`
					// so we need to use `docker buildx build` in the next step
					Install: false,
				}
				var dockerBuildCache, dockerfile string
				if useSudo {
					// This needs to be set only when rootful docker mode.
					// When rootless, we need to use the `docker` buildx driver, which doesn't support cache export
					// so we end up with the below error on docker-build:
					//   error: cache export feature is currently not supported for docker driver. Please switch to a different driver (eg. "docker buildx create --use")
					// See https://docs.docker.com/engine/reference/commandline/buildx_create/#docker-container-driver
					// for the `docker-container` driver.
					dockerBuildCache = "--cache-from=type=local,src=/home/runner/.cache/buildx " +
						"--cache-to=type=local,dest=/home/runner/.cache/buildx-new,mode=max "
					dockerfile = "Dockerfile"
					// Note though, if the cache does not exist yet, the buildx build seem to write cache data to /home/runner/.cache/buildx,
					// not buildx-new.
					// I think the following message emitted by buildx in the end is relevant to this behaviour, but not 100% sure:
					//   WARNING: local cache import at /home/runner/.cache/buildx not found due to err: could not read /home/runner/.cache/buildx/index.json: open /home/runner/.cache/buildx/index.json: no such file or directory
				} else {
					// See https://docs.docker.com/engine/reference/commandline/buildx_create/#docker-driver
					// for the `docker` driver.
					setupBuildXActionWith.Driver = "docker"
					dockerfile = "Dockerfile.nocache"
				}

				useCustomDockerContext := os.Getenv("ARC_E2E_USE_CUSTOM_DOCKER_CONTEXT") != ""
				if useCustomDockerContext {
					setupBuildXActionWith.Endpoint = "mycontext"

					steps = append(steps, testing.Step{
						// https://github.com/docker/buildx/issues/413#issuecomment-710660155
						// To prevent setup-buildx-action from failing with:
						//   error: could not create a builder instance with TLS data loaded from environment. Please use `docker context create <context-name>` to create a context for current environment and then create a builder instance with `docker buildx create <context-name>`
						Run: "docker context create mycontext",
					},
						testing.Step{
							Run: "docker context use mycontext",
						},
					)
				}

				steps = append(steps,
					testing.Step{
						Name: "Set up Docker Buildx",
						Uses: "docker/setup-buildx-action@v1",
						With: setupBuildXActionWith,
					},
					testing.Step{
						Run: "docker buildx build --platform=linux/amd64 -t test1 --load " +
							dockerBuildCache +
							fmt.Sprintf("-f %s .", dockerfile),
					},
					testing.Step{
						Run: "docker run --rm test1",
					},
					testing.Step{
						Uses: "addnab/docker-run-action@v3",
						With: &testing.With{
							Image: "test1",
							Run:   "hello",
							Shell: "sh",
						},
					},
				)

				if useSudo {
					steps = append(steps,
						testing.Step{
							// https://github.com/docker/build-push-action/blob/master/docs/advanced/cache.md#local-cache
							// See https://github.com/moby/buildkit/issues/1896 for why this is needed
							Run: "if -d /home/runner/.cache/buildx-new; then " + sudo + "rm -rf /home/runner/.cache/buildx && " + sudo + `mv /home/runner/.cache/buildx-new /home/runner/.cache/buildx; else echo "/home/runner/.cache/buildx-new is not found. Perhaps you're running this on a stateleess runner?"; fi`,
						},
						testing.Step{
							Run: "ls -lah /home/runner/.cache/*",
						},
					)
				}
			}

			if useSudo {
				if kind == RunnerDeployments {
					steps = append(steps,
						testing.Step{
							// https://github.com/docker/build-push-action/blob/master/docs/advanced/cache.md#local-cache
							// See https://github.com/moby/buildkit/issues/1896 for why this is needed
							Run: sudo + "rm -rf /home/runner/.cache/buildx && mv /home/runner/.cache/buildx-new /home/runner/.cache/buildx",
						},
						testing.Step{
							Run: sudo + "ls -lah /home/runner/.cache/*",
						},
					)
				}
			}
		}

		steps = append(steps,
			testing.Step{
				Uses: "azure/setup-kubectl@v1",
				With: &testing.With{
					Version: "v1.22.1",
				},
			},
			testing.Step{
				Run: fmt.Sprintf("./test.sh %s %s", t.Name(), j.testArg),
			},
		)

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

func verifyActionsWorkflowRun(t *testing.T, ctx context.Context, env *testing.Env, testJobs []job, timeout time.Duration, cmCfg testing.KubectlConfig) {
	t.Helper()

	var expected []string

	for range testJobs {
		expected = append(expected, "ok")
	}

	gomega.NewGomegaWithT(t).Eventually(ctx, func() ([]string, error) {
		var results []string

		var errs []error

		for i := range testJobs {
			testResultCMName := testJobs[i].configMapName

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
	}, timeout, 30*time.Second).Should(gomega.Equal(expected))
}
