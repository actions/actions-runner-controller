#!/usr/bin/env bash

# UNITTEST: should work as non ephemeral
# Will simulate a scenario where ephemeral=false. expects:
# - the configuration step to be run exactly once
# - the startup script to exit with no error
# - the run.sh script to run without the --once flag

source ../assets/logging.sh

startup_log() {
  while read I; do
    printf "\tstartup.sh: $I\n"
  done
}

log "Setting up test area"
export RUNNER_HOME=$(pwd)/testarea
mkdir -p ${RUNNER_HOME}

log "Setting up the test"
export UNITTEST=true
export RUNNER_NAME="example_runner_name"
export RUNNER_REPO="myorg/myrepo"
export RUNNER_TOKEN="xxxxxxxxxxxxx"
export RUNNER_EPHEMERAL=false

# run.sh and config.sh get used by the runner's real entrypoint.sh and are part of actions/runner.
# We change symlink dummy versions so the entrypoint.sh can run allowing us to test the real entrypoint.sh
log "Symlink dummy config.sh and run.sh"
ln -s ../../assets/config.sh ${RUNNER_HOME}/config.sh
ln -s ../../assets/run.sh ${RUNNER_HOME}/run.sh

cleanup() {
  rm -rf ${RUNNER_HOME}
  unset UNITTEST
  unset RUNNERHOME
  unset RUNNER_NAME
  unset RUNNER_REPO
  unset RUNNER_TOKEN
  unset RUNNER_EPHEMERAL
}

# Always run cleanup when test ends regardless of how it ends
trap cleanup SIGINT SIGTERM SIGQUIT EXIT

log "Running the startup script"
log ""

# Run the runner entrypstartupoint script which as a final step runs this
# unit tests run.sh as it was symlinked
cd ../../../runner
export PATH=${PATH}:$(pwd)
./startup.sh 2> >(startup_log)

if [ "$?" != "0" ]; then
  error "==========================================="
  error "FAIL | Startup script did not exit successfully"
  exit 1
fi

log "Testing if we went through the configuration step only once"
count=`cat ${RUNNER_HOME}/counter || echo "not_found"`
if [ ${count} != "1" ]; then
  error "==============================================="
  error "FAIL | The configuration step was not run exactly once"
  exit 1
fi

success "PASS | The configuration ran ${count} time(s)"

log "Testing if run.sh ran"
if [ ! -f "${RUNNER_HOME}/run_sh_ran" ]; then
  error "=============================="
  error "FAIL | The runner service has not run"
  exit 1
fi
success "PASS | run.sh ran"
success ""
success "==========================="
success "Test completed successfully"
