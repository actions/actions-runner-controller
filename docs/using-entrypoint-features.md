# Using entrypoint features

## Runner Entrypoint Features

> Environment variable values must all be strings

The entrypoint script is aware of a few environment variables for configuring features:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeployment
spec:
  template:
    spec:
      env:
        # Disable various runner entrypoint log levels 
        - name: LOG_DEBUG_DISABLED
          value: "true"
        - name: LOG_NOTICE_DISABLED
          value: "true"
        - name: LOG_WARNING_DISABLED
          value: "true"
        - name: LOG_ERROR_DISABLED
          value: "true"
        - name: LOG_SUCCESS_DISABLED
          value: "true"
        # Issues a sleep command at the start of the entrypoint
        - name: STARTUP_DELAY_IN_SECONDS
          value: "2"
        # Specify the duration to wait for the docker daemon to be available
        # The default duration of 120 seconds is sometimes too short
        # to reliably wait for the docker daemon to start
        # See https://github.com/actions/actions-runner-controller/issues/1804
        - name: WAIT_FOR_DOCKER_SECONDS
          value: 120
        # Disables the wait for the docker daemon to be available check
        - name: DISABLE_WAIT_FOR_DOCKER
          value: "true"
        # Disables automatic runner updates
        # WARNING : Upon a new version of the actions/runner software being released 
        # GitHub stops allocating jobs to runners on the previous version of the
        # actions/runner software after 30 days.
        - name: DISABLE_RUNNER_UPDATE
          value: "true"
```

There are a few advanced envvars also that are available only for dind runners:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeployment
spec:
  template:
    spec:
      dockerdWithinRunnerContainer: true
      image: summerwind/actions-runner-dind
      env:
        # Sets the respective default-address-pools fields within dockerd daemon.json
        # See https://github.com/actions/actions-runner-controller/pull/1971 for more information.
        # Also see https://github.com/docker/docs/issues/8663 for the default base/size values in dockerd.
        - name: DOCKER_DEFAULT_ADDRESS_POOL_BASE
          value: "172.17.0.0/12"
        - name: DOCKER_DEFAULT_ADDRESS_POOL_SIZE
          value: "24"
```