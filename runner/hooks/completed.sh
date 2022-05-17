#!/bin/bash

# shellcheck source=runner/logger.bash
source logger.bash

log.notice "Running ARC Completed Hooks"

for hook in $(ls /etc/arc/hooks/completed.d); do
  /etc/arc/hooks/completed.d/$hook "$1" "$2"
done
