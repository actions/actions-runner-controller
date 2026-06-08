#!/bin/bash

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh" || {
    echo "Failed to source helper.sh"
    exit 1
}

export VERSION="$(chart_version "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental/Chart.yaml")"

SCALE_SET_NAME="update-strategy-$(date '+%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-sleepy-matrix.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

UPGRADE_MARKER="e2e-upgrade-${SCALE_SET_NAME}-$(date +%s)"

function install_arc() {
    echo "Installing ARC"
    helm install "${ARC_NAME}" \
        --namespace "${ARC_NAMESPACE}" \
        --create-namespace \
        --set controller.manager.container.image="${IMAGE_NAME}:${IMAGE_TAG}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental" \
        --debug

    if ! NAME="${ARC_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_arc; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function install_scale_set() {
    echo "Installing scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        --set scaleset.name="${SCALE_SET_NAME}" \
        --set scaleset.minRunners=5 \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function upgrade_scale_set() {
    echo "Upgrading scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"

    echo "Generated upgrade marker: ${UPGRADE_MARKER}"

    helm upgrade "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        --set scaleset.name="${SCALE_SET_NAME}" \
        --set runner.container.image="ghcr.io/actions/actions-runner:latest" \
        --set runner.container.command={"/home/runner/run.sh"} \
        --set runner.container.env[0].name="TEST" \
        --set runner.container.env[0].value="E2E TESTS" \
        --set "runner.pod.metadata.labels.e2e\.arc/upgrade-marker=${UPGRADE_MARKER}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug

}

function assert_idle_pod_recreated() {
    echo "Waiting for idle pod recreation"
    local count=0

    while true; do
        local pods
        if ! pods=$(kubectl get pods -n "${SCALE_SET_NAMESPACE}" -l "actions.github.com/scale-set-name=${SCALE_SET_NAME},e2e.arc/upgrade-marker=${UPGRADE_MARKER}" -o jsonpath='{.items[*].metadata.name}'); then
            echo "Failed to get pods: $pods"
            return 1
        fi

        if [[ -n "$pods" ]]; then
            echo "Found idle pod with upgrade marker: $pods"
            return 0
        fi

        if ((count >= 30)); then
            echo "Timeout waiting for idle pod recreation after upgrade"
            return 1
        fi

        echo "No idle pod with upgrade marker found yet, retrying... ($((count + 1))/30)"
        sleep 10
        ((count++))
    done

}

function main() {
    local failed=()
    local run_id=""

    build_image
    create_cluster
    install_arc
    install_scale_set

    upgrade_scale_set || failed+=("upgrade_scale_set")

    if ! run_id=$(WORKFLOW_FILE="${WORKFLOW_FILE}" SCALE_SET_NAME="${SCALE_SET_NAME}" start_workflow); then
        failed+=("run_workflow")
    fi

    assert_idle_pod_recreated || failed+=("assert_idle_pod_recreated")

    if [[ -n "${run_id}" ]]; then
        wait_for_run_completion "${run_id}" || failed+=("wait_for_run_completion")
    fi

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
