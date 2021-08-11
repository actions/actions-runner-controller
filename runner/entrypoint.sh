#!/bin/bash

if [ ! -z "${STARTUP_DELAY_IN_SECONDS}" ]; then
  echo "Delaying startup by ${STARTUP_DELAY_IN_SECONDS} seconds" 1>&2
  sleep ${STARTUP_DELAY_IN_SECONDS}
fi

if [ -z "${GITHUB_URL}" ]; then
  echo "Working with public GitHub" 1>&2
  GITHUB_URL="https://github.com/"
else
  length=${#GITHUB_URL}
  last_char=${GITHUB_URL:length-1:1}

  [[ $last_char != "/" ]] && GITHUB_URL="$GITHUB_URL/"; :
  echo "Github endpoint URL ${GITHUB_URL}"
fi

if [ -z "${RUNNER_NAME}" ]; then
  echo "RUNNER_NAME must be set" 1>&2
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
  echo "At least one of RUNNER_ORG or RUNNER_REPO or RUNNER_ENTERPRISE must be set" 1>&2
  exit 1
fi

if [ -z "${RUNNER_TOKEN}" ]; then
  echo "RUNNER_TOKEN must be set" 1>&2
  exit 1
fi

if [ -z "${RUNNER_REPO}" ] && [ -n "${RUNNER_GROUP}" ];then
  RUNNER_GROUPS=${RUNNER_GROUP}
fi

# Hack due to https://github.com/actions-runner-controller/actions-runner-controller/issues/252#issuecomment-758338483
if [ ! -d /runner ]; then
  echo "/runner should be an emptyDir mount. Please fix the pod spec." 1>&2
  exit 1
fi

sudo chown -R runner:docker /runner
cp -r /runnertmp/* /runner/

cd /runner

config_args=()
if [ "${RUNNER_FEATURE_FLAG_EPHEMERAL:-}" == "true" -a "${RUNNER_EPHEMERAL}" != "false" ]; then
  config_args+=(--ephemeral)
  echo "Passing --ephemeral to config.sh to enable the ephemeral runner."
fi

./config.sh --unattended --replace \
  --name "${RUNNER_NAME}" \
  --url "${GITHUB_URL}${ATTACH}" \
  --token "${RUNNER_TOKEN}" \
  --runnergroup "${RUNNER_GROUPS}" \
  --labels "${RUNNER_LABELS}" \
  --work "${RUNNER_WORKDIR}" "${config_args[@]}"

if [ -f /runner/.runner ]; then
  echo Runner has successfully been configured with the following data.
  cat /runner/.runner
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

if [ -n "${RUNNER_REGISTRATION_ONLY}" ]; then
  echo
  echo "This runner is configured to be registration-only. Exiting without starting the runner service..."
  exit 0
fi

mkdir ./externals
# Hack due to the DinD volumes
mv ./externalstmp/* ./externals/

for f in runsvc.sh RunnerService.js; do
  diff {bin,patched}/${f} || :
  sudo mv bin/${f}{,.bak}
  sudo mv {patched,bin}/${f}
done

args=()
if [ "${RUNNER_FEATURE_FLAG_EPHEMERAL:-}" != "true" -a "${RUNNER_EPHEMERAL}" != "false" ]; then
  args+=(--once)
  echo "Passing --once to runsvc.sh to enable the legacy ephemeral runner."
fi

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN
exec ./bin/runsvc.sh "${args[@]}"
