#!/usr/bin/env bash

# UNITTEST: retry config
# Will simulate a configuration failure and expects:
# - the configuration step to be run 10 times
# - the entrypoint script to exit with error code 2
# - the run.sh script to never run.

source ../logging.sh

entrypoint_log() {
  while read I; do
    printf "\tentrypoint.sh: $I\n"
  done
}

log "Setting up the test"
export UNITTEST=true
export RUNNER_HOME=test
export RUNNER_NAME="example_runner_name"
export RUNNER_REPO="myorg/myrepo"
export RUNNER_TOKEN="xxxxxxxxxxxxx"

mkdir -p ${RUNNER_HOME}/bin
# run.sh and config.sh get used by the runner's real entrypoint.sh
# set the runner/entrypoint.sh to use this tests dummy versions via
# a symlink
ln -s ../config.sh ${RUNNER_HOME}/config.sh
ln -s ../../run.sh ${RUNNER_HOME}/bin/run.sh

cleanup() {
  rm -rf ${RUNNER_HOME}
  unset UNITTEST
  unset RUNNERHOME
  unset RUNNER_NAME
  unset RUNNER_REPO
  unset RUNNER_TOKEN
}

trap cleanup SIGINT SIGTERM SIGQUIT EXIT

log "Running the entrypoint"
log ""

# Run the runner entrypoint script which as a final step runs this
# unit tests run.sh as it was symlinked
../../../runner/entrypoint.sh 2> >(entrypoint_log)

if [ "$?" != "2" ]; then
  error "========================================="
  error "Configuration should have thrown an error"
  exit 1
fi
success "Entrypoint didn't complete successfully"
success ""

log "Checking the counter, should have 10 iterations"
count=`cat ${RUNNER_HOME}/counter || "notfound"`
if [ "${count}" != "10" ]; then
  error "============================================="
  error "The retry loop should have done 10 iterations"
  exit 1
fi
success "Retry loop went up to 10"
success

log "Checking that run.sh never ran"
if [ -f ${RUNNER_HOME}/run_sh_ran ]; then
  error "================================================================="
  error "run.sh was invoked, entrypoint.sh should have failed before that."
  exit 1
fi

success "run.sh never ran"
success
success "==========================="
success "Test completed successfully"
exit 0
