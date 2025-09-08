# ADR 2023-02-02: Automate updating runner version

**Date**: 2023-02-02

**Status**: Done

## Context

When a new [runner](https://github.com/actions/runner) version is released, new
images need to be built in
[actions-runner-controller/releases](https://github.com/actions-runner-controller/releases).
This is currently started by the
[release-runners](https://github.com/actions/actions-runner-controller/blob/master/.github/workflows/arc-release-runners.yaml)
workflow, although this only starts when the set of file containing the runner
version is updated (and this is currently done manually).

## Decision

We can have another workflow running on a cadence (hourly seems sensible) and checking for new runner
releases, creating a PR updating `RUNNER_VERSION` in:

- `.github/workflows/arc-release-runners.yaml`
- `Makefile`
- `runner/Makefile`
- `test/e2e/e2e_test.go`

Once that PR is merged, the existing workflow will pick things up.

## Consequences

We don't have to add an extra step to the runner release process and a direct
dependency on ARC. Since images won't be built until the generated PR is merged
we still have room to wait before triggering a build should there be any
problems with the runner release.

## Considered alternatives

We also considered firing the workflow to create the PR via
`repository_dispatch` as part of the release process of runner itself, but we
discarded it because that would have required a PAT or a GitHub app with `repo`
scope within the Actions org and would have added a new direct dependency on the
runner side.
