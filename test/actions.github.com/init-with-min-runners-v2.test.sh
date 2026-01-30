#!/bin/bash

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh" || {
    echo "Failed to source helper.sh"
    exit 1
}

export VERSION="$(chart_version "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental/Chart.yaml")"

SCALE_SET_NAME="init-min-runners-$(date +'%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-workflow.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

function install_arc() {
    echo "Installing ARC"
    helm install arc \
        --namespace "arc-systems" \
        --create-namespace \
        --set controller.manager.container.image="${IMAGE_NAME}:${IMAGE_TAG}" \
        --set controller.manager.config.updateStrategy="eventual" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental" \
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
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        --set scaleset.minRunners=5 \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental"

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function assert_5_runners() {
    echo "[*] Asserting 5 runners are created"
    local count=0
    while true; do
        pod_count=$(kubectl get pods -n arc-runners --no-headers | wc -l)

        if [[ "${pod_count}" = 5 ]]; then
            echo "[*] Found 5 runners as expected"
            break
        fi

        if [[ "$count" -ge 30 ]]; then
            echo "Timeout waiting for 5 pods to be created"
            exit 1
        fi
        sleep 1
        count=$((count + 1))
    done
}

function main() {
    local failed=()

    build_image
    create_cluster

    install_arc
    install_scale_set

    assert_5_runners || failed+=("assert_5_runners")

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
