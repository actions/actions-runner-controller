#!/usr/bin/env bash

# UNITTEST: should work to disable default labels
# Will simulate a scneario where no-default-labels=true. expects:
# - the configuration step to be run exactly once
# - the startup script to exit with no error
# - the config.sh script to run with the --no-default-labels flag set to 'true'.

source ../assets/logging.sh

startup_log() {
  while read I; do
    printf "\tstartup.sh: $I\n"
  done
}

log "Setting up test area"
export RUNNER_HOME=testarea
mkdir -p ${RUNNER_HOME}

log "Setting up the test"
export UNITTEST=true
export RUNNER_NAME="example_runner_name"
export RUNNER_REPO="myorg/myrepo"
export RUNNER_TOKEN="xxxxxxxxxxxxx"
export RUNNER_NO_DEFAULT_LABELS="true"

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
  unset RUNNER_NO_DEFAULT_LABELS
}

# Always run cleanup when test ends regardless of how it ends
trap cleanup SIGINT SIGTERM SIGQUIT EXIT

log "Running the startup script"
log ""

# run.sh and config.sh get used by the runner's real startup.sh and are part of actions/runner.
# We change symlink dummy versions so the startup.sh can run allowing us to test the real entrypoint.sh
../../../runner/startup.sh 2> >(startup_log)

if [ "$?" != "0" ]; then
  error "=========================="
  error "FAIL | Test completed with errors"
  exit 1
fi

log "Testing if the configuration step was run only once"
count=`cat ${RUNNER_HOME}/counter || echo "not_found"`
if [ ${count} != "1" ]; then
  error "==============================================="
  error "FAIL | The configuration step was not run exactly once"
  exit 1
fi
success "PASS | The configuration ran ${count} time(s)"

log "Testing if the configuration included the --no-default-labels flag"
if ! grep -q -- '--no-default-labels' ${RUNNER_HOME}/runner_config; then
  error "==============================================="
  error "FAIL | The configuration should not include the --no-default-labels flag"
  exit 1
fi

success "PASS | The --no-default-labels switch was included in the configuration"

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
