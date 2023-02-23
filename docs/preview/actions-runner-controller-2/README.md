# Autoscaling Runner Scale Sets mode

**⚠️ This mode is currently only available for a limited number of organizations.**

This new autoscaling mode brings numerous enhancements (described in the following sections) that will make your experience more reliable and secure.

## How it works

![arc_hld_v1 drawio (1)](https://user-images.githubusercontent.com/568794/212665433-2d1f3d6e-0ba8-4f02-9d1b-27d00c49abd1.png)

In addition to the increased reliability of the automatic scaling, we have worked on these improvements:

- No longer require cert-manager as a prerequisite for installing actions-runner-controller
- Reliable scale-up based on job demands and scale-down to zero runner pods
- Reduce API requests to `api.github.com`, no more API rate-limiting problems
- The GitHub Personal Access Token (PAT) or the GitHub App installation token is no longer passed to the runner pod for runner registration
- Maximum flexibility for customizing your runner pod template

### Demo

https://user-images.githubusercontent.com/568794/212668313-8946ddc5-60c1-461f-a73e-27f5e8c75720.mp4

## Setup

### Prerequisites

1. Create a K8s cluster, if not available.
    - If you don't have a K8s cluster, you can install a local environment using minikube. See [installing minikube](https://minikube.sigs.k8s.io/docs/start/).
1. Install helm 3, if not available. See [installing Helm](https://helm.sh/docs/intro/install/).

### Install actions-runner-controller

1. Install actions-runner-controller using helm 3. For additional configuration options, see [values.yaml](https://github.com/actions/actions-runner-controller/blob/master/charts/actions-runner-controller-2/values.yaml)

    ```bash
    NAMESPACE="arc-systems"
    helm install arc \
        --namespace "${NAMESPACE}" \
        --create-namespace \
        oci://ghcr.io/actions/actions-runner-controller-charts/actions-runner-controller-2 \
        --version 0.2.0
    ```

1. Generate a Personal Access Token (PAT) or create and install a GitHub App. See [Creating a personal access token](https://docs.github.com/en/github/authenticating-to-github/creating-a-personal-access-token) and [Creating a GitHub App](https://docs.github.com/en/developers/apps/creating-a-github-app).
    - ℹ For the list of required permissions, see [Authenticating to the GitHub API](https://github.com/actions/actions-runner-controller/blob/master/docs/authenticating-to-the-github-api.md#authenticating-to-the-github-api).

1. You're ready to install the autoscaling runner set. For additional configuration options, see [values.yaml](https://github.com/actions/actions-runner-controller/blob/master/charts/auto-scaling-runner-set/values.yaml)
    - ℹ **Choose your installation name carefully**, you will use it as the value of `runs-on` in your workflow.
    - ℹ **We recommend you choose a unique namespace in the following steps**. As a good security measure, it's best to have your runner pods created in a different namespace than the one containing the manager and listener pods.

    ```bash
    # Using a Personal Access Token (PAT)
    INSTALLATION_NAME="arc-runner-set" 
    NAMESPACE="arc-runners"
    GITHUB_CONFIG_URL="https://github.com/<your_enterprise/org/repo>"
    GITHUB_PAT="<PAT>"
    helm install "${INSTALLATION_NAME}" \
        --namespace "${NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="${GITHUB_CONFIG_URL}" \
        --set githubConfigSecret.github_token="${GITHUB_PAT}" \
        oci://ghcr.io/actions/actions-runner-controller-charts/auto-scaling-runner-set --version 0.2.0
    ```

    ```bash
    # Using a GitHub App
    INSTALLATION_NAME="arc-runner-set" 
    NAMESPACE="arc-runners"
    GITHUB_CONFIG_URL="https://github.com/<your_enterprise/org/repo>" 
    GITHUB_APP_ID="<GITHUB_APP_ID>"
    GITHUB_APP_INSTALLATION_ID="<GITHUB_APP_INSTALLATION_ID>"
    GITHUB_APP_PRIVATE_KEY="<GITHUB_APP_PRIVATE_KEY>"
    helm install arc-runner-set \
        --namespace "${NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="${GITHUB_CONFIG_URL}" \
        --set githubConfigSecret.github_app_id="${GITHUB_APP_ID}" \
        --set githubConfigSecret.github_app_installation_id="${GITHUB_APP_INSTALLATION_ID}" \
        --set githubConfigSecret.github_app_private_key="${GITHUB_APP_PRIVATE_KEY}" \
        oci://ghcr.io/actions/actions-runner-controller-charts/auto-scaling-runner-set --version 0.2.0
    ```

1. Check your installation. If everything went well, you should see the following:

    ```bash
    $ helm list -n "${NAMESPACE}"

    NAME            NAMESPACE       REVISION        UPDATED                                 STATUS          CHART                                    APP VERSION
    arc             arc-systems     1               2023-01-18 10:03:36.610534934 +0000 UTC deployed        actions-runner-controller-2-0.2.0        preview    
    arc-runner-set  arc-systems     1               2023-01-18 10:20:14.795285645 +0000 UTC deployed        auto-scaling-runner-set-0.2.0            0.2.0 
    ```

    ```bash
    $ kubectl get pods -n "${NAMESPACE}"

    NAME                                              READY   STATUS    RESTARTS   AGE
    arc-actions-runner-controller-2-8c74b6f95-gr7zr   1/1     Running   0          20m
    arc-runner-set-6cd58d58-listener                  1/1     Running   0          21s
    ```

1. In a repository, create a simple test workflow as follows. The `runs-on` value should match the helm installation name you used in the previous step.

    ```yaml
    name: Test workflow
    on:
        workflow_dispatch:

    jobs:
    test:
        runs-on: arc-runner-set
        steps:
        - name: Hello world
            run: echo "Hello world"
    ```

1. Run the workflow. You should see the runner pod being created and the workflow being executed.

    ```bash
    $ kubectl get pods -A

    NAMESPACE     NAME                                              READY   STATUS    RESTARTS      AGE
    arc-systems   arc-actions-runner-controller-2-8c74b6f95-gr7zr   1/1     Running   0             27m
    arc-systems   arc-runner-set-6cd58d58-listener                  1/1     Running   0             7m52s
    arc-runners   arc-runner-set-rmrgw-runner-p9p5n                 1/1     Running   0             21s
    ```

## Troubleshooting

### Check the logs

You can check the logs of the controller pod using the following command:

```bash
# Controller logs
$ kubectl logs -n "${NAMESPACE}" -l app.kubernetes.io/name=actions-runner-controller-2

# Runner set listener logs
kubectl logs -n "${NAMESPACE}" -l runner-scale-set-listener=arc-systems-arc-runner-set
```

### If you installed the autoscaling runner set, but the listener pod is not created

Verify that the secret you provided is correct and that the `githubConfigUrl` you provided is accurate.

## Changelog

### v0.2.0

#### Major changes

1. Added proxy support for the controller and the runner pods, see the new helm chart fields [#2286](https://github.com/actions/actions-runner-controller/pull/2286)
1. Added the abiilty to provide a pre-defined kubernetes secret for the auto scaling runner set helm chart [#2234](https://github.com/actions/actions-runner-controller/pull/2234)
1. Enhanced security posture by removing un-required permissions for the manager-role [#2260](https://github.com/actions/actions-runner-controller/pull/2260)
1. Enhanced our logging by returning an error when a runner group is defined in the values file but it's not created in GitHub [#2215](https://github.com/actions/actions-runner-controller/pull/2215)
1. Fixed helm charts issues that were preventing the use of DinD [#2291](https://github.com/actions/actions-runner-controller/pull/2291)
1. Fixed a bug that was preventing runner scale from being removed from the backend when they were deleted from the cluster [#2255](https://github.com/actions/actions-runner-controller/pull/2255) [#2223](https://github.com/actions/actions-runner-controller/pull/2223)
1. Fixed bugs with the helm chart definitions preventing certain values from being set [#2222](https://github.com/actions/actions-runner-controller/pull/2222)
1. Fixed a bug that prevented the configuration of a runner group for a runner scale set [#2216](https://github.com/actions/actions-runner-controller/pull/2216)

#### Log

- [1c7b7f4](https://github.com/actions/actions-runner-controller/commit/1c7b7f4) Bump arc-2 chart version and prepare 0.2.0 release [#2313](https://github.com/actions/actions-runner-controller/pull/2313)
- [73e22a1](https://github.com/actions/actions-runner-controller/commit/73e22a1) Disable metrics serving in proxy tests [#2307](https://github.com/actions/actions-runner-controller/pull/2307)
- [9b44f00](https://github.com/actions/actions-runner-controller/commit/9b44f00) Documentation corrections [#2116](https://github.com/actions/actions-runner-controller/pull/2116)
- [6b4250c](https://github.com/actions/actions-runner-controller/commit/6b4250c) Add support for proxy [#2286](https://github.com/actions/actions-runner-controller/pull/2286)
- [ced8822](https://github.com/actions/actions-runner-controller/commit/ced8822) Resolves the erroneous webhook scale down due to check runs [#2119](https://github.com/actions/actions-runner-controller/pull/2119)
- [44c06c2](https://github.com/actions/actions-runner-controller/commit/44c06c2) fix: case-insensitive webhook label matching [#2302](https://github.com/actions/actions-runner-controller/pull/2302)
- [4103fe3](https://github.com/actions/actions-runner-controller/commit/4103fe3) Use DOCKER_IMAGE_NAME instead of NAME to avoid conflict. [#2303](https://github.com/actions/actions-runner-controller/pull/2303)
- [a44fe04](https://github.com/actions/actions-runner-controller/commit/a44fe04) Fix manager crashloopback for ARC deployments without scaleset-related controllers [#2293](https://github.com/actions/actions-runner-controller/pull/2293)
- [274d0c8](https://github.com/actions/actions-runner-controller/commit/274d0c8) Added ability to configure log level from chart values [#2252](https://github.com/actions/actions-runner-controller/pull/2252)
- [256e08e](https://github.com/actions/actions-runner-controller/commit/256e08e) Ask runner to wait for docker daemon from DinD. [#2292](https://github.com/actions/actions-runner-controller/pull/2292)
- [f677fd5](https://github.com/actions/actions-runner-controller/commit/f677fd5) doc: Fix chart name for helm commands in docs [#2287](https://github.com/actions/actions-runner-controller/pull/2287)
- [d962714](https://github.com/actions/actions-runner-controller/commit/d962714) Fix helm chart when containerMode.type=dind. [#2291](https://github.com/actions/actions-runner-controller/pull/2291)
- [3886f28](https://github.com/actions/actions-runner-controller/commit/3886f28) Add EKS test environment Terraform templates [#2290](https://github.com/actions/actions-runner-controller/pull/2290)
- [dab9004](https://github.com/actions/actions-runner-controller/commit/dab9004) Added workflow to be triggered via rest api dispatch in e2e test [#2283](https://github.com/actions/actions-runner-controller/pull/2283)
- [dd8ec1a](https://github.com/actions/actions-runner-controller/commit/dd8ec1a) Add testserver package [#2281](https://github.com/actions/actions-runner-controller/pull/2281)
- [8e52a6d](https://github.com/actions/actions-runner-controller/commit/8e52a6d) EphemeralRunner: On cleanup, if pod is pending, delete from service [#2255](https://github.com/actions/actions-runner-controller/pull/2255)
- [9990243](https://github.com/actions/actions-runner-controller/commit/9990243) Early return if finalizer does not exist to make it more readable [#2262](https://github.com/actions/actions-runner-controller/pull/2262)
- [0891981](https://github.com/actions/actions-runner-controller/commit/0891981) Port ADRs from internal repo [#2267](https://github.com/actions/actions-runner-controller/pull/2267)
- [facae69](https://github.com/actions/actions-runner-controller/commit/facae69) Remove un-required permissions for the manager-role of the new `AutoScalingRunnerSet` [#2260](https://github.com/actions/actions-runner-controller/pull/2260)
- [8f62e35](https://github.com/actions/actions-runner-controller/commit/8f62e35) Add options to multi client [#2257](https://github.com/actions/actions-runner-controller/pull/2257)
- [55951c2](https://github.com/actions/actions-runner-controller/commit/55951c2) Add new workflow to automate runner updates [#2247](https://github.com/actions/actions-runner-controller/pull/2247)
- [c4297d2](https://github.com/actions/actions-runner-controller/commit/c4297d2) Avoid deleting scale set if annotation is not parsable or if it does not exist [#2239](https://github.com/actions/actions-runner-controller/pull/2239)
- [0774f06](https://github.com/actions/actions-runner-controller/commit/0774f06) ADR: automate runner updates [#2244](https://github.com/actions/actions-runner-controller/pull/2244)
- [92ab11b](https://github.com/actions/actions-runner-controller/commit/92ab11b) Use UUID v5 for client identifiers [#2241](https://github.com/actions/actions-runner-controller/pull/2241)
- [7414dc6](https://github.com/actions/actions-runner-controller/commit/7414dc6) Add Identifier to actions.Client [#2237](https://github.com/actions/actions-runner-controller/pull/2237)
- [34efb9d](https://github.com/actions/actions-runner-controller/commit/34efb9d) Add documentation to update ARC with prometheus CRDs needed by actions metrics server [#2209](https://github.com/actions/actions-runner-controller/pull/2209)
- [fbad561](https://github.com/actions/actions-runner-controller/commit/fbad561) Allow provide pre-defined kubernetes secret when helm-install AutoScalingRunnerSet [#2234](https://github.com/actions/actions-runner-controller/pull/2234)
- [a5cef7e](https://github.com/actions/actions-runner-controller/commit/a5cef7e) Resolve CI break due to bad merge. [#2236](https://github.com/actions/actions-runner-controller/pull/2236)
- [1f4fe46](https://github.com/actions/actions-runner-controller/commit/1f4fe46) Delete RunnerScaleSet on service when AutoScalingRunnerSet is deleted. [#2223](https://github.com/actions/actions-runner-controller/pull/2223)
- [067686c](https://github.com/actions/actions-runner-controller/commit/067686c) Fix typos and markdown structure in troubleshooting guide [#2148](https://github.com/actions/actions-runner-controller/pull/2148)
- [df12e00](https://github.com/actions/actions-runner-controller/commit/df12e00) Remove network requests from actions.NewClient [#2219](https://github.com/actions/actions-runner-controller/pull/2219)
- [cc26593](https://github.com/actions/actions-runner-controller/commit/cc26593) Skip CT when list-changed=false. [#2228](https://github.com/actions/actions-runner-controller/pull/2228)
- [835eac7](https://github.com/actions/actions-runner-controller/commit/835eac7) Fix helm charts when pass values file.  [#2222](https://github.com/actions/actions-runner-controller/pull/2222)
- [01e9dd3](https://github.com/actions/actions-runner-controller/commit/01e9dd3) Update Validate ARC workflow to go 1.19 [#2220](https://github.com/actions/actions-runner-controller/pull/2220)
- [8038181](https://github.com/actions/actions-runner-controller/commit/8038181) Allow update runner group for AutoScalingRunnerSet [#2216](https://github.com/actions/actions-runner-controller/pull/2216)
- [219ba5b](https://github.com/actions/actions-runner-controller/commit/219ba5b) chore(deps): bump sigs.k8s.io/controller-runtime from 0.13.1 to 0.14.1 [#2132](https://github.com/actions/actions-runner-controller/pull/2132)
- [b09e3a2](https://github.com/actions/actions-runner-controller/commit/b09e3a2) Return error for non-existing runner group. [#2215](https://github.com/actions/actions-runner-controller/pull/2215)
- [7ea60e4](https://github.com/actions/actions-runner-controller/commit/7ea60e4) Fix intermittent image push failures to GHCR [#2214](https://github.com/actions/actions-runner-controller/pull/2214)
- [c8918f5](https://github.com/actions/actions-runner-controller/commit/c8918f5) Fix URL for authenticating using a GitHub app [#2206](https://github.com/actions/actions-runner-controller/pull/2206)
- [d57d17f](https://github.com/actions/actions-runner-controller/commit/d57d17f) Add support for custom CA in actions.Client [#2199](https://github.com/actions/actions-runner-controller/pull/2199)
- [6e69c75](https://github.com/actions/actions-runner-controller/commit/6e69c75) chore(deps): bump github.com/hashicorp/go-retryablehttp from 0.7.1 to 0.7.2 [#2203](https://github.com/actions/actions-runner-controller/pull/2203)
- [882bfab](https://github.com/actions/actions-runner-controller/commit/882bfab) Renaming autoScaling to autoscaling in tests matching the convention [#2201](https://github.com/actions/actions-runner-controller/pull/2201)
- [3327f62](https://github.com/actions/actions-runner-controller/commit/3327f62) Refactor actions.Client with options to help extensibility [#2193](https://github.com/actions/actions-runner-controller/pull/2193)
- [282f2dd](https://github.com/actions/actions-runner-controller/commit/282f2dd) chore(deps): bump github.com/onsi/gomega from 1.20.2 to 1.25.0 [#2169](https://github.com/actions/actions-runner-controller/pull/2169)
- [d67f808](https://github.com/actions/actions-runner-controller/commit/d67f808) Include nikola-jokic in CODEOWNERS file [#2184](https://github.com/actions/actions-runner-controller/pull/2184)
- [4932412](https://github.com/actions/actions-runner-controller/commit/4932412) Fix L0 test to make it more reliable. [#2178](https://github.com/actions/actions-runner-controller/pull/2178)
- [6da1cde](https://github.com/actions/actions-runner-controller/commit/6da1cde) Update runner version to 2.301.1 [#2182](https://github.com/actions/actions-runner-controller/pull/2182)
- [f9bae70](https://github.com/actions/actions-runner-controller/commit/f9bae70) Add distinct namespace best practice note [#2181](https://github.com/actions/actions-runner-controller/pull/2181)
- [05a3908](https://github.com/actions/actions-runner-controller/commit/05a3908) Add arc-2 quickstart guide [#2180](https://github.com/actions/actions-runner-controller/pull/2180)
- [606ed1b](https://github.com/actions/actions-runner-controller/commit/606ed1b) Add Repository information to Runner Status [#2093](https://github.com/actions/actions-runner-controller/pull/2093)
