#!/bin/bash

./config.sh --unattended --replace --name "${RUNNER_NAME}" --url "https://github.com/${RUNNER_REPO}" --token "${RUNNER_TOKEN}"

unset RUNNER_NAME RUNNER_REPO RUNNER_TOKEN
exec ./run.sh --once
