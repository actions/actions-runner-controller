#!/bin/bash

if [ -z "${RUNNER_NAME}" ]; then
  echo "RUNNER_NAME must be set" 1>&2
  exit 1
fi

if [ -n "${RUNNER_ORG}" -a -n "${RUNNER_REPO}" ]; then
  ATTACH="${RUNNER_ORG}/${RUNNER_REPO}"
elif [ -n "${RUNNER_ORG}" ]; then
  ATTACH="${RUNNER_ORG}"
elif [ -n "${RUNNER_REPO}" ]; then
  ATTACH="${RUNNER_REPO}"
else
  echo "At least one of RUNNER_ORG or RUNNER_REPO must be set" 1>&2
  exit 1
fi

if [ -n "${RUNNER_LABELS}" ]; then
  LABEL_ARG="--labels ${RUNNER_LABELS}"
fi

if [ -z "${RUNNER_TOKEN}" ]; then
  echo "RUNNER_TOKEN must be set" 1>&2
  exit 1
fi

cd /runner
./config.sh --unattended --replace --name "${RUNNER_NAME}" --url "https://github.com/${ATTACH}" --token "${RUNNER_TOKEN}" ${LABEL_ARG}

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN
exec ./run.sh --once
