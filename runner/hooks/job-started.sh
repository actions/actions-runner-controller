#!/bin/bash

# shellcheck source=runner/logger.bash
source logger.bash

log.notice "Running ARC Job Started Hooks"

for hook in $(ls /etc/arc/hooks/job-started.d); do
  log.notice "Running hook: $hook"
  /etc/arc/hooks/job-started.d/$hook "$1" "$2"
done
