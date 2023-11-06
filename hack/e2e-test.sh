#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

TEST_DIR="$(realpath "${DIR}/../test/actions.github.com")"

export PLATFORMS="linux/amd64"

TARGETS=()

function set_targets() {
    local cases="$(find "${TEST_DIR}" -name '*.test.sh' | sed "s#^${TEST_DIR}/##g" )"

    mapfile -t TARGETS < <(echo "${cases}")

    echo $TARGETS
}

function main() {
    local failed=()

    for target in "${TARGETS[@]}"; do
        echo "============================================================"
        test="${TEST_DIR}/${target}"
        if [[ ! -x "${test}" ]]; then
            echo "Error: test ${test} is not executable or not found"
            failed+=("${test}")
            continue
        fi

        echo "Running test ${target}"
        if ! "${test}"; then
            failed+=("${target}")
            echo "---------------------------------"
            echo "FAILED: ${target}"
        else
            echo "---------------------------------"
            echo "PASSED: ${target}"
        fi
        echo "============================================================"
    done

    if [[ "${#failed[@]}" -gt 0 ]]; then
        echo "Failed tests:"
        for fail in "${failed[@]}"; do
            echo "  ${fail}"
        done
        exit 1
    fi
}

set_targets

main
