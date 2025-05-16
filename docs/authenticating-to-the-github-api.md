# Authenticating to the GitHub API

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Setting Up Authentication with GitHub API

There are two ways for actions-runner-controller to authenticate with the GitHub API (only 1 can be configured at a time however):

1. Using a GitHub App (not supported for enterprise level runners due to lack of support from GitHub)
2. Using a PAT

Functionality wise, there isn't much of a difference between the 2 authentication methods. The primary benefit of authenticating via a GitHub App is an [increased API quota](https://docs.github.com/en/developers/apps/rate-limits-for-github-apps).

If you are deploying the solution for a GHES environment you are able to [configure your rate limit settings](https://docs.github.com/en/enterprise-server@3.0/admin/configuration/configuring-rate-limits) making the main benefit irrelevant. If you're deploying the solution for a GHEC or regular GitHub environment and you run into rate limit issues, consider deploying the solution using the GitHub App authentication method instead.

### Deploying Using GitHub App Authentication

You can create a GitHub App for either your user account or any organization, below are the app permissions required for each supported type of runner:

_Note: Links are provided further down to create an app for your logged in user account or an organization with the permissions for all runner types set in each link's query string_

**Required Permissions for Repository Runners:**<br />
**Repository Permissions**

* Actions (read)
* Administration (read / write)
* Checks (read) (if you are going to use [Webhook Driven Scaling](automatically-scaling-runners.md#webhook-driven-scaling))
* Metadata (read)

**Required Permissions for Organization Runners:**<br />
**Repository Permissions**

* Actions (read)
* Metadata (read)

**Organization Permissions**

* Self-hosted runners (read / write)

_Note: All API routes mapped to their permissions can be found [here](https://docs.github.com/en/rest/reference/permissions-required-for-github-apps) if you wish to review_

**Subscribe to events**

At this point you have a choice of configuring a webhook, a webhook is needed if you are going to use [webhook driven scaling](automatically-scaling-runners.md#webhook-driven-scaling). The webhook can be configured centrally in the GitHub app itself or separately. In either case you need to subscribe to the `Workflow Job` event.

---

**Setup Steps**

If you want to create a GitHub App for your account, open the following link to the creation page, enter any unique name in the "GitHub App name" field, and hit the "Create GitHub App" button at the bottom of the page.

- [Create GitHub Apps on your account](https://github.com/settings/apps/new?url=http://github.com/actions/actions-runner-controller&webhook_active=false&public=false&administration=write&actions=read)

If you want to create a GitHub App for your organization, replace the `:org` part of the following URL with your organization name before opening it. Then enter any unique name in the "GitHub App name" field, and hit the "Create GitHub App" button at the bottom of the page to create a GitHub App.

- [Create GitHub Apps on your organization](https://github.com/organizations/:org/settings/apps/new?url=http://github.com/actions/actions-runner-controller&webhook_active=false&public=false&administration=write&organization_self_hosted_runners=write&actions=read&checks=read)

You will see an *App ID* on the page of the GitHub App you created as follows, the value of this App ID will be used later.

<img width="750" alt="App ID" src="https://user-images.githubusercontent.com/230145/78968802-6e7c8880-7b40-11ea-8b08-0c1b8e6a15f0.png">

Download the private key file by pushing the "Generate a private key" button at the bottom of the GitHub App page. This file will also be used later.

<img width="750" alt="Generate a private key" src="https://user-images.githubusercontent.com/230145/78968805-71777900-7b40-11ea-97e6-55c48dfc44ac.png">

Go to the "Install App" tab on the left side of the page and install the GitHub App that you created for your account or organization.

<img width="750" alt="Install App" src="https://user-images.githubusercontent.com/230145/78968806-72100f80-7b40-11ea-810d-2bd3261e9d40.png">

When the installation is complete, you will be taken to a URL in one of the following formats, the last number of the URL will be used as the Installation ID later (For example, if the URL ends in `settings/installations/12345`, then the Installation ID is `12345`).

- `https://github.com/settings/installations/${INSTALLATION_ID}`
- `https://github.com/organizations/eventreactor/settings/installations/${INSTALLATION_ID}`


Finally, register the App ID (`APP_ID`), Installation ID (`INSTALLATION_ID`), and the downloaded private key file (`PRIVATE_KEY_FILE_PATH`) to Kubernetes as a secret.

**Kubectl Deployment:**

```shell
$ kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_app_id=${APP_ID} \
    --from-literal=github_app_installation_id=${INSTALLATION_ID} \
    --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH}
```

**Helm Deployment:**

Configure your values.yaml, see the chart's [README](../charts/actions-runner-controller/README.md) for deploying the secret via Helm

### Deploying Using PAT Authentication

Personal Access Tokens can be used to register a self-hosted runner by *actions-runner-controller*.

Log-in to a GitHub account that has `admin` privileges for the repository, and [create a personal access token](https://github.com/settings/tokens/new) with the appropriate scopes listed below:

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

_Note: When you deploy enterprise runners they will get access to organizations, however, access to the repositories themselves is **NOT** allowed by default. Each GitHub organization must allow enterprise runner groups to be used in repositories as an initial one-time configuration step, this only needs to be done once after which it is permanent for that runner group._

_Note: GitHub does not document exactly what permissions you get with each PAT scope beyond a vague description. The best documentation they provide on the topic can be found [here](https://docs.github.com/en/developers/apps/building-oauth-apps/scopes-for-oauth-apps) if you wish to review. The docs target OAuth apps and so are incomplete and may not be 100% accurate._

---

Once you have created the appropriate token, deploy it as a secret to your Kubernetes cluster that you are going to deploy the solution on:

**Kubectl Deployment:**

```shell
kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_token=${GITHUB_TOKEN}
```

**Helm Deployment:**

Configure your values.yaml, see the chart's [README](../charts/actions-runner-controller/README.md) for deploying the secret via Helm


### Using without cert-manager

There are two methods of deploying without cert-manager, you can generate your own certificates or rely on helm to generate a CA and certificate each time you update the chart.

#### Using custom certificates

Assuming you are installing in the default namespace, ensure your certificate has SANs:

* `actions-runner-controller-webhook.actions-runner-system.svc`
* `actions-runner-controller-webhook.actions-runner-system.svc.cluster.local`

It is possible to use a self-signed certificate by following a guide like
[this one](https://mariadb.com/docs/security/encryption/in-transit/create-self-signed-certificates-keys-openssl/)
using `openssl`.

Install your certificate as a TLS secret:

```shell
$ kubectl create secret tls actions-runner-controller-serving-cert \
  -n actions-runner-system \
  --cert=path/to/cert/file \
  --key=path/to/key/file
```

Set the Helm chart values as follows:

```shell
$ CA_BUNDLE=$(cat path/to/ca.pem | base64)
$ helm upgrade --install actions-runner-controller/actions-runner-controller \
  certManagerEnabled=false \
  admissionWebHooks.caBundle=${CA_BUNDLE}
```

#### Using helm to generate CA and certificates

Set the Helm chart values as follows:

```shell
$ helm upgrade --install actions-runner-controller/actions-runner-controller \
  certManagerEnabled=false
```

This generates a temporary CA using the helm `genCA` function and issues a certificate for the webhook. Note that this approach rotates the CA and certificate each time `helm install` or `helm upgrade` are run. In effect, this will cause short interruptions to the mutating webhook while the ARC pods stabilize and use the new certificate each time `helm upgrade` is called for the chart. The outage can affect kube-api activity due to the way mutating webhooks are called.

### Using IRSA (IAM Roles for Service Accounts) in EKS

> This feature requires controller version => [v0.15.0](https://github.com/actions/actions-runner-controller/releases/tag/v0.15.0)

Similar to regular pods and deployments, you firstly need an existing service account with the IAM role associated.
Create one using e.g. `eksctl`. You can refer to [the EKS documentation](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) for more details.

Once you set up the service account, all you need is to add `serviceAccountName` and `fsGroup` to any pods that use the IAM-role enabled service account.

`fsGroup` needs to be set to the UID of the `runner` Linux user that runs the runner agent (and dockerd in case you use dind-runner). For anyone using an Ubuntu 20.04 runner image it's `1000` and for Ubuntu 22.04 and 24.04 one it's `1001`.

For `RunnerDeployment`, you can set those two fields under the runner spec at `RunnerDeployment.Spec.Template`:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeploy
spec:
  template:
    spec:
      repository: USER/REO
      serviceAccountName: my-service-account
      securityContext:
        # For Ubuntu 20.04 runner
        fsGroup: 1000
        # Use 1001 for Ubuntu 22.04 and 24.04 runner
        #fsGroup: 1001
```

