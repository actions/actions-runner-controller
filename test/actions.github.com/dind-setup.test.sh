#!/bin/bash

DIR="$(dirname "${BAASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(realpath "${DIR}/../../")"

source "${DIR}/helper.sh"

SCALE_SET_NAME="dind-$(date +'%M%S')$(((${RANDOM} + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="example.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

function install_arc() {
    echo "Installing ARC"

    helm install "${ARC_NAME}" \
        --namespace "${ARC_NAMESPACE}" \
        --create-namespace \
        --set image.repository="${IMAGE_NAME}" \
        --set image.tag="${IMAGE_TAG}" \
        ${ROOT_DIR}/charts/gha-runner-scale-set-controller \
        --debug

    if ! NAME="${ARC_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_arc; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function install_scale_set() {
    echo "Installing scale set ${SCALE_SET_NAMESPACE}/${SCALE_SET_NAME}"

    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set githubConfigSecret.github_token="${GITHUB_TOKEN}" \
        --set containerMode.type="dind" \
        ${ROOT_DIR}/charts/gha-runner-scale-set \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${}"
}

function main() {
    local failed=()

    builf_image
    create_cluster

    NAME="${ARC_NAME}" NAMESPACE="${ARC_NAMESPACE}" install_arc

    install_scale_set || failed+=("install_scale_set")
    run_workflow || failed+=("run_workflow")
    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    delete_cluster

    if [[ "${#failed[@]}" -ne 0 ]]; then
        echo "----------------------------------"
        echo "The following tests failed:"
        for test in "${failed[@]}"; do
            echo "  - ${test}"
        done
        return 1
    fi
}

main
