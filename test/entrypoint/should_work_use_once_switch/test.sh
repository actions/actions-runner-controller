#!/usr/bin/env bash

# UNITTEST: should work as non ephemeral
# Will simulate a scenario where ephemeral=false. expects:
# - the configuration step to be run exactly once
# - the entrypoint script to exit with no error
# - the run.sh script to run without the --once flag

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
export RUNNER_FEATURE_FLAG_EPHEMERAL="false"
export RUNNER_EPHEMERAL="true"

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
  unset RUNNER_EPHEMERAL
  unset RUNNER_FEATURE_FLAG_EPHEMERAL
}

log "Running the entrypoint"
log ""

# Run the runner entrypoint script which as a final step runs this
# unit tests run.sh as it was symlinked 
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

log "Testing if the configuration included the --once flag"
if ! grep -q -- '--once' ${RUNNER_HOME}/runner_args; then
  error "==============================================="
  error "The configuration did not include the --once flag, config printed below:"
  exit 1
fi

success "The configuration ran ${count} time(s)"

log "Testing if run.sh ran"
if [ ! -f "${RUNNER_HOME}/run_sh_ran" ]; then
  error "=============================="
  error "The runner service has not run"
  exit 1
fi
success "The service ran"
success ""
success "==========================="
success "Test completed successfully"

# TODO: This needs to be a run at the end always() really
trap cleanup SIGINT SIGTERM SIGQUIT EXIT