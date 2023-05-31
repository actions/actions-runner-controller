#!/bin/bash
source logger.sh

RUNNER_ASSETS_DIR=${RUNNER_ASSETS_DIR:-/runnertmp}
RUNNER_HOME=${RUNNER_HOME:-/runner}

# Let GitHub runner execute these hooks. These environment variables are used by GitHub's Runner as described here
# https://github.com/actions/runner/blob/main/docs/adrs/1751-runner-job-hooks.md
# Scripts referenced in the ACTIONS_RUNNER_HOOK_ environment variables must end in .sh or .ps1
# for it to become a valid hook script, otherwise GitHub will fail to run the hook
export ACTIONS_RUNNER_HOOK_JOB_STARTED=/etc/arc/hooks/job-started.sh
export ACTIONS_RUNNER_HOOK_JOB_COMPLETED=/etc/arc/hooks/job-completed.sh

if [ -n "${STARTUP_DELAY_IN_SECONDS}" ]; then
  log.notice "Delaying startup by ${STARTUP_DELAY_IN_SECONDS} seconds"
  sleep "${STARTUP_DELAY_IN_SECONDS}"
fi

if ! cd "${RUNNER_HOME}"; then
  log.error "Failed to cd into ${RUNNER_HOME}"
  exit 1
fi

# past that point, it's all relative pathes from /runner

