#!/usr/bin/env bash

# UNITTEST: retry config
# Will simulate a configuration failure and expects:
# - the configuration step to be run 10 times
# - the startup script to exit with error code 2
# - the run.sh script to never run.

source ../assets/logging.sh

startup_log() {
  while read I; do
    printf "\tstartup.sh: $I\n"
  done
}

log "Setting up test area"
export RUNNER_HOME=$(pwd)/testarea
mkdir -p ${RUNNER_HOME}

log "Setting up the test config"
export UNITTEST=true
export FAIL_RUNNER_CONFIG_SETUP=true
export RUNNER_NAME="example_runner_name"
export RUNNER_REPO="myorg/myrepo"
export RUNNER_TOKEN="xxxxxxxxxxxxx"

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
  unset FAIL_RUNNER_CONFIG_SETUP
}

# Always run cleanup when test ends regardless of how it ends
trap cleanup SIGINT SIGTERM SIGQUIT EXIT

log "Running the startup script"
log ""

# Run the runner startup script which as a final step runs this
# unit tests run.sh as it was symlinked
cd ../../../runner
export PATH=${PATH}:$(pwd)
./startup.sh 2> >(startup_log)

if [ "$?" != "2" ]; then
  error "========================================="
  error "FAIL | Configuration should have thrown an error"
  exit 1
fi

success "PASS | Entrypoint didn't complete successfully"

log "Checking the counter, should have 10 iterations"
count=`cat ${RUNNER_HOME}/counter || "notfound"`
if [ "${count}" != "10" ]; then
  error "============================================="
  error "FAIL | The retry loop should have done 10 iterations"
  exit 1
fi
success "PASS | Retry loop went up to 10"

log "Checking that run.sh never ran"
if [ -f ${RUNNER_HOME}/run_sh_ran ]; then
  error "================================================================="
  error "FAIL | run.sh was invoked, entrypoint.sh should have failed before that."
  exit 1
fi

success "PASS | run.sh never ran"
success
success "==========================="
success "Test completed successfully"
exit 0
