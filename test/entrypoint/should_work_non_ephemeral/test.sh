#!/bin/bash

# UNITTEST: should work as non ephemeral
# Will simulate a scenario where ephemeral=false. expects:
# - the configuration step to be run exactly once
# - the entrypoint script to exit with no error
# - the runsvc.sh script to run without the --once flag

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
export RUNNER_EPHEMERAL=false

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
  unset RUNNER_EPHEMERAL
}

trap cleanup SIGINT SIGTERM SIGQUIT EXIT

log "Running the entrypoint"
log ""

../../../runner/entrypoint.sh 2> >(entrypoint_log)

if [ "$?" != "0" ]; then
  error "==========================================="
  error "Entrypoint script did not exit successfully"
  exit 1
fi

log "Testing if we went through the configuration step only once"
count=`cat ${RUNNER_HOME}/counter || echo "not_found"`
if [ ${count} != "1" ]; then
  error "==============================================="
  error "The configuration step was not run exactly once"
  exit 1
fi

success "The configuration ran ${count} time(s)"

log "Testing if runsvc ran"
if [ ! -f "${RUNNER_HOME}/runsvc_ran" ]; then
  error "=============================="
  error "The runner service has not run"
  exit 1
fi
success "The service ran"
success ""
success "==========================="
success "Test completed successfully"
