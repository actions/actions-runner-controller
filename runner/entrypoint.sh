#!/bin/bash
source logger.sh
source graceful-stop.sh
trap graceful_stop TERM

dumb-init bash <<'SCRIPT' &
source logger.sh

startup.sh
SCRIPT

RUNNER_INIT_PID=$!
log.notice "Runner init started with pid $RUNNER_INIT_PID"
wait $RUNNER_INIT_PID
log.notice "Runner init exited. Exiting this process with code 0 so that the container and the pod is GC'ed Kubernetes soon."

trap - TERM
