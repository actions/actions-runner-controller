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
        --version 0.1.0
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
        oci://ghcr.io/actions/actions-runner-controller-charts/auto-scaling-runner-set --version 0.1.0
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
        oci://ghcr.io/actions/actions-runner-controller-charts/auto-scaling-runner-set --version 0.1.0
    ```

1. Check your installation. If everything went well, you should see the following:

    ```bash
    $ helm list -n "${NAMESPACE}"

    NAME            NAMESPACE       REVISION        UPDATED                                 STATUS          CHART                                    APP VERSION
    arc             arc-systems     1               2023-01-18 10:03:36.610534934 +0000 UTC deployed        actions-runner-controller-2-0.1.0        preview    
    arc-runner-set  arc-systems     1               2023-01-18 10:20:14.795285645 +0000 UTC deployed        auto-scaling-runner-set-0.1.0            0.1.0 
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
