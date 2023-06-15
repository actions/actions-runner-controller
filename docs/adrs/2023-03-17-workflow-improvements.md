# Improve ARC workflows for autoscaling runner sets

**Date**: 2023-03-17

**Status**: Done

## Context

In the [actions-runner-controller](https://github.com/actions/actions-runner-controller)
repository we essentially have two projects living side by side: the "legacy"
actions-runner-controller and the new one GitHub is supporting
(gha-runner-scale-set). To hasten progress we relied on existing workflows and
added some of our own (e.g.: end-to-end tests). We now got to a point where it's
sort of confusing what does what and why, not to mention the increased running
times of some those workflows and some GHA-related flaky tests getting in the
way of legacy ARC and viceversa. The three main areas we want to cover are: Go
code, Kubernetes manifests / Helm charts and E2E tests.

## Go code

At the moment we have three workflows that validate Go code:

- [golangci-lint](https://github.com/actions/actions-runner-controller/blob/34f3878/.github/workflows/golangci-lint.yaml):
  this is a collection of linters that currently runs on all PRs and push to
  master
- [Validate ARC](https://github.com/actions/actions-runner-controller/blob/01e9dd3/.github/workflows/validate-arc.yaml):
  this is a bit of a catch-all workflow, other than Go tests this also validates
  Kubernetes manifests, runs `go generate`, `go fmt` and `go vet`
- [Run CodeQL](https://github.com/actions/actions-runner-controller/blob/master/.github/workflows/global-run-codeql.yaml)

### Proposal

I think having one `Go` workflow that collects everything-Go would help a ton with
reliability and understandability of what's going on. This shouldn't be limited
to the GHA-supported mode as there are changes that even if made outside the GHA
code base could affect us (such as a dependency update).
This workflow should only run on changes to `*.go` files, `go.mod` and `go.sum`.
It should have these jobs, aiming to cover all existing functionality and
eliminate some duplication:

- `test`: run all Go tests in the project. We currently use the `-short` and
  `-coverprofile` flags: while `-short` is used to skip [old ARC E2E
  tests](https://github.com/actions/actions-runner-controller/blob/master/test/e2e/e2e_test.go#L85-L87),
  `-coverprofile` is adding to the test time without really giving us any value
  in return. We should also start using `actions/setup-go@v4` to take advantage
  of caching (it would speed up our tests by a lot) or enable it on `v3` if we
  have a strong reason not to upgrade. We should keep ignoring our E2E tests too
  as those will be run elsewhere (either use `Short` there too or ignoring the
  package like we currently do). As a dependency for tests this needs to run
  `make manifests` first: we should fail there and then if there is a diff.
- `fmt`: we currently run `go fmt ./...` as part of `Validate ARC` but do
  nothing with the results. We should fail in case of a diff. We don't need
  caching for this job.
- `lint`: this corresponds to what's currently the `golanci-lint` workflow (this
  also covers `go vet` which currently happens as part of `Validate ARC too`)
- `generate`: the current behaviour for this is actually quite risky, we
  generate our code in `Validate ARC` workflow and use the results to run the
  tests but we don't validate that up to date generate code is checked in. This
  job should run `go generate` and fail on a diff.
- `vulncheck`: **EDIT: this is covered by CodeQL** the Go team is maintaining [`govulncheck`](https://go.dev/blog/vuln), a tool to recursively
  analyzing all function calls in Go code and spot vulnerabilities on the call
  stack.

## Kubernetes manifests / Helm charts

We have [recently separated](https://github.com/actions/actions-runner-controller/commit/bd9f32e3540663360cf47f04acad26e6010f772e)
Helm chart validation and we validate up-to-dateness of manifests as part of `Go
/ test`.

## End to end tests

These tests are giving us really good coverage and should be one of the main
actors when it comes to trusting our releases. Two improvements that could be
done here are:

- renaming the workflow to `GHA E2E`: since renaming our resources the `gha`
  prefix has been used to identify things related to the mode GitHub supports
  and these jobs strictly validate the GitHub mode _only_. Having a shorter name
  allows for more readability of the various scenarios (e.g. `GHA E2E /
  single-namespace-setup`).
- the test currently monitors and validates the number of pods spawning during
  the workflow but not the outcome of the workflow. While not necessary to look
  at pods specifics, we should at least guarantee that the workflow can
  successfully conclude.
