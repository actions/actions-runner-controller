#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

TEST_DIR="$(realpath "${DIR}/../test/actions.github.com")"

TARGETS=()

function set_targets() {
    local cases="$(find "${TEST_DIR}" -name '*.test.sh' | sort | sed -e 's/\(.*\)/test_\1\.sh/')"

    mapfile -t TARGETS < <(echo "${cases}")

    echo $TARGETS
}

set_targets
