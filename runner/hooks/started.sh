#!/bin/bash

# shellcheck source=runner/logger.bash
source logger.bash

log.notice "Running ARC Started Hooks"

for hook in $(ls /etc/arc/hooks/started.d); do
  /etc/arc/hooks/started.d/$hook "$1" "$2"
done
