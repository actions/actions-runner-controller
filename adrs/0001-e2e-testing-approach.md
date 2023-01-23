# ADR 0001: ARC e2e testing approach

**Date**: 2023-01-23

**Status**: In Progress

## Context

We need to add end to end testing to support ARC in different environments. On one hand we will 
have a Linux VM and on the other, the following external platforms: EKS, GKE and AKS.

There are 2 aspects we need to consider: 
1. security for all platforms 
2. the pros and cons of spending resources for EKS, GKE and AKS

## 1. Security
These tests will make use of secrets like Personal Access Tokens.
Even if this data is not accessible in runs triggered by forks of this public repo, there are 
still cases that could have it exposed, like simple human error coming from the maintainers of ARC for example.
This creates some less probable but possible vulnerabilities that can lead to unwanted authorization. 

### Proposal

Create a new dummy org and repository to be used to register self-hosted runners and run ARC jobs for our e2e tests.
For authorization, we can create a GitHub App owned by this org with very limited privileges.
We will add the app_id and private_key as secrets to the actions/action-runner-controller and use them to register runners and trigger test workflows


## 2. Resources

There are several arguments for introducing tests run on managed Kubernetes services:
* Vendors of managed Kubernetes services could introduce changes that affects the behaviour of ARC
* We will need to troubleshoot customer issues in different environments
* Build knowledge in cloud provider environments to better support customers
* It'll be consistent for how the runner tests work at the moment

There are also a few arguments against it:
* Kubernetes should behave the same across cloud providers
* Increases the attack surface
* High maintenance effort for what could be flaky end to end tests
* Infrastructure costs
* Lots of red tape to go through to get access to these cloud providers and get the infra setup
* High effort for a free product that will have e2e tests covered regardless of the managed K8s services

### Proposal
A good compromise would be to setup ARC at least once in all these cloud providers to understand what we're dealing with and build knowledge.
