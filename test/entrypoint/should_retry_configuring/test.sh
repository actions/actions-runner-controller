#!/bin/bash

# UNITTEST: retry config
# Will simulate a configuration failure and expects:
# - the configuration step to be run 10 times
# - the entrypoint script to exit with error code 2
# - the runsvc.sh script to never run.

source ../logging.sh

entrypoint_log() {
  while read I; do
    printf "\tentrypoint.sh: $I\n"
  done
}

log "Setting up the test"
export UNITTEST=true
export RUNNER_HOME=localhome
export RUNNER_NAME="example_runner_name"
export RUNNER_REPO="myorg/myrepo"
export RUNNER_TOKEN="xxxxxxxxxxxxx"

mkdir -p ${RUNNER_HOME}/bin
# add up the config.sh and runsvc.sh
ln -s ../config.sh ${RUNNER_HOME}/config.sh
ln -s ../../runsvc.sh ${RUNNER_HOME}/bin/runsvc.sh

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

log "Checking that runsvc never ran"
if [ -f ${RUNNER_HOME}/runsvc_ran ]; then
  error "================================================================="
  error "runsvc was invoked, entrypoint.sh should have failed before that."
  exit 1
fi

success "runsvc.sh never ran"
success
success "==========================="
success "Test completed successfully"
exit 0
