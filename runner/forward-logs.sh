#!/usr/bin/env bash
set -euo pipefail

# Forward GitHub Actions build logs to stdout
# This script finds and outputs worker log files that contain job execution logs

if [ "${FORWARD_BUILD_LOGS:-false}" != "true" ]; then
    exit 0
fi

RUNNER_HOME=${RUNNER_HOME:-/runner}
DIAG_DIR="${RUNNER_HOME}/_diag/pages"

if [ ! -d "$DIAG_DIR" ]; then
    echo "No diagnostic logs directory found at $DIAG_DIR"
    exit 0
fi

# Find worker log files (these contain the actual job execution logs)
WORKER_LOGS=$(find "$DIAG_DIR" -name "Worker_*.log" -type f 2>/dev/null || true)

if [ -z "$WORKER_LOGS" ]; then
    echo "No worker log files found"
    exit 0
fi

echo "=== GITHUB ACTIONS BUILD LOGS START ==="
for log_file in $WORKER_LOGS; do
    echo "--- Log from: $(basename "$log_file") ---"
    cat "$log_file"
done
echo "=== GITHUB ACTIONS BUILD LOGS END ==="
