## Contributing

### Testing Controller Built from a Pull Request

We always appreciate your help in testing open pull requests by deploying custom builds of actions-runner-controller onto your own environment, so that we are extra sure we didn't break anything.

It is especially true when the pull request is about GitHub Enterprise, both GHEC and GHES, as [maintainers don't have GitHub Enterprise environments for testing](/README.md#github-enterprise-support).

The process would look like the below:

- Clone this repository locally
- Checkout the branch. If you use the `gh` command, run `gh pr checkout $PR_NUMBER`
- Run `NAME=$DOCKER_USER/actions-runner-controller VERSION=canary make docker-build docker-push` for a custom container image build
- Update your actions-runner-controller's controller-manager deployment to use the new image, `$DOCKER_USER/actions-runner-controller:canary`

Please also note that you need to replace `$DOCKER_USER` with your own DockerHub account name.

### How to Contribute a Patch

Depending on what you are patching depends on how you should go about it. Below are some guides on how to test patches locally as well as develop the controller and runners.

When submitting a PR for a change please provide evidence that your change works as we still need to work on improving the CI of the project. Some resources are provided for helping achieve this, see this guide for details.

#### Running an End to End Test

> **Notes for Ubuntu 20.04+ users**
>
> If you're using Ubuntu 20.04 or greater, you might have installed `docker` with `snap`.
>
> If you want to stick with `snap`-provided `docker`, do not forget to set `TMPDIR` to
> somewhere under `$HOME`.
> Otherwise `kind load docker-image` fail while running `docker save`.
> See https://kind.sigs.k8s.io/docs/user/known-issues/#docker-installed-with-snap for more information.

To test your local changes against both PAT and App based authentication please run the `acceptance` make target with the authentication configuration details provided:

```shell
# This sets `VERSION` envvar to some appropriate value
. hack/make-env.sh

DOCKER_USER=*** \
  GITHUB_TOKEN=*** \
  APP_ID=*** \
  PRIVATE_KEY_FILE_PATH=path/to/pem/file \
  INSTALLATION_ID=*** \
  make acceptance
```

**Rerunning a failed test**

When one of tests run by `make acceptance` failed, you'd probably like to rerun only the failed one.

It can be done by `make acceptance/run` and by setting the combination of `ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm|kubectl` and `ACCEPTANCE_TEST_SECRET_TYPE=token|app` values that failed (note, you just need to set the corresponding authentication configuration in this circumstance)

In the example below, we rerun the test for the combination `ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm ACCEPTANCE_TEST_SECRET_TYPE=token` only:

```shell
DOCKER_USER=*** \
  GITHUB_TOKEN=*** \
  ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm
  ACCEPTANCE_TEST_SECRET_TYPE=token \
  make acceptance/run
```

**Testing in a non-kind cluster**

If you prefer to test in a non-kind cluster, you can instead run:

```shell
KUBECONFIG=path/to/kubeconfig \
  DOCKER_USER=*** \
  GITHUB_TOKEN=*** \
  APP_ID=*** \
  PRIVATE_KEY_FILE_PATH=path/to/pem/file \
  INSTALLATION_ID=*** \
  ACCEPTANCE_TEST_SECRET_TYPE=token \
  make docker-build acceptance/setup \
       acceptance/deploy \
       acceptance/tests
```

#### Developing the Controller

Rerunning the whole acceptance test suite from scratch on every little change to the controller, the runner, and the chart would be counter-productive.

To make your development cycle faster, use the below command to update deploy and update all the three:

```shell
# Let assume we have all other envvars like DOCKER_USER, GITHUB_TOKEN already set,
# The below command will (re)build `actions-runner-controller:controller1` and `actions-runner:runner1`,
# load those into kind nodes, and then rerun kubectl or helm to install/upgrade the controller,
# and finally upgrade the runner deployment to use the new runner image.
#
# As helm 3 and kubectl is unable to recreate a pod when no tag change,
# you either need to bump VERSION and RUNNER_TAG on each run,
# or manually run `kubectl delete pod $POD` on respective pods for changes to actually take effect.

# Makefile
VERSION=controller1 \
  RUNNER_TAG=runner1 \
  make acceptance/pull acceptance/kind docker-build acceptance/load acceptance/deploy
```

If you've already deployed actions-runner-controller and only want to recreate pods to use the newer image, you can run:

```shell
# Makefile
NAME=$DOCKER_USER/actions-runner-controller \
  make docker-build acceptance/load && \
  kubectl -n actions-runner-system delete po $(kubectl -n actions-runner-system get po -ojsonpath={.items[*].metadata.name})
```

Similarly, if you'd like to recreate runner pods with the newer runner image you can use the runner specific [Makefile](runner/Makefile) to build and / or push new runner images

```shell
# runner/Makefile
NAME=$DOCKER_USER/actions-runner make \
  -C runner docker-{build,push}-ubuntu && \
  (kubectl get po -ojsonpath={.items[*].metadata.name} | xargs -n1 kubectl delete po)
```

#### Developing the Runners

**Tests**

A set of example pipelines (./acceptance/pipelines) are provided in this repository which you can use to validate your runners are working as expected. When raising a PR please run the relevant suites to prove your change hasn't broken anything.

**Running Ginkgo Tests**

You can run the integration test suite that is written in Ginkgo with:

```shell
make test-with-deps
```

This will firstly install a few binaries required to setup the integration test environment and then runs `go test` to start the Ginkgo test.

If you don't want to use `make`, like when you're running tests from your IDE, install required binaries to `/usr/local/kubebuilder/bin`. That's the directory in which controller-runtime's `envtest` framework locates the binaries.

```shell
sudo mkdir -p /usr/local/kubebuilder/bin
make kube-apiserver etcd
sudo mv test-assets/{etcd,kube-apiserver} /usr/local/kubebuilder/bin/
go test -v -run TestAPIs github.com/actions-runner-controller/actions-runner-controller/controllers
```

To run Ginkgo tests selectively, set the pattern of target test names to `GINKGO_FOCUS`.
All the Ginkgo test that matches `GINKGO_FOCUS` will be run.

```shell
GINKGO_FOCUS='[It] should create a new Runner resource from the specified template, add a another Runner on replicas increased, and removes all the replicas when set to 0' \
  go test -v -run TestAPIs github.com/actions-runner-controller/actions-runner-controller/controllers
```

#### Helm Version Bumps

In general we ask you not to bump the version in your PR, the maintainers in general manage the publishing of a new chart.

#### Running linters locally

To run the `golangci-lint` tool locally, we recommend you use `make lint` to run the tool using a Docker container matching the CI version.