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

echo "Creating configuration file to /etc/default/actions-runner..."
cat << EOS > /etc/default/actions-runner
RUNNER_NAME=${RUNNER_NAME}
RUNNER_REPO=${RUNNER_REPO}
RUNNER_TOKEN=${RUNNER_TOKEN}
EOS

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN

echo "Starting systemd..."
echo "Use 'systemctl' or 'journalctl' in the container to see the state of systemd."
exec /sbin/init
