#!/bin/bash

# shellcheck source=runner/logger.bash
source logger.bash

log.notice "Running ARC Job Completed Hooks"

for hook in $(ls /etc/arc/hooks/job-completed.d); do
  log.notice "Running hook: $hook"
  /etc/arc/hooks/job-completed.d/$hook "$1" "$2"
done
