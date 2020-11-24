#!/bin/bash

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

if [ -n "${RUNNER_ORG}" ] && [ -n "${RUNNER_REPO}" ]; then
  ATTACH="${RUNNER_ORG}/${RUNNER_REPO}"
elif [ -n "${RUNNER_ORG}" ]; then
  ATTACH="${RUNNER_ORG}"
elif [ -n "${RUNNER_REPO}" ]; then
  ATTACH="${RUNNER_REPO}"
else
  echo "At least one of RUNNER_ORG or RUNNER_REPO must be set" 1>&2
  exit 1
fi

if [ -n "${RUNNER_WORKDIR}" ]; then
  WORKDIR_ARG="--work ${RUNNER_WORKDIR}"
fi

if [ -n "${RUNNER_LABELS}" ]; then
  LABEL_ARG="--labels ${RUNNER_LABELS}"
fi

if [ -z "${RUNNER_TOKEN}" ]; then
  echo "RUNNER_TOKEN must be set" 1>&2
  exit 1
fi

if [ -z "${RUNNER_REPO}" ] && [ -n "${RUNNER_ORG}" ] && [ -n "${RUNNER_GROUP}" ];then
  RUNNER_GROUP_ARG="--runnergroup ${RUNNER_GROUP}"
fi

cd /runner
./config.sh --unattended --replace --name "${RUNNER_NAME}" --url "${GITHUB_URL}${ATTACH}" --token "${RUNNER_TOKEN}" ${RUNNER_GROUP_ARG} ${LABEL_ARG} ${WORKDIR_ARG}

# Hack due to the DinD volumes
mv ./externalstmp/* ./externals/

for f in runsvc.sh RunnerService.js; do
  diff {bin,patched}/${f} || :
  sudo mv bin/${f}{,.bak}
  sudo mv {patched,bin}/${f}
done

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN
exec ./bin/runsvc.sh --once
