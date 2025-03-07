# Actions Runner Controller (ARC)

[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/6061/badge)](https://bestpractices.coreinfrastructure.org/projects/6061)
[![awesome-runners](https://img.shields.io/badge/listed%20on-awesome--runners-blue.svg)](https://github.com/jonico/awesome-runners)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/actions-runner-controller)](https://artifacthub.io/packages/search?repo=actions-runner-controller)

## About

Actions Runner Controller (ARC) is a Kubernetes operator that orchestrates and scales self-hosted runners for GitHub Actions.

With ARC, you can create runner scale sets that automatically scale based on the number of workflows running in your repository, organization, or enterprise. Because controlled runners can be ephemeral and based on containers, new runner instances can scale up or down rapidly and cleanly. For more information about autoscaling, see ["Autoscaling with self-hosted runners."](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/autoscaling-with-self-hosted-runners)

You can set up ARC on Kubernetes using Helm, then create and run a workflow that uses runner scale sets. For more information about runner scale sets, see ["Deploying runner scale sets with Actions Runner Controller."](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/deploying-runner-scale-sets-with-actions-runner-controller#runner-scale-set)

## People

Actions Runner Controller (ARC) is an open-source project currently developed and maintained in collaboration with the GitHub Actions team, external maintainers @mumoshu and @toast-gear, various [contributors](https://github.com/actions/actions-runner-controller/graphs/contributors), and the [awesome community](https://github.com/actions/actions-runner-controller/discussions).

If you think the project is awesome and is adding value to your business, please consider directly sponsoring [community maintainers](https://github.com/sponsors/actions-runner-controller) and individual contributors via GitHub Sponsors.

If you are already the employer of one of the contributors, sponsoring via GitHub Sponsors might not be an option. Just support them by other means!

See [the sponsorship dashboard](https://github.com/sponsors/actions-runner-controller) for the former and the current sponsors.

## Getting Started

To give ARC a try with just a handful of commands, please refer to the [Quickstart guide](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller).

For an overview of ARC, please refer to [About ARC](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/about-actions-runner-controller).

With the introduction of [autoscaling runner scale sets](https://github.com/actions/actions-runner-controller/discussions/2775), the existing [autoscaling modes](./docs/automatically-scaling-runners.md) are now legacy. The legacy modes have certain use cases and will continue to be maintained by the community only.

For further information on what is supported by GitHub and what's managed by the community, please refer to [this announcement discussion.](https://github.com/actions/actions-runner-controller/discussions/2775)

### Documentation

ARC documentation is available on [docs.github.com](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller).

### Legacy documentation

The following documentation is for the legacy autoscaling modes that continue to be maintained by the community:

- [Quickstart guide](/docs/quickstart.md)
- [About ARC](/docs/about-arc.md)
- [Installing ARC](/docs/installing-arc.md)
- [Authenticating to the GitHub API](/docs/authenticating-to-the-github-api.md)
- [Deploying ARC runners](/docs/deploying-arc-runners.md)
- [Adding ARC runners to a repository, organization, or enterprise](/docs/choosing-runner-destination.md)
- [Automatically scaling runners](/docs/automatically-scaling-runners.md)
- [Using custom volumes](/docs/using-custom-volumes.md)
- [Using ARC runners in a workflow](/docs/using-arc-runners-in-a-workflow.md)
- [Managing access with runner groups](/docs/managing-access-with-runner-groups.md)
- [Configuring Windows runners](/docs/configuring-windows-runners.md)
- [Using ARC across organizations](/docs/using-arc-across-organizations.md)
- [Using entrypoint features](/docs/using-entrypoint-features.md)
- [Deploying alternative runners](/docs/deploying-alternative-runners.md)
- [Monitoring and troubleshooting](/docs/monitoring-and-troubleshooting.md)

## Contributing

We welcome contributions from the community. For more details on contributing to the project (including requirements), please refer to "[Getting Started with Contributing](https://github.com/actions/actions-runner-controller/blob/master/CONTRIBUTING.md)."

## Troubleshooting

We are very happy to help you with any issues you have. Please refer to the "[Troubleshooting](https://github.com/actions/actions-runner-controller/blob/master/TROUBLESHOOTING.md)" section for common issues.
