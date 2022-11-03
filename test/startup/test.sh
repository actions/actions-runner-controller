#!/usr/bin/env bash

source assets/logging.sh

for unittest in ./should*; do
  log "**********************************"
  log " UNIT TEST: ${unittest}"
  log "**********************************"
  log ""
  cd ${unittest}
  ./test.sh
  ret_code=$?
  cd ..

  log ""
  log ""
  if [ "${ret_code}" = "0" ]; then
    success "Completed: unit test ${unittest}"
  else
    error "Completed: unit test ${unittest} with errors"
    failed="true"
  fi
done

if [ -n "${failed:-}" ]; then
  error ""
  error "*************************************"
  error "All unit tests completed, with errors"
  error "*************************************"
  exit 1
else
  success ""
  success "***************************************"
  success "All unit tests completed with no errors"
  success "***************************************"
fi
