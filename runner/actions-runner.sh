#!/bin/bash
source logger.bash
source graceful-stop.bash
trap graceful_stop TERM

dumb-init bash <<'SCRIPT' &
source logger.bash
source wait.bash

entrypoint.sh
SCRIPT

RUNNER_INIT_PID=$!
log.notice "Runner init started with pid $RUNNER_INIT_PID"
wait $RUNNER_INIT_PID
log.notice "Runner init exited. Exiting this process with code 0 so that the container and the pod is GC'ed Kubernetes soon."

trap - TERM
