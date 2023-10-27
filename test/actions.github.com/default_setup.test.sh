#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(relpath "${DIR}/../..")"

source "${DIR}/helper.sh"

SCALE_SET_NAME="default-$(date + '%M%S')$(((${RANDOM} + 100) % 100 +  1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-workflow.yaml"


function install_scale_set() {
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set githubConfigSecret.github_token="${GITHUB_TOKEN}" \
        ${ROOT_DIR}/charts/gha-runner-scale-set \
        --debug

    NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" wait_for_scale_set
}

function run_workflow() {
    gh workflow run -R "${TARGET_ORG}/${TARGET_REPO}" "${WORKFLOW_FILE}"

    local count=0
    while true; do
        STATUS=$(gh run list -R "${TARGET_ORG}/${TARGET_REPO}" --limit 1 --limit 1 --json status --jq '.[0].status')
        if [ "${STATUS}" != "completed" ]; then
            sleep 30
            count=$((count + 1))
            continue
        fi

        CONCLUSION=$(gh run list -R "${TARGET_ORG}/${TARGET_REPO}" --limit 1 --limit 1 --json conclusion --jq '.[0].conclusion')
        if [[ "${CONCLUSION}" != "success" ]]; then
            echo "Workflow failed"
            return 1
        fi

        return 0
    done
}

function main() {
    local failed=()

    build_image
    create_cluster
    install_arc

    install_scale_set || failed+=("install_scale_set")
    run_workflow || failed+=("run_workflow")
    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    if [[ "${#failed[@]}" -ne 0 ]]; then
        echo "----------------------------------"
        echo "The following tests failed:"
        for test in "${failed[@]}"; do
            echo "  - ${test}"
        done

        exit 1
    fi
}

main
