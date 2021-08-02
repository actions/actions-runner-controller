#!/bin/bash

RUNNER_HOME=${RUNNER_HOME:-/runner}

LIGHTGREEN="\e[0;32m"
LIGHTRED="\e[0;31m"
WHITE="\e[0;97m"
RESET="\e[0m"

log(){
  printf "${WHITE}${@}${RESET}\n" 1>&2
}

success(){
  printf "${LIGHTGREEN}${@}${RESET}\n" 1>&2
}

error(){
  printf "${LIGHTRED}${@}${RESET}\n" 1>&2
}

if [ ! -z "${STARTUP_DELAY_IN_SECONDS}" ]; then
  log "Delaying startup by ${STARTUP_DELAY_IN_SECONDS} seconds"
  sleep ${STARTUP_DELAY_IN_SECONDS}
fi


if [ -z "${GITHUB_URL}" ]; then
  log "Working with public GitHub"
  GITHUB_URL="https://github.com/"
else
  length=${#GITHUB_URL}
  last_char=${GITHUB_URL:length-1:1}

  [[ $last_char != "/" ]] && GITHUB_URL="$GITHUB_URL/"; :
  log "Github endpoint URL ${GITHUB_URL}"
fi

if [ -z "${RUNNER_NAME}" ]; then
  error "RUNNER_NAME must be set"
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
  error "At least one of RUNNER_ORG or RUNNER_REPO or RUNNER_ENTERPRISE must be set"
  exit 1
fi

if [ -z "${RUNNER_TOKEN}" ]; then
  error "RUNNER_TOKEN must be set"
  exit 1
fi

if [ -z "${RUNNER_REPO}" ] && [ -n "${RUNNER_GROUP}" ];then
  RUNNER_GROUPS=${RUNNER_GROUP}
fi

# Hack due to https://github.com/actions-runner-controller/actions-runner-controller/issues/252#issuecomment-758338483
if [ ! -d "${RUNNER_HOME}" ]; then
  error "${RUNNER_HOME} should be an emptyDir mount. Please fix the pod spec."
  exit 1
fi

# if this is not a testing environment
if [ -z "${UNITTEST:-}" ]; then
  sudo chown -R runner:docker ${RUNNER_HOME}
  mv /runnertmp/* ${RUNNER_HOME}/
fi

cd ${RUNNER_HOME}
# past that point, it's all relative pathes from /runner

config_args=()
if [ "${RUNNER_FEATURE_FLAG_EPHEMERAL:-}" == "true" -a "${RUNNER_EPHEMERAL}" != "false" ]; then
  config_args+=(--ephemeral)
  echo "Passing --ephemeral to config.sh to enable the ephemeral runner."
fi

retries_left=10
while [[ ${retries_left} -gt 0 ]]; do
  log "Configuring the runner."
  ./config.sh --unattended --replace \
    --name "${RUNNER_NAME}" \
    --url "${GITHUB_URL}${ATTACH}" \
    --token "${RUNNER_TOKEN}" \
    --runnergroup "${RUNNER_GROUPS}" \
    --labels "${RUNNER_LABELS}" \
    --work "${RUNNER_WORKDIR}" "${config_args[@]}"

  if [ -f .runner ]; then
    success "Runner successfully configured."
    break
  fi

  error "Configuration failed. Retrying"
  retries_left=$((retries_left - 1))
  sleep 1
done

if [ ! -f .runner ]; then
  # we couldn't configure and register the runner; no point continuing
  error "Configuration failed!"
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

if [ -n "${RUNNER_REGISTRATION_ONLY}" ]; then
  success "This runner is configured to be registration-only. Exiting without starting the runner service..."
  exit 0
fi

if [ -z "${UNITTEST:-}" ]; then
  mkdir ./externals
  # Hack due to the DinD volumes
  mv ./externalstmp/* ./externals/

  for f in runsvc.sh RunnerService.js; do
    diff {bin,patched}/${f} || :
    sudo mv bin/${f}{,.bak}
    sudo mv {patched,bin}/${f}
  done
fi

args=()
if [ "${RUNNER_FEATURE_FLAG_EPHEMERAL:-}" != "true" -a "${RUNNER_EPHEMERAL}" != "false" ]; then
  args+=(--once)
  echo "Passing --once to runsvc.sh to enable the legacy ephemeral runner."
fi

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN
exec ./bin/runsvc.sh "${args[@]}"
