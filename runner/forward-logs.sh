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
echo "=== GITHUB ACTIONS BUILD LOGS START ==="
found_logs=0
find "$DIAG_DIR" -name "Worker_*.log" -type f -print0 2>/dev/null | while IFS= read -r -d '' log_file; do
    found_logs=1
    echo "--- Log from: $(basename "$log_file") ---"
    cat "$log_file"
done

if [ "$found_logs" -eq 0 ]; then
    echo "No worker log files found"
fi
echo "=== GITHUB ACTIONS BUILD LOGS END ==="
