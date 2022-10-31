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

if [ -f /runner/.runner ]; then
# If the runner failed with the following error:
#   √ Connected to GitHub
#   Failed to create a session. The runner registration has been deleted from the server, please re-configure.
#   Runner listener exit with terminated error, stop the service, no retry needed.
#   Exiting runner...
# It might have failed to delete the .runner file.
# We use the existence of the .runner file as the indicator that the runner agent has not stopped yet.
# Remove it by ourselves now, so that the dockerd sidecar prestop won't hang waiting for the .runner file to appear.
  echo "Removing the .runner file"
  rm -f /runner/.runner
fi

trap - TERM
