# ADR 2022-10-17: Produce the runner image for the scaleset client

**Date**: 2022-10-17

**Status**: Done

# Breaking Changes

We aim to provide an similar experience (as close as possible) between self-hosted and GitHub-hosted runners. To achieve this, we are making the following changes to align our self-hosted runner container image with the Ubuntu runners managed by GitHub.
Here are the changes:

- We created a USER `runner(1001)` and a GROUP `docker(123)`
- `sudo` has been on the image and the `runner` will be a passwordless sudoer.
- The runner binary was placed placed under `/home/runner/` and launched using `/home/runner/run.sh`
- The runner's work directory is `/home/runner/_work`
- `$HOME` will point to `/home/runner`
- The container image user will be the `runner(1001)`

The latest Dockerfile can be found at: https://github.com/actions/runner/blob/main/images/Dockerfile

# Context

users can bring their own runner images, the contract we require is:

- It must have a runner binary under `/actions-runner` i.e. `/actions-runner/run.sh` exists
- The `WORKDIR` is set to `/actions-runner`
- If the user inside the container is root, the environment variable `RUNNER_ALLOW_RUNASROOT` should be set to `1`

The existing [ARC runner images](https://github.com/orgs/actions-runner-controller/packages?tab=packages&q=actions-runner) will not work with the new ARC mode out-of-box for the following reason:

- The current runner image requires the caller to pass runner configuration info, ex: URL and Config Token
- The current runner image has the runner binary under `/runner` which violates the contract described above
- The current runner image requires a special entrypoint script in order to work around some volume mount limitation for setting up DinD.

Since we expose the raw runner PodSpec to our end users, they can modify the helm `values.yaml` to adjust the runner container to their needs.

# Guiding Principles

- Build image is separated in two stages.

## The first stage (build)

- Reuses the same base image, so it is faster to build.
- Installs utilities needed to download assets (`runner` and `runner-container-hooks`).
- Downloads the runner and stores it into `/actions-runner` directory.
- Downloads the runner-container-hooks and stores it into `/actions-runner/k8s` directory.
- You can use build arguments to control the runner version, the target platform and runner container hooks version.

Preview (the published runner image might vary):

```Dockerfile
FROM mcr.microsoft.com/dotnet/runtime-deps:6.0 as build

ARG RUNNER_ARCH="x64"
ARG RUNNER_VERSION=2.298.2
ARG RUNNER_CONTAINER_HOOKS_VERSION=0.1.3

RUN apt update -y && apt install curl unzip -y

WORKDIR /actions-runner
RUN curl -f -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${RUNNER_ARCH}-${RUNNER_VERSION}.tar.gz \
    && tar xzf ./runner.tar.gz \
    && rm runner.tar.gz

RUN curl -f -L -o runner-container-hooks.zip https://github.com/actions/runner-container-hooks/releases/download/v${RUNNER_CONTAINER_HOOKS_VERSION}/actions-runner-hooks-k8s-${RUNNER_CONTAINER_HOOKS_VERSION}.zip \
    && unzip ./runner-container-hooks.zip -d ./k8s \
    && rm runner-container-hooks.zip
```

## The main image:

- Copies assets from the build stage to `/actions-runner`
- Does not provide an entrypoint. The entrypoint should be set within the container definition.

Preview:

```Dockerfile
FROM mcr.microsoft.com/dotnet/runtime-deps:6.0

WORKDIR /actions-runner
COPY --from=build /actions-runner .
```

## Example of pod spec with the init container copying assets

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: <name>
spec:
  containers:
    - name: runner
      image: <image>
      command: ["/runner/run.sh"]
      volumeMounts:
        - name: runner
          mountPath: /runner
  initContainers:
    - name: setup
      image: <image>
      command: ["sh", "-c", "cp -r /actions-runner/* /runner/"]
      volumeMounts:
        - name: runner
          mountPath: /runner
  volumes:
    - name: runner
      emptyDir: {}
```
