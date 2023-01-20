# Authenticating to the GitHub API

## Overview

There are two ways for Actions Runner Controller (ARC) to authenticate with the GitHub API, although you must one use only one of the options at a time.

1. Using a GitHub App. Note that using a GitHub App is not supported to manage enterprise-level runners.
2. Using a personal access token (classic).

ARC functions mostly the same using either authentication method, but if you authenticate using a GitHub App, there are increased API quotas compared to using a personal access token (classic). For more information, see "[Rate limits for GitHub Apps](https://docs.github.com/en/developers/apps/building-github-apps/rate-limits-for-github-apps)."

<!-- Version for GHES -->

If you are deploying ARC to a GitHub Enterprise Server environment, you can configure your instance's rate limit settings. For more information, see "[Configuring rate limits ](https://docs.github.com/en/admin/configuration/configuring-your-enterprise/configuring-rate-limits)."

<!-- End version for GHES -->

## Deploying using GitHub App authentication

You can create a GitHub App for either your user account or an organization. If you want to add runners at the organization level, make sure you create the GitHub App for your organization.

### Creating a GitHub App for a user account

1. To create a GitHub App for a user account, click the link below, which automatically sets the required permissions for a new app. If you want to manually create the app, the required permissions are listed in the next section.<!-- for GHES, edit the link -->

   * [Create a GitHub App on your user account](https://github.com/settings/apps/new?url=http://github.com/actions/actions-runner-controller&webhook_active=false&public=false&administration=write&actions=read)
1. On the "Register new GitHub App" page that the link opens, enter any unique app name in the "GitHub App name" field. <!-- Make a reusable -->
1. Click **Create GitHub App** at the bottom of the page. <!-- Make a reusable -->
1. On the GitHub App's page, note its **App ID**. This value is used later. <!-- Make a reusable -->

   <img width="750" alt="App ID" src="https://user-images.githubusercontent.com/230145/78968802-6e7c8880-7b40-11ea-8b08-0c1b8e6a15f0.png">
1. Under "Private keys", click **Generate a private key**, and save the `pem` file. This key is used later. <!-- Make a reusable -->

   <img width="750" alt="Generate a private key" src="https://user-images.githubusercontent.com/230145/78968805-71777900-7b40-11ea-97e6-55c48dfc44ac.png">
1. In the menu at the top-left of the page, click **Install app**, and then click **Install** to install the app on your user account. 

   <img width="750" alt="Install App" src="https://user-images.githubusercontent.com/230145/78968806-72100f80-7b40-11ea-810d-2bd3261e9d40.png">
1. After confirming the installation permissions on your user account, you are taken to the app installation page, which has the following URL format:

   `https://github.com/settings/installations/<INSTALLATION_ID>`

   where `<INSTALLATION_ID>` is a number that represents the app installation. Note this number, as it is used later.
1. Finally, register the App ID (`APP_ID`), Installation ID (`INSTALLATION_ID`), and the downloaded private key file (`PRIVATE_KEY_FILE_PATH`) from the previous steps to Kubernetes as a secret. <!-- Make a reusable -->

   * If you are using a `kubectl` deployment, you can use the following command:

     ```shell
     kubectl create secret generic controller-manager \
         -n actions-runner-system \
         --from-literal=github_app_id=${APP_ID} \
         --from-literal=github_app_installation_id=${INSTALLATION_ID} \
         --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH}
     ```
   * If you are using a Helm chart for your deployment, set the following keys in your `values.yaml` with their corresponding values above:
     * `authSecret.github_app_id`
     * `authSecret.github_app_installation_id`
     * `authSecret.github_app_private_key`

     For more information, see [Helm chart README](https://github.com/actions/actions-runner-controller/blob/master/charts/actions-runner-controller/README.md).

### Creating a GitHub App for an organization

1. To create a GitHub App for a user account, use the link below, which automatically sets the required permissions for a new app. In the URL, you must replace `<ORG_NAME>` with the name of your organization. If you want to manually create the app, the required permissions are listed in the next section.<!-- for GHES, edit the link -->

   * [Create GitHub Apps on your organization](https://github.com/organizations/<ORG_NAME>/settings/apps/new?url=http://github.com/actions/actions-runner-controller&webhook_active=false&public=false&administration=write&organization_self_hosted_runners=write&actions=read&checks=read)
1. On the "Register new GitHub App" page that the link opens, enter any unique app name in the "GitHub App name" field. <!-- Make a reusable -->
1. Click **Create GitHub App** at the bottom of the page. <!-- Make a reusable -->
1. On the GitHub App's page, note its **App ID**. This value is used later. <!-- Make a reusable -->

   <img width="750" alt="App ID" src="https://user-images.githubusercontent.com/230145/78968802-6e7c8880-7b40-11ea-8b08-0c1b8e6a15f0.png">
1. Under "Private keys", click **Generate a private key**, and save the `pem` file. This key is used later. <!-- Make a reusable -->

   <img width="750" alt="Generate a private key" src="https://user-images.githubusercontent.com/230145/78968805-71777900-7b40-11ea-97e6-55c48dfc44ac.png">
1. In the menu at the top-left of the page, click **Install app**, and then click **Install** to install the app on your organization.

   <img width="750" alt="Install App" src="https://user-images.githubusercontent.com/230145/78968806-72100f80-7b40-11ea-810d-2bd3261e9d40.png">
1. After confirming the installation permissions on your organization, you are taken to the app installation page, which has the following URL format:

   `https://github.com/<ORG_NAME>/nohomers/settings/installations/<INSTALLATION_ID>`

   where `<INSTALLATION_ID>` is a number that represents the app installation. Note this number, as it is used later.
1. Finally, register the App ID (`APP_ID`), Installation ID (`INSTALLATION_ID`), and the downloaded private key file (`PRIVATE_KEY_FILE_PATH`) from the previous steps to Kubernetes as a secret. <!-- Make a reusable -->

   * If you are using a `kubectl` deployment, you can use the following command:

     ```shell
     kubectl create secret generic controller-manager \
         -n actions-runner-system \
         --from-literal=github_app_id=${APP_ID} \
         --from-literal=github_app_installation_id=${INSTALLATION_ID} \
         --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH}
     ```
   * If you are using a Helm chart for your deployment, set the following keys in your `values.yaml` with their corresponding values above:
     * `authSecret.github_app_id`
     * `authSecret.github_app_installation_id`
     * `authSecret.github_app_private_key`

     For more information, see [Helm chart README](https://github.com/actions/actions-runner-controller/blob/master/charts/actions-runner-controller/README.md).

### Permissions required for the GitHub App

If you do not use the links in the previous sections to create your GitHub App, you must manually set the required permissions. For more information, see "[Creating a GitHub App](https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app)."

The permissions required for each type of runner are listed below. For more information on which API routes are mapped to which permission, see "[Permissions required for GitHub Apps](https://docs.github.com/en/rest/overview/permissions-required-for-github-apps)."

#### Required permissions for repository-level runners

**Repository Permissions**

* Actions (read)
* Administration (read / write)
* Checks (read) (if you are going to use [webhook-driven scaling](automatically-scaling-runners.md#webhook-driven-scaling))
* Metadata (read)

#### Required permissions for organization-level runners

**Repository Permissions**

* Actions (read)
* Metadata (read)

**Organization Permissions**

* Self-hosted runners (read / write)

## Deploying using personal access token (classic) authentication

Actions Runner Controller can use personal access tokens (classic) to register self-hosted runners.

1. Create a personal access token (classic) with the scopes listed below. The required scopes are different depending on whether you are registering runners at the repository, organization, or enterprise level. For more information on how to create the token, see "[Creating a personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token#creating-a-personal-access-token-classic)."

   **Required Scopes for Repository Runners**

   * repo (Full control)

   **Required Scopes for Organization Runners**

   * repo (Full control)
   * admin:org (Full control)
   * admin:public_key (read:public_key)
   * admin:repo_hook (read:repo_hook)
   * admin:org_hook (Full control)
   * notifications (Full control)
   * workflow (Full control)

   **Required Scopes for Enterprise Runners**

   * admin:enterprise (manage_runners:enterprise)

1. After you have created the personal access token (classic), deploy it as a secret to your Kubernetes cluster.

   * If you are using a `kubectl` deployment, you can use the following command:

     ```shell
     kubectl create secret generic controller-manager \
         -n actions-runner-system \
         --from-literal=github_token=${GITHUB_TOKEN}
     ```
   * If you are using a Helm chart for your deployment, in your `values.yaml`, set `authSecret.github_token` with the value of the token.

     For more information, see the [Helm chart README](https://github.com/actions/actions-runner-controller/blob/master/charts/actions-runner-controller/README.md).

## Deploying without using without cert-manager

There are two methods of deploying without using cert-manager: you can generate your own custom certificates, or rely on Helm to generate a certificate authority (CA) and certificate each time you update the chart.

### Using custom certificates

If you are installing in the default namespace, ensure your certificate has SANs. For example:

* `actions-runner-controller-webhook.actions-runner-system.svc`
* `actions-runner-controller-webhook.actions-runner-system.svc.cluster.local`

Install your certificate and key as a TLS secret:

* If you are using a `kubectl` deployment, you can use the following command, replacing the paths to your certificate and key files:

  ```shell
  kubectl create secret tls actions-runner-controller-serving-cert \
    -n actions-runner-system \
    --cert=path/to/cert/file \
    --key=path/to/key/file
  ```
* If you are using Helm for your deployment, set the Helm chart values with the following commands, replacing the path to your CA `pem` file:

  ```shell
  CA_BUNDLE=$(cat path/to/ca.pem | base64)
  helm upgrade --install actions/actions-runner-controller \
    certManagerEnabled=false \
    admissionWebHooks.caBundle=${CA_BUNDLE}
  ```

### Using Helm to generate the certificate authority and certificates

If you want Helm to generate the certificate authority (CA) and certificates, set the Helm chart values with the following command.

```shell
helm upgrade --install actions/actions-runner-controller \
  certManagerEnabled=false
```

This uses the helm `genCA` function to generate a temporary  certificate authority, and issues a certificate for the webhook.

> **Note**: This approach rotates the certificate authority and certificate each time `helm install` or `helm upgrade` are run. When this happens, it might cause short interruptions to the mutating webhook while the ARC pods stabilize and use the new certificate. It can affect kube-api activity because of the way mutating webhooks are called.

<!-- not sure if this section belongs in this article -->
## Using IAM roles for service accounts in Amazon EKS

> This feature requires controller version => [v0.15.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.15.0)

If you are using Amazon EKS, there might be some additional configuration to use IAM roles for service accounts.

Similar to regular pods and deployments, you need an existing service account with the appropriate IAM role associated.

1. Create a service account, for example using `eksctl`. For more information, see "[IAM roles for service accounts](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) in the Amazon EKS documentation.
1. Add the `serviceAccountName` and `fsGroup` to any pods that use the IAM role-enabled service account.

   * `serviceAccountName` is the name of your Amazon service account.
   * `fsGroup` must be set to the UID of the `runner` Linux user that runs the runner agent, or if you use `dind-runner`, runs `dockerd`. If you use an Ubuntu 20.04 runner image, the UID is `1000`, and for Ubuntu 22.04 it is `1001`.

   For `RunnerDeployment`, you can set those two fields under the runner spec at `RunnerDeployment.Spec.Template`. 

   ```yaml
   apiVersion: actions.summerwind.dev/v1alpha1
   kind: RunnerDeployment
   metadata:
     name: example-runnerdeploy
   spec:
     template:
       spec:
         repository: USER/REPO
         serviceAccountName: my-service-account
         securityContext:
           # For Ubuntu 20.04 runner
           fsGroup: 1000
           # Use 1001 for Ubuntu 22.04 runner
           #fsGroup: 1001
   ```
