# Actions Runner Controller (ARC)

[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/6061/badge)](https://bestpractices.coreinfrastructure.org/projects/6061)
[![awesome-runners](https://img.shields.io/badge/listed%20on-awesome--runners-blue.svg)](https://github.com/jonico/awesome-runners)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/actions-runner-controller)](https://artifacthub.io/packages/search?repo=actions-runner-controller)

## People

`actions-runner-controller` is an open-source project currently developed and maintained in collaboration with maintainers @mumoshu and @toast-gear, various [contributors](https://github.com/actions/actions-runner-controller/graphs/contributors), and the [awesome community](https://github.com/actions/actions-runner-controller/discussions), mostly in their spare time.

If you think the project is awesome and it's becoming a basis for your important business, consider [sponsoring us](https://github.com/sponsors/actions-runner-controller)!

In case you are already the employer of one of contributors, sponsoring via GitHub Sponsors might not be an option. Just support them in other means!

We don't currently have [any sponsors dedicated to this project yet](https://github.com/sponsors/actions-runner-controller).

However, [HelloFresh](https://www.hellofreshgroup.com/en/) has recently started sponsoring @mumoshu for this project along with his other works. A part of their sponsorship will enable @mumoshu to add an E2E test to keep ARC even more reliable on AWS. Thank you for your sponsorship!

[<img src="https://user-images.githubusercontent.com/22009/170898715-07f02941-35ec-418b-8cd4-251b422fa9ac.png" width="219" height="71" />](https://careers.hellofresh.com/)

## Status

Even though actions-runner-controller is used in production environments, it is still in its early stage of development, hence versioned 0.x.

actions-runner-controller complies to Semantic Versioning 2.0.0 in which v0.x means that there could be backward-incompatible changes for every release.

The documentation is kept inline with master@HEAD, we do our best to highlight any features that require a specific ARC version or higher however this is not always easily done due to there being many moving parts. Additionally, we actively do not retain compatibly with every GitHub Enterprise Server version nor every Kubernetes version so you will need to ensure you stay current within a reasonable timespan.

## About

[GitHub Actions](https://github.com/features/actions) is a very useful tool for automating development. GitHub Actions jobs are run in the cloud by default, but you may want to run your jobs in your environment. [Self-hosted runner](https://github.com/actions/runner) can be used for such use cases, but requires the provisioning and configuration of a virtual machine instance. Instead if you already have a Kubernetes cluster, it makes more sense to run the self-hosted runner on top of it.

**actions-runner-controller** makes that possible. Just create a *Runner* resource on your Kubernetes, and it will run and operate the self-hosted runner for the specified repository. Combined with Kubernetes RBAC, you can also build simple Self-hosted runners as a Service.

## Getting Started
To give ARC a try with just a handful of commands, Please refer to the [Quickstart guide](/docs/quickstart.md). 

For an overview of ARC, please refer to [ARC Overview](https://github.com/actions/actions-runner-controller/blob/master/docs/Actions-Runner-Controller-Overview.md)

For more information, please refer to detailed documentation below!

## Documentation

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