# This is for registering ARC v0 runners.
# ARC v1 runners do not need config.sh for registering themselves.
# ARC v1 runners are supposed to given the ACTIONS_RUNNER_INPUT_JITCONFIG envvars
# so we use it as the trigger to skip config.sh.
if [ -z "${ACTIONS_RUNNER_INPUT_JITCONFIG:-}" ]; then
  if [ -z "${GITHUB_URL}" ]; then
    log.debug 'Working with public GitHub'
    GITHUB_URL="https://github.com/"
  else
    length=${#GITHUB_URL}
    last_char=${GITHUB_URL:length-1:1}

    [[ $last_char != "/" ]] && GITHUB_URL="$GITHUB_URL/"; :
    log.debug "Github endpoint URL ${GITHUB_URL}"
  fi

  if [ -z "${RUNNER_NAME}" ]; then
    log.error 'RUNNER_NAME must be set'
    exit 1
  fi

  if [ -n "${RUNNER_ORG}" ] && [ -n "${RUNNER_REPO}" ] && [ -n "${RUNNER_ENTERPRISE}" ]; then
    ATTACH="${RUNNER_ORG}/${RUNNER_REPO}"
  elif [ -n "${RUNNER_ORG}" ]; then
    ATTACH="${RUNNER_ORG}"
  elif [ -n "${RUNNER_REPO}" ]; then
    ATTACH="${RUNNER_REPO}"
  elif [ -n "${RUNNER_ENTERPRISE}" ]; then
    ATTACH="enterprises/${RUNNER_ENTERPRISE}"
  else
    log.error 'At least one of RUNNER_ORG, RUNNER_REPO, or RUNNER_ENTERPRISE must be set'
    exit 1
  fi

  if [ -z "${RUNNER_TOKEN}" ]; then
    log.error 'RUNNER_TOKEN must be set'
    exit 1
  fi

  if [ -z "${RUNNER_REPO}" ] && [ -n "${RUNNER_GROUP}" ];then
    RUNNER_GROUPS=${RUNNER_GROUP}
  fi

  # Hack due to https://github.com/actions/actions-runner-controller/issues/252#issuecomment-758338483
  if [ ! -d "${RUNNER_HOME}" ]; then
    log.error "$RUNNER_HOME should be an emptyDir mount. Please fix the pod spec."
    exit 1
  fi

  # if this is not a testing environment
  if [[ "${UNITTEST:-}" == '' ]]; then
    sudo chown -R runner:docker "$RUNNER_HOME"
    # enable dotglob so we can copy a ".env" file to load in env vars as part of the service startup if one is provided
    # loading a .env from the root of the service is part of the actions/runner logic
    shopt -s dotglob
    # use cp instead of mv to avoid issues when src and dst are on different devices
    cp -r "$RUNNER_ASSETS_DIR"/* "$RUNNER_HOME"/
    shopt -u dotglob
  fi

  config_args=()
  if [ "${RUNNER_FEATURE_FLAG_ONCE:-}" != "true" ] && [ "${RUNNER_EPHEMERAL}" == "true" ]; then
    config_args+=(--ephemeral)
    log.debug 'Passing --ephemeral to config.sh to enable the ephemeral runner.'
  fi
  if [ "${DISABLE_RUNNER_UPDATE:-}" == "true" ]; then
    config_args+=(--disableupdate)
    log.debug 'Passing --disableupdate to config.sh to disable automatic runner updates.'
  fi

  update-status "Registering"

  retries_left=10
  while [[ ${retries_left} -gt 0 ]]; do
    log.debug 'Configuring the runner.'
    ./config.sh --unattended --replace \
      --name "${RUNNER_NAME}" \
      --url "${GITHUB_URL}${ATTACH}" \
      --token "${RUNNER_TOKEN}" \
      --runnergroup "${RUNNER_GROUPS}" \
      --labels "${RUNNER_LABELS}" \
      --work "${RUNNER_WORKDIR}" "${config_args[@]}"

    if [ -f .runner ]; then
      log.debug 'Runner successfully configured.'
      break
    fi

    log.debug 'Configuration failed. Retrying'
    retries_left=$((retries_left - 1))
    sleep 1
  done

  # Note that ARC v1 runners do create this file, but only after the runner
  # agent is up and running.
  # On the other hand, this logic assumes the file to be created BEFORE
  # the runner is up, by running `config.sh`, which is not present in a v1 runner deployment.
  # That's why we need to skip this check for v1 runners.
  # Otherwise v1 runner will never start up due to this check.
  if [ ! -f .runner ]; then
    # we couldn't configure and register the runner; no point continuing
    log.error 'Configuration failed!'
    exit 2
  fi

  cat .runner
  # Note: the `.runner` file's content should be something like the below:
  #
  # $ cat /runner/.runner
  # {
  # "agentId": 117, #=> corresponds to the ID of the runner
  # "agentName": "THE_RUNNER_POD_NAME",
  # "poolId": 1,
  # "poolName": "Default",
  # "serverUrl": "https://pipelines.actions.githubusercontent.com/SOME_RANDOM_ID",
  # "gitHubUrl": "https://github.com/USER/REPO",
  # "workFolder": "/some/work/dir" #=> corresponds to Runner.Spec.WorkDir
  # }
  #
  # Especially `agentId` is important, as other than listing all the runners in the repo,
  # this is the only change we could get the exact runnner ID which can be useful for further
  # GitHub API call like the below. Note that 171 is the agentId seen above.
  #   curl \
  #     -H "Accept: application/vnd.github.v3+json" \
  #     -H "Authorization: bearer ${GITHUB_TOKEN}"
  #     https://api.github.com/repos/USER/REPO/actions/runners/171
fi

# Hack due to the DinD volumes
# This is necessary only for legacy ARC v0.x.
# ARC v1.x uses the "externals" as the copy source and "tmpDir" as the copy destionation.
# See https://github.com/actions/actions-runner-controller/blob/91c8991835016f8c6568f101d4a28185baec3dcc/charts/gha-runner-scale-set/templates/_helpers.tpl#L76-L87
if [ -z "${UNITTEST:-}" ] && [ -e ./externalstmp ]; then
  mkdir -p ./externals
  mv ./externalstmp/* ./externals/
fi

WAIT_FOR_DOCKER_SECONDS=${WAIT_FOR_DOCKER_SECONDS:-120}
if [[ "${DISABLE_WAIT_FOR_DOCKER}" != "true" ]] && [[ "${DOCKER_ENABLED}" == "true" ]]; then
    log.debug 'Docker enabled runner detected and Docker daemon wait is enabled'
    log.debug "Waiting until Docker is available or the timeout of ${WAIT_FOR_DOCKER_SECONDS} seconds is reached"
    if ! timeout "${WAIT_FOR_DOCKER_SECONDS}s" bash -c 'until docker ps ;do sleep 1; done'; then
      log.notice "Docker has not become available within ${WAIT_FOR_DOCKER_SECONDS} seconds. Exiting with status 1."
      exit 1
    fi
else
  log.notice 'Docker wait check skipped. Either Docker is disabled or the wait is disabled, continuing with entrypoint'
fi

# Unset entrypoint environment variables so they don't leak into the runner environment
unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN STARTUP_DELAY_IN_SECONDS DISABLE_WAIT_FOR_DOCKER

# Docker ignores PAM and thus never loads the system environment variables that
# are meant to be set in every environment of every user. We emulate the PAM
# behavior by reading the environment variables without interpreting them.
#
# https://github.com/actions/actions-runner-controller/issues/1135
# https://github.com/actions/runner/issues/1703

# /etc/environment may not exist when running unit tests depending on the platform being used
# (e.g. Mac OS) so we just skip the mapping entirely
if [ -z "${UNITTEST:-}" ]; then
  mapfile -t env </etc/environment
fi

log.notice "WARNING LATEST TAG HAS BEEN DEPRECATED. SEE GITHUB ISSUE FOR DETAILS:"
log.notice "https://github.com/actions/actions-runner-controller/issues/2056"

update-status "Idle"
exec env -- "${env[@]}" ./run.sh
