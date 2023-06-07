# Autoscaling Runner Scale Sets mode

This new autoscaling mode brings numerous enhancements (described in the following sections) that will make your experience more reliable and secure.

## How it works

![ARC architecture diagram](arc-diagram-light.png#gh-light-mode-only)
![ARC architecture diagram](arc-diagram-dark.png#gh-dark-mode-only)

1. ARC is installed using the supplied Helm charts, and the controller manager pod is deployed in the specified namespace. A new `AutoScalingRunnerSet` resource is deployed via the supplied Helm charts or a customized manifest file. The `AutoScalingRunnerSet` controller calls GitHub's APIs to fetch the runner group ID that the runner scale set will belong to.
2. The `AutoScalingRunnerSet` controller calls the APIs one more time to either fetch or create a runner scale set in the `Actions Service` before creating the `Runner ScaleSet Listener` resource.
3. A `Runner ScaleSet Listener` pod is deployed by the `AutoScaling Listener Controller`. In this pod, the listener application connects to the `Actions Service` to authenticate and establish a long poll HTTPS connection. The listener stays idle until it receives a `Job Available` message from the `Actions Service`.
4. When a workflow run is triggered from a repository, the `Actions Service` dispatches individual job runs to the runners or runner scalesets where the `runs-on` property matches the name of the runner scaleset or labels of self-hosted runners.
5. When the `Runner ScaleSet Listener` receives the `Job Available` message, it checks whether it can scale up to the desired count. If it can, the `Runner ScaleSet Listener` acknowledges the message.
6. The `Runner ScaleSet Listener` uses a `Service Account` and a `Role` bound to that account to make an HTTPS call through the Kubernetes APIs to patch the `EphemeralRunner Set` resource with the number of desired replicas count.
7. The `EphemeralRunner Set` attempts to create new runners and the `EphemeralRunner Controller` requests a JIT configuration token to register these runners. The controller attempts to create runner pods. If the pod's status is `failed`, the controller retries up to 5 times. After 24 hours the `Actions Service` unassigns the job if no runner accepts it.
8. Once the runner pod is created, the runner application in the pod uses the JIT configuration token to register itself with the `Actions Service`. It then establishes another HTTPS long poll connection to receive the job details it needs to execute.
9. The `Actions Service` acknowledges the runner registration and dispatches the job run details.
10. Throughout the job run execution, the runner continuously communicates the logs and job run status back to the `Actions Service`.
11. When the runner completes its job successfully, the `EphemeralRunner Controller` checks with the `Actions Service` to see if runner can be deleted. If it can, the `Ephemeral RunnerSet` deletes the runner.

In addition to the increased reliability of the automatic scaling, we have worked on these improvements:

- No longer require cert-manager as a prerequisite for installing actions-runner-controller
- Reliable scale-up based on job demands and scale-down to zero runner pods
- Reduce API requests to `api.github.com`, no more API rate-limiting problems
- The GitHub Personal Access Token (PAT) or the GitHub App installation token is no longer passed to the runner pod for runner registration
- Maximum flexibility for customizing your runner pod template

### Demo

[![Watch the walkthrough](thumbnail.png)](https://youtu.be/wQ0k5k6KW5Y)

> Will take you to Youtube for a short walkthrough of the Autoscaling Runner Scale Sets mode.

## Setup

### Prerequisites

1. Create a K8s cluster, if not available.
    - If you don't have a K8s cluster, you can install a local environment using minikube. See [installing minikube](https://minikube.sigs.k8s.io/docs/start/).
1. Install helm 3, if not available. See [installing Helm](https://helm.sh/docs/intro/install/).

### Install actions-runner-controller

1. Install actions-runner-controller using helm 3. For additional configuration options, see [values.yaml](https://github.com/actions/actions-runner-controller/blob/master/charts/gha-runner-scale-set-controller/values.yaml)

    ```bash
    NAMESPACE="arc-systems"
    helm install arc \
        --namespace "${NAMESPACE}" \
        --create-namespace \
        oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller
    ```

1. Generate a Personal Access Token (PAT) or create and install a GitHub App. See [Creating a personal access token](https://docs.github.com/en/github/authenticating-to-github/creating-a-personal-access-token) and [Creating a GitHub App](https://docs.github.com/en/developers/apps/creating-a-github-app).
    - ℹ For the list of required permissions, see [Authenticating to the GitHub API](https://github.com/actions/actions-runner-controller/blob/master/docs/authenticating-to-the-github-api.md#authenticating-to-the-github-api).

1. You're ready to install the autoscaling runner set. For additional configuration options, see [values.yaml](https://github.com/actions/actions-runner-controller/blob/master/charts/gha-runner-scale-set/values.yaml)
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
        oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set
    ```

    ```bash
    # Using a GitHub App
    INSTALLATION_NAME="arc-runner-set"
    NAMESPACE="arc-runners"
    GITHUB_CONFIG_URL="https://github.com/<your_enterprise/org/repo>"
    GITHUB_APP_ID="<GITHUB_APP_ID>"
    GITHUB_APP_INSTALLATION_ID="<GITHUB_APP_INSTALLATION_ID>"
    GITHUB_APP_PRIVATE_KEY="<GITHUB_APP_PRIVATE_KEY>"
    helm install "${INSTALLATION_NAME}" \
        --namespace "${NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="${GITHUB_CONFIG_URL}" \
        --set githubConfigSecret.github_app_id="${GITHUB_APP_ID}" \
        --set githubConfigSecret.github_app_installation_id="${GITHUB_APP_INSTALLATION_ID}" \
        --set githubConfigSecret.github_app_private_key="${GITHUB_APP_PRIVATE_KEY}" \
        oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set
    ```

1. Check your installation. If everything went well, you should see the following:

    ```bash
    $ helm list -n "${NAMESPACE}"

    NAME            NAMESPACE       REVISION        UPDATED                                 STATUS          CHART                                    APP VERSION
    arc             arc-systems     1               2023-01-18 10:03:36.610534934 +0000 UTC deployed        gha-runner-scale-set-controller-0.4.0        preview
    arc-runner-set  arc-systems     1               2023-01-18 10:20:14.795285645 +0000 UTC deployed        gha-runner-scale-set-0.4.0            0.4.0
    ```

    ```bash
    $ kubectl get pods -n "${NAMESPACE}"

    NAME                                              READY   STATUS    RESTARTS   AGE
    arc-gha-runner-scale-set-controller-8c74b6f95-gr7zr   1/1     Running   0          20m
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

    NAMESPACE     NAME                                                  READY   STATUS    RESTARTS      AGE
    arc-systems   arc-gha-runner-scale-set-controller-8c74b6f95-gr7zr   1/1     Running   0             27m
    arc-systems   arc-runner-set-6cd58d58-listener                      1/1     Running   0             7m52s
    arc-runners   arc-runner-set-rmrgw-runner-p9p5n                     1/1     Running   0             21s
    ```

### Upgrade to newer versions

Upgrading actions-runner-controller requires a few extra steps because CRDs will not be automatically upgraded (this is a helm limitation).

1. Uninstall the autoscaling runner set first

    ```bash
    INSTALLATION_NAME="arc-runner-set"
    NAMESPACE="arc-runners"
    helm uninstall "${INSTALLATION_NAME}" --namespace "${NAMESPACE}"
    ```

1. Wait for all the pods to drain

1. Pull the new helm chart, unpack it and update the CRDs. When applying this step, don't forget to replace `<PATH>` with the path of the `gha-runner-scale-set-controller` helm chart:

    ```bash
    helm pull oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller \
        --untar && \
        kubectl replace -f <PATH>/gha-runner-scale-set-controller/crds/
    ```

1. Reinstall actions-runner-controller using the steps from the previous section

## Troubleshooting

### I'm using the charts from the `master` branch and the controller is not working

The `master` branch is highly unstable! We offer no guarantees that the charts in the `master` branch will work at any given time. If you're using the charts from the `master` branch, you should expect to encounter issues. Please use the latest release instead.

### Controller pod is running but the runner set listener pod is not

You need to inspect the logs of the controller first and see if there are any errors. If there are no errors, and the runner set listener pod is still not running, you need to make sure that the **controller pod has access to the Kubernetes API server in your cluster!**

You'll see something similar to the following in the logs of the controller pod:

```log
kubectl logs <controller_pod_name> -c manager
17:35:28.661069       1 request.go:690] Waited for 1.032376652s due to client-side throttling, not priority and fairness, request: GET:https://10.0.0.1:443/apis/monitoring.coreos.com/v1alpha1?timeout=32s
2023-03-15T17:35:29Z    INFO    starting manager
```

If you have a proxy configured or you're using a sidecar proxy that's automatically injected (think [Istio](https://istio.io/)), you need to make sure it's configured appropriately to allow traffic from the controller container (manager) to the Kubernetes API server.

### Check the logs

You can check the logs of the controller pod using the following command:

```bash
# Controller logs
kubectl logs -n "${NAMESPACE}" -l app.kubernetes.io/name=gha-runner-scale-set-controller
```

```bash
# Runner set listener logs
kubectl logs -n "${NAMESPACE}" -l actions.github.com/scale-set-namespace=arc-systems -l actions.github.com/scale-set-name=arc-runner-set
```

### Naming error: `Name must have up to characters`

We are using some of the resources generated names as labels for other resources. Resource names have a max length of `263 characters` while labels are limited to `63 characters`. Given this constraint, we have to limit the resource names to `63 characters`.

Since part of the resource name is defined by you, we have to impose a limit on the amount of characters you can use for the installation and namespace names.

If you see these errors, you have to use shorter installation or namespace names.

```bash
Error: INSTALLATION FAILED: execution error at (gha-runner-scale-set/templates/autoscalingrunnerset.yaml:5:5): Name must have up to 45 characters

Error: INSTALLATION FAILED: execution error at (gha-runner-scale-set/templates/autoscalingrunnerset.yaml:8:5): Namespace must have up to 63 characters
```

### If you installed the autoscaling runner set, but the listener pod is not created

Verify that the secret you provided is correct and that the `githubConfigUrl` you provided is accurate.

### Access to the path `/home/runner/_work/_tool` is denied error

You might see this error if you're using kubernetes mode with persistent volumes. This is because the runner container is running with a non-root user and is causing a permissions mismatch with the mounted volume.

To fix this, you can either:

1. Use a volume type that supports `securityContext.fsGroup` (`hostPath` volumes don't support it, `local` volumes do as well as other types). Update the `fsGroup` of your runner pod to match the GID of the runner. You can do that by updating the `gha-runner-scale-set` helm chart values to include the following:

    ```yaml
    spec:
      securityContext:
        fsGroup: 123
      containers:
      - name: runner
        image: ghcr.io/actions/actions-runner:<VERSION> # Replace <VERSION> with the version you want to use
        command: ["/home/runner/run.sh"]
    ```

1. If updating the `securityContext` of your runner pod is not a viable solution, you can workaround the issue by using `initContainers` to change the mounted volume's ownership, as follows:

    ```yaml
    template:
    spec:
      initContainers:
      - name: kube-init
        image: ghcr.io/actions/actions-runner:latest
        command: ["sudo", "chown", "-R", "1001:123", "/home/runner/_work"]
        volumeMounts:
        - name: work
          mountPath: /home/runner/_work
      containers:
      - name: runner
        image: ghcr.io/actions/actions-runner:latest
        command: ["/home/runner/run.sh"]
    ```

## Changelog

### v0.4.0

#### ⚠️ Warning

This release contains a major change related to the way permissions are
applied to the manager ([#2276](https://github.com/actions/actions-runner-controller/pull/2276) and [#2363](https://github.com/actions/actions-runner-controller/pull/2363)).

Please evaluate these changes carefully before upgrading.

#### Major changes

1. Surface EphemeralRunnerSet stats to AutoscalingRunnerSet [#2382](https://github.com/actions/actions-runner-controller/pull/2382)
1. Improved security posture by removing list/watch secrets permission from manager cluster role
   [#2276](https://github.com/actions/actions-runner-controller/pull/2276)
1. Improved security posture by delaying role/rolebinding creation to gha-runner-scale-set during installation
   [#2363](https://github.com/actions/actions-runner-controller/pull/2363)
1. Improved security posture by supporting watching a single namespace from the controller
   [#2374](https://github.com/actions/actions-runner-controller/pull/2374)
1. Added labels to AutoscalingRunnerSet subresources to allow easier inspection [#2391](https://github.com/actions/actions-runner-controller/pull/2391)
1. Fixed bug preventing env variables from being specified
   [#2450](https://github.com/actions/actions-runner-controller/pull/2450)
1. Enhance quickstart troubleshooting guides
   [#2435](https://github.com/actions/actions-runner-controller/pull/2435)
1. Fixed ignore extra dind container when container mode type is "dind"
   [#2418](https://github.com/actions/actions-runner-controller/pull/2418)
1. Added additional cleanup finalizers [#2433](https://github.com/actions/actions-runner-controller/pull/2433)
1. gha-runner-scale-set listener pod inherits the ImagePullPolicy from the manager pod [#2477](https://github.com/actions/actions-runner-controller/pull/2477)
1. Treat `.ghe.com` domain as hosted environment [#2480](https://github.com/actions/actions-runner-controller/pull/2480)

### v0.3.0

#### Major changes

1. Runner pods are more similar to hosted runners [#2348](https://github.com/actions/actions-runner-controller/pull/2348)
1. Add support for self-signed CA certificates [#2268](https://github.com/actions/actions-runner-controller/pull/2268)
1. Fixed trailing slashes in config URLs breaking installations [#2381](https://github.com/actions/actions-runner-controller/pull/2381)
1. Fixed a bug where the listener pod would ignore proxy settings from env [#2366](https://github.com/actions/actions-runner-controller/pull/2366)
1. Added runner set name field making it optionally configurable [#2279](https://github.com/actions/actions-runner-controller/pull/2279)
1. Name and namespace labels of listener pod have been split [#2341](https://github.com/actions/actions-runner-controller/pull/2341)
1. Added chart name constraints validation on AutoscalingRunnerSet install [#2347](https://github.com/actions/actions-runner-controller/pull/2347)

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
