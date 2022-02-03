#!/bin/bash

# UNITTEST: should work disable update
# Will simulate a scneario where disableupdate=true. expects:
# - the configuration step to be run exactly once
# - the entrypoint script to exit with no error
# - the config.sh script to run with the --disableupdate flag set to 'true'.

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
export DISABLE_RUNNER_UPDATE="true"

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

if [ "$?" != "0" ]; then
  error "=========================="
  error "Test completed with errors"
  exit 1
fi

log "Testing if the configuration step was run only once"
count=`cat ${RUNNER_HOME}/counter || echo "not_found"`
if [ ${count} != "1" ]; then
  error "==============================================="
  error "The configuration step was not run exactly once"
  exit 1
fi
success "The configuration ran ${count} time(s)"

log "Testing if the configuration included the --disableupdate flag"
if ! grep -q -- '--disableupdate' ${RUNNER_HOME}/runner_config; then
  error "==============================================="
  error "The configuration should not include the --disableupdate flag"
  exit 1
fi

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
