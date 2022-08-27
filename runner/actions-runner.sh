#!/bin/bash
source logger.bash
source graceful-stop.bash

dumb-init bash <<'SCRIPT' &
source logger.bash
source wait.bash

entrypoint.sh
SCRIPT

runner_init_pid=$!
log.notice "Runner init started with pid $runner_init_pid"
wait $runner_init_pid
log.notice "Runner init exited. Exiting this process with code 0 so that the container and the pod is GC'ed Kubernetes soon."
