#!/usr/bin/env bash
set -euo pipefail

# Forward GitHub Actions build logs to stdout.
# GitHub runner hooks capture normal hook output, so redirect to the
# container's stdout/stderr when running as a Kubernetes container.

if [ "${FORWARD_BUILD_LOGS:-false}" != "true" ]; then
    exit 0
fi

if [ -w /proc/1/fd/1 ]; then
    exec >/proc/1/fd/1
fi

if [ -w /proc/1/fd/2 ]; then
    exec 2>/proc/1/fd/2
fi

RUNNER_HOME=${RUNNER_HOME:-/runner}
DIAG_DIR="${RUNNER_HOME}/_diag"
PAGES_DIR="${DIAG_DIR}/pages"

if [ ! -d "$DIAG_DIR" ]; then
    echo "No diagnostic logs directory found at $DIAG_DIR"
    exit 0
fi

emit_logs() {
    local log_dir="$1"
    local pattern="$2"
    local label="$3"
    local found_logs=0

    if [ ! -d "$log_dir" ]; then
        return 1
    fi

    while IFS= read -r -d '' log_file; do
        found_logs=1
        echo "--- ${label}: ${log_file} ---"
        cat "$log_file"
        printf '\n'
    done < <(find "$log_dir" -maxdepth 1 -name "$pattern" -type f -print0 2>/dev/null)

    [ "$found_logs" -eq 1 ]
}

echo "=== GITHUB ACTIONS BUILD LOGS START ==="

if ! emit_logs "$PAGES_DIR" "*.log" "Build log page" &&
    ! emit_logs "$DIAG_DIR" "Worker_*.log" "Worker log"; then
    echo "No build log files found"
fi

echo "=== GITHUB ACTIONS BUILD LOGS END ==="
