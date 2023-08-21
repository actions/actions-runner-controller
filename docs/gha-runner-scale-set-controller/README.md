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

You can follow [this quickstart guide](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller) for installation steps.

## Troubleshooting

You can follow [this troubleshooting guide](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/troubleshooting-actions-runner-controller-errors) for troubleshooting steps.

## Changelog

### v0.5.0

1. Provide scale-set listener metrics [#2559](https://github.com/actions/actions-runner-controller/pull/2559)
1. Add DrainJobsMode [#2569](https://github.com/actions/actions-runner-controller/pull/2569)
1. Trim gha-runner-scale-set to gha-rs in names and remove role type suffixes [#2706](https://github.com/actions/actions-runner-controller/pull/2706)
1. Adapt role name to prevent namespace collision [#2617](https://github.com/actions/actions-runner-controller/pull/2617)
1. Add status check before deserializing runner-registration response [#2699](https://github.com/actions/actions-runner-controller/pull/2699)
1. Add configurable log format to values.yaml and propagate it to listener [#2686](https://github.com/actions/actions-runner-controller/pull/2686)
1. Extend manager roles to accept ephemeralrunnerset/finalizers [#2493](https://github.com/actions/actions-runner-controller/pull/2493)
1. Trim repo/org/enterprise to 63 characters in label values [#2657](https://github.com/actions/actions-runner-controller/pull/2657)
1. Discard logs on helm chart tests [#2607](https://github.com/actions/actions-runner-controller/pull/2607)
1. Use build.Version to check if resource version is a mismatch [#2521](https://github.com/actions/actions-runner-controller/pull/2521)
1. Reordering methods and constants so it is easier to look it up [#2501](https://github.com/actions/actions-runner-controller/pull/2501)
1. chore: Set build version on make-runscaleset [#2713](https://github.com/actions/actions-runner-controller/pull/2713)
1. Fix scaling back to 0 after min runners were set to number > 0 [#2742](https://github.com/actions/actions-runner-controller/pull/2742)
1. Document customization for containerModes [#2777](https://github.com/actions/actions-runner-controller/pull/2777)
1. Bump github.com/cloudflare/circl from 1.1.0 to 1.3.3 [#2628](https://github.com/actions/actions-runner-controller/pull/2628)
1. chore(deps): bump github.com/stretchr/testify from 1.8.2 to 1.8.4 [#2716](https://github.com/actions/actions-runner-controller/pull/2716)
1. Move gha-* docs out of preview [#2779](https://github.com/actions/actions-runner-controller/pull/2779)
1. Prepare 0.5.0 release [#2783](https://github.com/actions/actions-runner-controller/pull/2783)
1. Security fix [#2676](https://github.com/actions/actions-runner-controller/pull/2676)

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
