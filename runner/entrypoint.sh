#!/bin/bash

if [ -z "${RUNNER_NAME}" ]; then
  echo "RUNNER_NAME must be set" 1>&2
  exit 1
fi

if [ -z "${RUNNER_REPO}" ]; then
  echo "RUNNER_REPO must be set" 1>&2
  exit 1
fi

if [ -z "${RUNNER_TOKEN}" ]; then
  echo "RUNNER_TOKEN must be set" 1>&2
  exit 1
fi

cd /runner
./config.sh --unattended --replace --name "${RUNNER_NAME}" --url "https://github.com/${RUNNER_REPO}" --token "${RUNNER_TOKEN}"

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN
exec ./run.sh --once
